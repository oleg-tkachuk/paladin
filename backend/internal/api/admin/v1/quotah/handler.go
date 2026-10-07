// Package quotah implements the admin QuotaService — set/get usage caps.
package quotah

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// EventProducer mirrors the seam used by tenanth / bucketh /
// objectkeyh — narrow interface, *worker.Dispatcher implements it.
type EventProducer interface {
	Dispatch(ctx context.Context, tenantID string, evt worker.Event) (int, error)
	// DispatchTx fans the event out on the caller's tx so the outbox rows
	// commit atomically with the quota upsert (ADR-0003).
	DispatchTx(ctx context.Context, tx pgx.Tx, tenantID string, evt worker.Event) (int, error)
}

// Repository is the admindomain QuotaRepository plus the ADR-0003 tx seam
// (RunInTx + *Tx upserts). Kept local so admindomain stays pgx-free; the
// concrete adapter satisfies both. RunInTx supplies the tx; the *Tx methods
// run the upsert (and the owner-tenant read for bucket scope) on it.
type Repository interface {
	admindomain.QuotaRepository
	RunInTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error
	UpsertTenantTx(ctx context.Context, tx pgx.Tx, q admindomain.Quota) error
	UpsertBucketTx(ctx context.Context, tx pgx.Tx, q admindomain.Quota) error
	GetBucketTx(ctx context.Context, tx pgx.Tx, backendID, bucketName string) (admindomain.Quota, error)
	GetByID(ctx context.Context, quotaID uuid.UUID) (admindomain.Quota, error)
	ResetBucketDaily(ctx context.Context, quotaID uuid.UUID, at time.Time) error
}

type Handler struct {
	repo   Repository
	policy cedar.Authorizer

	events EventProducer
	log    *zap.Logger
}

func NewHandler(r Repository, policy cedar.Authorizer) *Handler {
	if policy == nil {
		panic("quotah: policy authorizer is required")
	}
	return &Handler{repo: r, policy: policy, log: zap.NewNop()}
}

// SetEventProducer / SetLogger — same opt-in contract as the rest
// of the producer-wired handlers.
func (h *Handler) SetEventProducer(p EventProducer) { h.events = p }
func (h *Handler) SetLogger(l *zap.Logger) {
	if l != nil {
		h.log = l
	}
}

// dispatchEventTx fans the event out on the caller's tx so the outbox rows
// commit atomically with the quota upsert (ADR-0003). Returns the error so
// the caller rolls back; nil-safe (and skips a Nil tenant, same as
// dispatchEvent).
func (h *Handler) dispatchEventTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, eventType, resourceName string, payload map[string]any) error {
	if h.events == nil || tenantID == uuid.Nil {
		return nil
	}
	actor := ""
	if p, err := auth.PrincipalFromContext(ctx); err == nil {
		actor = p.Subject
	}
	_, err := h.events.DispatchTx(ctx, tx, tenantID.String(), worker.Event{
		Type:         eventType,
		At:           time.Now().UTC(),
		TenantID:     tenantID.String(),
		ResourceName: resourceName,
		ActorSubject: actor,
		Payload:      payload,
	})
	return err
}

// authorize gates a quota RPC against Cedar. The Resource carries the
// tenant or bucket coordinates so policies can pin "tenant.admin manages
// own quota" via resource.tenant_id == principal.tenant_id.
// On success it returns a context scoped to q.TenantID, so the RLS pool
// reads the quota rows the caller was just authorised for rather than the
// caller's own. A bucket-scoped quota carries no tenant; the context is
// returned unchanged there and the caller's own scope applies.
func (h *Handler) authorize(ctx context.Context, action cedar.Action, q admindomain.Quota) (context.Context, error) {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return ctx, connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipal(p),
		action,
		&cedar.Resource{
			TenantID:   q.TenantID,
			BackendID:  q.BackendID,
			BucketName: q.BucketName,
		},
		cedar.RequestContext{Now: time.Now()},
	)
	if err != nil {
		return ctx, apiutil.MapError(fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return ctx, connect.NewError(connect.CodePermissionDenied, "denied by policy")
	}
	return auth.WithActingTenant(ctx, q.TenantID), nil
}

func (h *Handler) GetTenantQuota(ctx context.Context, tenantID uuid.UUID) (*admindomain.Quota, error) {
	caller, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	if !apiutil.HasRole(ctx, apiutil.RolePlatformAdmin) && tenantID != caller {
		return nil, connect.NewError(connect.CodePermissionDenied, "cross-tenant denied")
	}
	if ctx, err = h.authorize(ctx, cedar.ActionReadQuota, admindomain.Quota{TenantID: tenantID}); err != nil {
		return nil, err
	}
	q, err := h.repo.GetTenant(ctx, tenantID)
	if err != nil {
		return nil, apiutil.MapError(err)
	}
	return &q, nil
}

