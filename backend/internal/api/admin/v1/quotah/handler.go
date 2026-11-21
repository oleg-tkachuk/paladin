// Package quotah implements the admin QuotaService — set/get usage caps.
package quotah

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// EventQuotaSet is the event a quota write records.
const EventQuotaSet = "paladin.quota.set"

// The scope a quota event's payload names.
const (
	scopeTenant = "tenant"
	scopeBucket = "bucket"
)

// The keys of a quota event's payload beside the caps, which are spelled as
// the mask spells them.
const (
	payloadTenantID   = "tenant_id"
	payloadScope      = "scope"
	payloadBackendID  = "backend_id"
	payloadBucketName = "bucket_name"
)

// The resource names a quota event carries.
const (
	tenantQuotaResource = "tenants/%s/quota"
	bucketQuotaResource = "tenants/%s/buckets/%s/%s/quota"
)

// The operations a log line names.
const (
	opGetTenantQuota = "GetTenantQuota"
	opGetBucketQuota = "GetBucketQuota"
	opSetQuota       = "SetQuota"
	opResetUsage     = "ResetUsage"
)

// The messages a change is logged under.
const (
	msgQuotaSet        = "quota set"
	msgQuotaUsageReset = "quota usage reset"
)

// EventProducer mirrors the seam used by tenanth / bucketh /
// objectkeyh — narrow interface, *worker.EventRecorder implements it.
type EventProducer interface {
	// RecordTx records the event on the caller's tx so it commits
	// atomically with the quota upsert (ADR-0003).
	RecordTx(ctx context.Context, tx pgx.Tx, tenantID string, evt worker.Event) error
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
// of the producer-wired handlers. The logger records each change, refusal
// and store failure; unset, nothing is.
func (h *Handler) SetEventProducer(p EventProducer) { h.events = p }
func (h *Handler) SetLogger(l *zap.Logger) {
	if l != nil {
		h.log = l
	}
}

// answer logs err, met by op, and returns what the caller is told: a refusal
// — a code the handler chose, a quota not there, a version that moved —
// keeps its code and is a warning; anything else is the store failing,
// internal, and an error.
func (h *Handler) answer(ctx context.Context, op string, err error, fields ...zap.Field) error {
	mapped := apiutil.MapError(err)
	fields = append(fields, zap.String("op", op), actor(ctx), zap.Error(err))
	if connect.CodeOf(mapped) == connect.CodeInternal {
		h.log.Error("quota store failed", fields...)
	} else {
		h.log.Warn("quota request refused", append(fields, zap.Stringer("code", connect.CodeOf(mapped)))...)
	}
	return mapped
}

// actor names the caller ctx carries, "" for none.
func actor(ctx context.Context) zap.Field {
	return zap.String("actor", subject(ctx))
}

// subject is the subject of the principal ctx carries, "" for none.
func subject(ctx context.Context) string {
	if p, err := auth.PrincipalFromContext(ctx); err == nil {
		return p.Subject
	}
	return ""
}

// scopeFields names the quota q is: its id when it has one, and its tenant or
// its bucket.
func scopeFields(q admindomain.Quota) []zap.Field {
	var fields []zap.Field
	if q.QuotaID != uuid.Nil() {
		fields = append(fields, zap.String("quota_id", q.QuotaID.String()))
	}
	if q.TenantID != uuid.Nil() {
		return append(fields, zap.String("tenant_id", q.TenantID.String()))
	}
	return append(fields, zap.String("backend_id", q.BackendID), zap.String("bucket_name", q.BucketName))
}

// dispatchEventTx records the event on the caller's tx so it commits
// atomically with the quota upsert (ADR-0003). Returns the error so
// the caller rolls back; nil-safe (and skips a Nil tenant, same as
// dispatchEvent).
func (h *Handler) dispatchEventTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, eventType, resourceName string, payload map[string]any) error {
	if h.events == nil || tenantID == uuid.Nil() {
		return nil
	}
	return h.events.RecordTx(ctx, tx, tenantID.String(), worker.Event{
		Type:         eventType,
		At:           time.Now().UTC(),
		TenantID:     tenantID.String(),
		ResourceName: resourceName,
		ActorSubject: subject(ctx),
		Payload:      payload,
	})
}

// authorize gates a quota RPC against Cedar. The Resource carries the
// tenant or bucket coordinates so policies can pin "tenant.admin manages
// own quota" via resource.tenant_id == principal.tenant_id.
// On success it returns a context scoped to q.TenantID, so the RLS pool
// reads the quota rows the caller was just authorised for rather than the
// caller's own. A bucket-scoped quota carries no tenant; the context is
// returned unchanged there and the caller's own scope applies.
func (h *Handler) authorize(ctx context.Context, action cedar.Action, q admindomain.Quota) (context.Context, error) {
	if _, err := apiutil.Authorize(ctx, h.policy, action, &cedar.Resource{
		TenantID:   q.TenantID,
		BackendID:  q.BackendID,
		BucketName: q.BucketName,
	}); err != nil {
		return ctx, err
	}
	return auth.WithActingTenant(ctx, q.TenantID), nil
}