func (h *Handler) GetBucketQuota(ctx context.Context, backendID, bucketName string) (*admindomain.Quota, error) {
	if err := apiutil.RequireAnyRole(ctx,
		apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin, apiutil.RoleTenantAdmin); err != nil {
		return nil, err
	}
	ctx, err := h.authorize(ctx, cedar.ActionReadQuota,
		admindomain.Quota{BackendID: backendID, BucketName: bucketName})
	if err != nil {
		return nil, err
	}
	q, err := h.repo.GetBucket(ctx, backendID, bucketName)
	if err != nil {
		return nil, apiutil.MapError(err)
	}
	return &q, nil
}

// SetQuota upserts a quota. Tenant-scoped quota = TenantID set. Bucket-scoped
// = BackendID + BucketName set. Mask: max_total_bytes / max_object_count /
// max_bytes_per_day / max_objects_per_day.
// SetQuota writes the caps for one scope, guarded by q.ResourceVersion.
//
// The guard is checked in SQL, in the upsert's DO UPDATE clause: a row whose
// stored version differs updates nothing, and the adapter turns the empty
// result into ErrVersionMismatch → Aborted. Passing 0 means "I believe no row
// exists"; if one does, that is a conflict too. Before this the upsert was a
// blind overwrite — two operators editing limits at once, last write wins,
// neither told.
func (h *Handler) SetQuota(ctx context.Context, q admindomain.Quota, mask []string) (*admindomain.Quota, error) {
	if err := apiutil.RequireAnyRole(ctx, apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin); err != nil {
		return nil, err
	}
	ctx, err := h.authorize(ctx, cedar.ActionManageQuota, q)
	if err != nil {
		return nil, err
	}
	// Mask is informational here — upsert writes all four caps. Future:
	// per-field UPDATE if real partial semantics needed.
	_ = mask

	tenantScope := q.TenantID != uuid.Nil
	bucketScope := q.BackendID != "" && q.BucketName != ""
	if tenantScope == bucketScope {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			"exactly one of tenant_id or (backend_id, bucket_name) must be set")
	}
	if tenantScope {
		// Upsert + paladin.quota.set in one tx (ADR-0003). The event payload is
		// the caps we just wrote (== q), so no in-tx read-back is needed;
		// the full row is fetched post-commit for the response.
		if err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			if e := h.repo.UpsertTenantTx(ctx, tx, q); e != nil {
				return e
			}
			return h.dispatchEventTx(ctx, tx, q.TenantID, "paladin.quota.set",
				fmt.Sprintf("tenants/%s/quota", q.TenantID),
				map[string]any{
					"tenant_id":           q.TenantID.String(),
					"scope":               "tenant",
					"max_total_bytes":     q.MaxTotalBytes,
					"max_object_count":    q.MaxObjectCount,
					"max_bytes_per_day":   q.MaxBytesPerDay,
					"max_objects_per_day": q.MaxObjectsPerDay,
				})
		}); err != nil {
			return nil, apiutil.MapError(err)
		}
		got, err := h.repo.GetTenant(ctx, q.TenantID)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		return &got, nil
	}
	// Bucket scope: the fan-out target is the bucket's owner, which the
	// read-back joins from `buckets` — so the upsert, the read-back and the
	// event all run on one tx (ADR-0003). `got` is reused for the response.
	var got admindomain.Quota
	if err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if e := h.repo.UpsertBucketTx(ctx, tx, q); e != nil {
			return e
		}
		var e error
		got, e = h.repo.GetBucketTx(ctx, tx, q.BackendID, q.BucketName)
		if e != nil {
			return e
		}
		// A shared bucket has no owner, so its quota has nobody to tell.
		return h.dispatchEventTx(ctx, tx, got.OwnerTenantID, "paladin.quota.set",
			fmt.Sprintf("tenants/%s/buckets/%s/%s/quota", got.OwnerTenantID, got.BackendID, got.BucketName),
			map[string]any{
				"tenant_id":           got.OwnerTenantID.String(),
				"scope":               "bucket",
				"backend_id":          got.BackendID,
				"bucket_name":         got.BucketName,
				"max_total_bytes":     got.MaxTotalBytes,
				"max_object_count":    got.MaxObjectCount,
				"max_bytes_per_day":   got.MaxBytesPerDay,
				"max_objects_per_day": got.MaxObjectsPerDay,
			})
	}); err != nil {
		return nil, apiutil.MapError(err)
	}
	return &got, nil
}

func (h *Handler) ResetUsage(ctx context.Context, quotaID uuid.UUID) error {
	if err := apiutil.RequireRole(ctx, apiutil.RolePlatformAdmin); err != nil {
		return err
	}
	// The request names only the quota id, so load the row first: Cedar then
	// sees its tenant or bucket, and the reset runs as that tenant. The read
	// is cross-tenant because the quota usually belongs to someone other
	// than the platform admin calling.
	q, err := h.repo.GetByID(auth.WithCrossTenantRead(ctx), quotaID)
	if err != nil {
		return apiutil.MapError(err)
	}
	ctx, err = h.authorize(ctx, cedar.ActionResetQuotaUsage, q)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if q.TenantID == uuid.Nil {
		// A bucket quota is platform configuration outside RLS
		// (044_bucket_quotas.sql); there is no tenant to act as.
		return apiutil.MapError(h.repo.ResetBucketDaily(ctx, quotaID, now))
	}
	return apiutil.MapError(h.repo.ResetDaily(ctx, quotaID, now))
}