// crossTenant refuses a caller reading tenantID's quota from another tenant,
// unless it is a platform admin.
func crossTenant(ctx context.Context, tenantID uuid.UUID) error {
	caller, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return err
	}
	if !apiutil.HasRole(ctx, apiutil.RolePlatformAdmin) && tenantID != caller {
		return connect.NewError(connect.CodePermissionDenied, "cross-tenant denied")
	}
	return nil
}

func (h *Handler) GetTenantQuota(ctx context.Context, tenantID uuid.UUID) (*admindomain.Quota, error) {
	scope := admindomain.Quota{TenantID: tenantID}
	if err := crossTenant(ctx, tenantID); err != nil {
		return nil, h.answer(ctx, opGetTenantQuota, err, scopeFields(scope)...)
	}
	ctx, err := h.authorize(ctx, cedar.ActionReadQuota, scope)
	if err != nil {
		return nil, h.answer(ctx, opGetTenantQuota, err, scopeFields(scope)...)
	}
	q, err := h.repo.GetTenant(ctx, tenantID)
	if err != nil {
		return nil, h.answer(ctx, opGetTenantQuota, err, scopeFields(scope)...)
	}
	return &q, nil
}

func (h *Handler) GetBucketQuota(ctx context.Context, backendID, bucketName string) (*admindomain.Quota, error) {
	scope := admindomain.Quota{BackendID: backendID, BucketName: bucketName}
	if err := apiutil.RequireAnyRole(ctx,
		apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin, apiutil.RoleTenantAdmin); err != nil {
		return nil, h.answer(ctx, opGetBucketQuota, err, scopeFields(scope)...)
	}
	ctx, err := h.authorize(ctx, cedar.ActionReadQuota, scope)
	if err != nil {
		return nil, h.answer(ctx, opGetBucketQuota, err, scopeFields(scope)...)
	}
	q, err := h.repo.GetBucket(ctx, backendID, bucketName)
	if err != nil {
		return nil, h.answer(ctx, opGetBucketQuota, err, scopeFields(scope)...)
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
		return nil, h.answer(ctx, opSetQuota, err, scopeFields(q)...)
	}
	ctx, err := h.authorize(ctx, cedar.ActionManageQuota, q)
	if err != nil {
		return nil, h.answer(ctx, opSetQuota, err, scopeFields(q)...)
	}
	tenantScope, err := scopeOf(q)
	if err != nil {
		return nil, h.answer(ctx, opSetQuota, err, scopeFields(q)...)
	}
	if q, err = h.withMaskedCaps(ctx, q, tenantScope, mask); err != nil {
		return nil, h.answer(ctx, opSetQuota, err, scopeFields(q)...)
	}
	var got admindomain.Quota
	if tenantScope {
		got, err = h.setTenantQuota(ctx, q)
	} else {
		got, err = h.setBucketQuota(ctx, q)
	}
	if err != nil {
		return nil, h.answer(ctx, opSetQuota, err, scopeFields(q)...)
	}
	h.log.Info(msgQuotaSet, append(scopeFields(got), actor(ctx), zap.Int64("resource_version", got.ResourceVersion))...)
	return &got, nil
}

// scopeOf is whether q is a tenant's quota rather than a bucket's: exactly
// one of the two is named, or the request is InvalidArgument.
func scopeOf(q admindomain.Quota) (tenantScope bool, err error) {
	tenantScope = q.TenantID != uuid.Nil()
	bucketScope := q.BackendID != "" && q.BucketName != ""
	if tenantScope == bucketScope {
		return false, connect.NewError(connect.CodeInvalidArgument,
			"exactly one of tenant_id or (backend_id, bucket_name) must be set")
	}
	return tenantScope, nil
}

// withMaskedCaps is q with the caps mask names and the stored row's others.
// The upsert writes all four caps, so the caps the mask does not name are
// taken from the stored row; with none stored they are no cap. A row that
// changes between this read and the write fails the version guard.
func (h *Handler) withMaskedCaps(ctx context.Context, q admindomain.Quota, tenantScope bool, mask []string) (admindomain.Quota, error) {
	var current admindomain.Quota
	var err error
	if tenantScope {
		current, err = h.repo.GetTenant(ctx, q.TenantID)
	} else {
		current, err = h.repo.GetBucket(ctx, q.BackendID, q.BucketName)
	}
	if err != nil && !errors.Is(err, admindomain.ErrNotFound) {
		return q, err
	}
	caps, err := applyCapMask(current, q, mask)
	if err != nil {
		return q, err
	}
	q.MaxTotalBytes, q.MaxObjectCount = caps.MaxTotalBytes, caps.MaxObjectCount
	q.MaxBytesPerDay, q.MaxObjectsPerDay = caps.MaxBytesPerDay, caps.MaxObjectsPerDay
	return q, nil
}

// capsPayload is the event payload naming q's caps, beside scope's keys.
func capsPayload(q admindomain.Quota, scope map[string]any) map[string]any {
	scope[CapMaxTotalBytes] = q.MaxTotalBytes
	scope[CapMaxObjectCount] = q.MaxObjectCount
	scope[CapMaxBytesPerDay] = q.MaxBytesPerDay
	scope[CapMaxObjectsPerDay] = q.MaxObjectsPerDay
	return scope
}

// setTenantQuota writes a tenant's caps and paladin.quota.set in one tx
// (ADR-0003). The event payload is the caps just written (== q), so no in-tx
// read-back is needed; the full row is fetched post-commit for the response.
func (h *Handler) setTenantQuota(ctx context.Context, q admindomain.Quota) (admindomain.Quota, error) {
	if err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if e := h.repo.UpsertTenantTx(ctx, tx, q); e != nil {
			return e
		}
		return h.dispatchEventTx(ctx, tx, q.TenantID, EventQuotaSet,
			fmt.Sprintf(tenantQuotaResource, q.TenantID),
			capsPayload(q, map[string]any{payloadTenantID: q.TenantID.String(), payloadScope: scopeTenant}))
	}); err != nil {
		return admindomain.Quota{}, err
	}
	got, err := h.repo.GetTenant(ctx, q.TenantID)
	if err != nil {
		// The caps are written; only the answer is lost, so it is internal
		// whatever the read said, a row not found included.
		return admindomain.Quota{}, connect.NewError(connect.CodeInternal,
			"quota written but not read back: "+err.Error()).WithCause(err)
	}
	return got, nil
}

// setBucketQuota writes a bucket's caps. The fan-out target is the bucket's
// owner, which the read-back joins from `buckets` — so the upsert, the
// read-back and the event all run on one tx (ADR-0003), and the read-back is
// the response.
func (h *Handler) setBucketQuota(ctx context.Context, q admindomain.Quota) (admindomain.Quota, error) {
	var got admindomain.Quota
	err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if e := h.repo.UpsertBucketTx(ctx, tx, q); e != nil {
			return e
		}
		var e error
		if got, e = h.repo.GetBucketTx(ctx, tx, q.BackendID, q.BucketName); e != nil {
			return e
		}
		// A shared bucket has no owner, so its quota has nobody to tell.
		return h.dispatchEventTx(ctx, tx, got.OwnerTenantID, EventQuotaSet,
			fmt.Sprintf(bucketQuotaResource, got.OwnerTenantID, got.BackendID, got.BucketName),
			capsPayload(got, map[string]any{
				payloadTenantID:   got.OwnerTenantID.String(),
				payloadScope:      scopeBucket,
				payloadBackendID:  got.BackendID,
				payloadBucketName: got.BucketName,
			}))
	})
	return got, err
}

func (h *Handler) ResetUsage(ctx context.Context, quotaID uuid.UUID) error {
	named := admindomain.Quota{QuotaID: quotaID}
	if err := apiutil.RequireRole(ctx, apiutil.RolePlatformAdmin); err != nil {
		return h.answer(ctx, opResetUsage, err, scopeFields(named)...)
	}
	// The request names only the quota id, so load the row first: Cedar then
	// sees its tenant or bucket, and the reset runs as that tenant. The read
	// is cross-tenant because the quota usually belongs to someone other
	// than the platform admin calling.
	q, err := h.repo.GetByID(auth.WithCrossTenantRead(ctx), quotaID)
	if err != nil {
		return h.answer(ctx, opResetUsage, err, scopeFields(named)...)
	}
	ctx, err = h.authorize(ctx, cedar.ActionResetQuotaUsage, q)
	if err != nil {
		return h.answer(ctx, opResetUsage, err, scopeFields(q)...)
	}
	now := time.Now().UTC()
	if q.TenantID == uuid.Nil() {
		// A bucket quota is platform configuration outside RLS
		// (044_bucket_quotas.sql); there is no tenant to act as.
		err = h.repo.ResetBucketDaily(ctx, quotaID, now)
	} else {
		err = h.repo.ResetDaily(ctx, quotaID, now)
	}
	if err != nil {
		return h.answer(ctx, opResetUsage, err, scopeFields(q)...)
	}
	h.log.Info(msgQuotaUsageReset, append(scopeFields(q), actor(ctx))...)
	return nil
}
