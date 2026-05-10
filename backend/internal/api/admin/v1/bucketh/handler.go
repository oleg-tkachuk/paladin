// Package bucketh implements the admin BucketService — physical bucket
// CRUD + per-bucket policy / lifecycle / lock / versioning / replication.
package bucketh

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	celpkg "github.com/oleg-tkachuk/paladin/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/internal/worker"
)

// EventProducer mirrors the seam used by tenanth — narrow interface
// so handler tests can stub the dispatcher without spinning up the
// outbox + Postgres. *worker.Dispatcher implements it.
type EventProducer interface {
	Dispatch(ctx context.Context, tenantID string, evt worker.Event) (int, error)
}

// Cedar action names — must match policies/schema.cedarschema.
const (
	actionManageBucket = "ManageBucket"
	actionReadBucket   = "ReadBucket"
)

// Provisioner provisions/de-provisions the underlying S3 bucket. nil-safe:
// when not wired, CreateBucket(provision_on_backend=true) returns Unavailable.
type Provisioner interface {
	CreateBucket(ctx context.Context, backendID, bucketName, region string) error
	DeleteBucket(ctx context.Context, backendID, bucketName string) error
}

type Handler struct {
	repo        admindomain.BucketRepository
	provisioner Provisioner
	policy      cedar.Authorizer

	events EventProducer
	log    *zap.Logger
}

func NewHandler(r admindomain.BucketRepository, p Provisioner, policyEngine cedar.Authorizer) *Handler {
	if policyEngine == nil {
		panic("bucketh: policy authorizer is required")
	}
	return &Handler{repo: r, provisioner: p, policy: policyEngine, log: zap.NewNop()}
}

// SetEventProducer attaches the optional outbox producer. nil is
// silent — same contract as tenanth.SetEventProducer. Handlers
// that don't pump events into the bus (unit tests, deployments
// where ingest/dispatcher is intentionally off) leave it unset.
func (h *Handler) SetEventProducer(p EventProducer) { h.events = p }

// SetLogger attaches a non-nop logger so fan-out failures surface
// in structured form. dispatchEvent is best-effort: a failure to
// queue an outbox row must not flip the RPC reply, so the only
// place these errors can land is the log.
func (h *Handler) SetLogger(l *zap.Logger) {
	if l != nil {
		h.log = l
	}
}

// dispatchEvent fans out a bucket lifecycle event into the outbox.
// Best-effort: the lifecycle write already committed, so any
// outbox-insert failure logs and returns rather than failing the
// whole RPC (which would mislead the caller into retrying).
func (h *Handler) dispatchEvent(ctx context.Context, tenantID uuid.UUID, eventType, resourceName string, payload map[string]any) {
	if h.events == nil {
		return
	}
	actor := ""
	if p, err := auth.PrincipalFromContext(ctx); err == nil {
		actor = p.Subject
	}
	queued, err := h.events.Dispatch(ctx, tenantID.String(), worker.Event{
		Type:         eventType,
		At:           time.Now().UTC(),
		TenantID:     tenantID.String(),
		ResourceName: resourceName,
		ActorSubject: actor,
		Payload:      payload,
	})
	if err != nil {
		h.log.Warn("bucket event fan-out failed",
			zap.String("event_type", eventType),
			zap.String("tenant_id", tenantID.String()),
			zap.String("resource", resourceName),
			zap.Error(err),
		)
		return
	}
	h.log.Debug("bucket event queued",
		zap.String("event_type", eventType),
		zap.String("tenant_id", tenantID.String()),
		zap.Int("subscriptions_matched", queued),
	)
}

// bucketResourceName is the canonical resource string subscribers
// route on. Mirrors the path `tenants/{tenant_id}/buckets/{backend}/{name}`
// the admin RPCs use elsewhere.
func bucketResourceName(tenantID uuid.UUID, backendID, bucketName string) string {
	return fmt.Sprintf("tenants/%s/buckets/%s/%s", tenantID, backendID, bucketName)
}

// authorize evaluates Cedar against the Bucket resource. The Bucket entity
// is anchored under StorageBackend, so the engine sees both the backend
// and the bucket attributes.
func (h *Handler) authorize(ctx context.Context, action, backendID, bucketName string, ownerTenantID uuid.UUID) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	decision, err := h.policy.IsAuthorized(ctx,
		&cedar.Principal{Subject: p.Subject, TenantID: p.TenantID, TenantSlug: p.TenantSlug, Roles: p.Roles, Scopes: apiutil.ScopeStrings(p.Scopes)},
		action,
		&cedar.Resource{
			BackendID:     backendID,
			BucketName:    bucketName,
			OwnerTenantID: ownerTenantID,
		},
		cedar.RequestContext{Now: time.Now()},
	)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied, errors.New("denied by policy"))
	}
	return nil
}

// ─── Create ─────────────────────────────────────────────────────────────────

type CreateBucketInput struct {
	Bucket             admindomain.Bucket
	ProvisionOnBackend bool
}

func (h *Handler) CreateBucket(ctx context.Context, in CreateBucketInput) (*admindomain.Bucket, error) {
	if err := apiutil.RequireAnyRole(ctx, apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin); err != nil {
		return nil, err
	}
	if in.Bucket.BackendID == "" || in.Bucket.BucketName == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("backend_id and bucket_name required"))
	}
	if err := h.authorize(ctx, actionManageBucket, in.Bucket.BackendID, in.Bucket.BucketName, in.Bucket.OwnerTenantID); err != nil {
		return nil, err
	}
	// Outbox model: the DB row is the source of truth. When the caller
	// asked us to create the physical bucket too, we mark the row
	// 'pending' and let the reconciler worker drive the backend
	// CreateBucket — that way a DB-write failure can never leave an
	// orphan in S3, and a backend-side failure is observable on the row
	// instead of being lost to a 5xx that never made it to the client.
	if in.ProvisionOnBackend {
		if h.provisioner == nil {
			return nil, connect.NewError(connect.CodeUnavailable,
				errors.New("backend provisioning not wired"))
		}
		in.Bucket.ProvisionState = admindomain.BucketProvisionStatePending
	} else {
		// Operator opted out of backend provisioning (e.g. binding a
		// pre-existing bucket). Row is immediately authoritative.
		in.Bucket.ProvisionState = admindomain.BucketProvisionStateReady
	}
	if err := h.repo.Create(ctx, in.Bucket); err != nil {
		// Translate the typed ErrConflict the repo raises for FK /
		// unique violations into FailedPrecondition so clients (UI,
		// SDKs) see a readable message instead of "internal: SQLSTATE
		// 23503". The repo's wrapped error already names the missing
		// backend or duplicate bucket.
		if errors.Is(err, admindomain.ErrConflict) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	got, err := h.repo.Get(ctx, in.Bucket.BackendID, in.Bucket.BucketName)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	h.dispatchEvent(ctx, got.OwnerTenantID, "paladin.bucket.created",
		bucketResourceName(got.OwnerTenantID, got.BackendID, got.BucketName),
		map[string]any{
			"tenant_id":       got.OwnerTenantID.String(),
			"backend_id":      got.BackendID,
			"bucket_name":     got.BucketName,
			"region":          got.Region,
			"provision_state": string(got.ProvisionState),
		})
	return &got, nil
}

// ─── Read ───────────────────────────────────────────────────────────────────

func (h *Handler) GetBucket(ctx context.Context, backendID, bucketName string) (*admindomain.Bucket, error) {
	if err := apiutil.RequireAnyRole(ctx,
		apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin, apiutil.RoleTenantAdmin); err != nil {
		return nil, err
	}
	b, err := h.repo.Get(ctx, backendID, bucketName)
	if err != nil {
		return nil, mapNotFound(err)
	}
	// Cedar runs after the read so the engine sees authoritative ownership.
	if err := h.authorize(ctx, actionReadBucket, b.BackendID, b.BucketName, b.OwnerTenantID); err != nil {
		return nil, err
	}
	if !apiutil.HasRole(ctx, apiutil.RolePlatformAdmin) && !apiutil.HasRole(ctx, apiutil.RoleBucketAdmin) {
		// Non-bucket-admins (i.e. tenant admins) get a redacted view: no
		// cedar_policy text, no replication target details. Avoids leaking
		// other tenants' policy graph.
		b.CedarPolicy = ""
		b.Replication.DestinationBucket = ""
	}
	return &b, nil
}

func (h *Handler) ListBuckets(ctx context.Context, args admindomain.ListBucketsArgs) ([]admindomain.Bucket, string, error) {
	if err := apiutil.RequireAnyRole(ctx,
		apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin, apiutil.RoleTenantAdmin); err != nil {
		return nil, "", err
	}
	if err := h.authorize(ctx, cedar.ActionReadBucket, args.BackendID, "", uuid.Nil); err != nil {
		return nil, "", err
	}
	return h.repo.List(ctx, args)
}

func (h *Handler) ListAccessibleBuckets(ctx context.Context, tenantID uuid.UUID, pageSize int32, afterBackend, afterName string) ([]admindomain.Bucket, string, error) {
	caller, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, "", err
	}
	if !apiutil.HasRole(ctx, apiutil.RolePlatformAdmin) && tenantID != caller {
		return nil, "", connect.NewError(connect.CodePermissionDenied,
			errors.New("cannot enumerate accessible buckets for another tenant"))
	}
	if err := h.authorize(ctx, cedar.ActionReadBucket, "", "", tenantID); err != nil {
		return nil, "", err
	}
	return h.repo.ListAccessible(ctx, tenantID, pageSize, afterBackend, afterName)
}

// ─── Update ─────────────────────────────────────────────────────────────────

type UpdateBucketInput struct {
	Bucket          admindomain.Bucket
	ExpectedVersion int64
	UpdateMask      []string
}

func (h *Handler) UpdateBucket(ctx context.Context, in UpdateBucketInput) (*admindomain.Bucket, error) {
	if err := apiutil.RequireAnyRole(ctx, apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, actionManageBucket, in.Bucket.BackendID, in.Bucket.BucketName, in.Bucket.OwnerTenantID); err != nil {
		return nil, err
	}
	if err := h.repo.UpdateBasic(ctx, in.Bucket, in.ExpectedVersion, in.UpdateMask); err != nil {
		return nil, mapVersion(err)
	}
	got, err := h.repo.Get(ctx, in.Bucket.BackendID, in.Bucket.BucketName)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	h.dispatchEvent(ctx, got.OwnerTenantID, "paladin.bucket.updated",
		bucketResourceName(got.OwnerTenantID, got.BackendID, got.BucketName),
		map[string]any{
			"tenant_id":        got.OwnerTenantID.String(),
			"backend_id":       got.BackendID,
			"bucket_name":      got.BucketName,
			"resource_version": got.ResourceVersion,
			// We don't ship UpdateMask: the field-mask is a connect-shim
			// concern and downstream consumers can diff against their
			// cached snapshot if they care which scalar moved.
		})
	return &got, nil
}

// ─── Single-purpose setters ─────────────────────────────────────────────────

func (h *Handler) SetPolicy(ctx context.Context, backendID, bucketName, policy string, expectedVersion int64) (*admindomain.Bucket, error) {
	if err := apiutil.RequireAnyRole(ctx, apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, cedar.ActionConfigureBucketPolicy, backendID, bucketName, uuid.Nil); err != nil {
		return nil, err
	}
	if err := h.repo.SetPolicy(ctx, backendID, bucketName, policy, expectedVersion); err != nil {
		return nil, mapVersion(err)
	}
	got, _ := h.repo.Get(ctx, backendID, bucketName)
	return &got, nil
}

func (h *Handler) SetLifecycleRules(ctx context.Context, backendID, bucketName string, rules []admindomain.LifecycleRule, expectedVersion int64) (*admindomain.Bucket, error) {
	if err := apiutil.RequireAnyRole(ctx, apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, cedar.ActionConfigureLifecycle, backendID, bucketName, uuid.Nil); err != nil {
		return nil, err
	}
	// Validate every rule's CEL match upfront so a typo is rejected at
	// write time, not silently swallowed by the lifecycle worker hours
	// later. Empty match = "always match" (worker contract).
	for i, r := range rules {
		if err := celpkg.Validate(celpkg.ObjectSchema, r.Match); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("rule[%d] (id=%q): %w", i, r.ID, err))
		}
	}
	if err := h.repo.SetLifecycle(ctx, backendID, bucketName, rules, expectedVersion); err != nil {
		return nil, mapVersion(err)
	}
	got, _ := h.repo.Get(ctx, backendID, bucketName)
	return &got, nil
}

func (h *Handler) SetObjectLock(ctx context.Context, backendID, bucketName string, lock admindomain.ObjectLockConfig, expectedVersion int64) (*admindomain.Bucket, error) {
	if err := apiutil.RequireAnyRole(ctx, apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, cedar.ActionConfigureLock, backendID, bucketName, uuid.Nil); err != nil {
		return nil, err
	}
	if err := h.repo.SetObjectLock(ctx, backendID, bucketName, lock, expectedVersion); err != nil {
		return nil, mapVersion(err)
	}
	got, _ := h.repo.Get(ctx, backendID, bucketName)
	return &got, nil
}

func (h *Handler) SetVersioning(ctx context.Context, backendID, bucketName string, v admindomain.BucketVersioning, expectedVersion int64) (*admindomain.Bucket, error) {
	if err := apiutil.RequireAnyRole(ctx, apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, cedar.ActionConfigureVersioning, backendID, bucketName, uuid.Nil); err != nil {
		return nil, err
	}
	if err := h.repo.SetVersioning(ctx, backendID, bucketName, v, expectedVersion); err != nil {
		return nil, mapVersion(err)
	}
	got, _ := h.repo.Get(ctx, backendID, bucketName)
	return &got, nil
}

func (h *Handler) SetReplication(ctx context.Context, backendID, bucketName string, r admindomain.BucketReplication, expectedVersion int64) (*admindomain.Bucket, error) {
	if err := apiutil.RequireAnyRole(ctx, apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, cedar.ActionConfigureReplication, backendID, bucketName, uuid.Nil); err != nil {
		return nil, err
	}
	if err := h.repo.SetReplication(ctx, backendID, bucketName, r, expectedVersion); err != nil {
		return nil, mapVersion(err)
	}
	got, _ := h.repo.Get(ctx, backendID, bucketName)
	return &got, nil
}

// ─── Delete ─────────────────────────────────────────────────────────────────

type DeleteBucketInput struct {
	BackendID       string
	BucketName      string
	ExpectedVersion int64
	DeleteOnBackend bool
}

func (h *Handler) DeleteBucket(ctx context.Context, in DeleteBucketInput) error {
	if err := apiutil.RequireAnyRole(ctx, apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin); err != nil {
		return err
	}
	if err := h.authorize(ctx, actionManageBucket, in.BackendID, in.BucketName, uuid.Nil); err != nil {
		return err
	}
	// Outbox model for deletes (mirrors CreateBucket): the row stays in
	// place flipped to 'deleting', and the bucket-reconciler worker
	// drives the physical s3.DeleteBucket then the actual row removal.
	// That avoids the same orphan window the create path used to have.
	//
	// When the operator opted out of backend deletion, we still mark
	// 'deleting' rather than physical-delete inline — the worker has
	// the same code path that handles the "no provisioner" case (it
	// just deletes the row without calling S3) and concentrating the
	// terminal logic there keeps the handler simple.
	if in.DeleteOnBackend && h.provisioner == nil {
		return connect.NewError(connect.CodeUnavailable, errors.New("backend provisioning not wired"))
	}
	if !in.DeleteOnBackend {
		// Operator says S3 cleanup is their problem — physically delete
		// the row right away. Faster and avoids parking dev/test rows
		// in a 'deleting' loop the worker can never resolve (no S3 →
		// always transient).
		// Snapshot the row before deletion so we can attach owner
		// tenant_id to the event payload — repo.Delete leaves us
		// without that context.
		preDelete, getErr := h.repo.Get(ctx, in.BackendID, in.BucketName)
		if err := h.repo.Delete(ctx, in.BackendID, in.BucketName, in.ExpectedVersion); err != nil {
			return mapVersion(err)
		}
		if getErr == nil {
			h.dispatchEvent(ctx, preDelete.OwnerTenantID, "paladin.bucket.deleted",
				bucketResourceName(preDelete.OwnerTenantID, preDelete.BackendID, preDelete.BucketName),
				map[string]any{
					"tenant_id":   preDelete.OwnerTenantID.String(),
					"backend_id":  preDelete.BackendID,
					"bucket_name": preDelete.BucketName,
					"mode":        "immediate",
				})
		}
		return nil
	}
	preMark, getErr := h.repo.Get(ctx, in.BackendID, in.BucketName)
	if err := h.repo.MarkDeleting(ctx, in.BackendID, in.BucketName, in.ExpectedVersion); err != nil {
		return mapVersion(err)
	}
	// Outbox-mode delete fires a `.deleting` (not `.deleted`) event
	// because the bucket isn't actually gone yet — the reconciler
	// worker drives the physical S3 DeleteBucket and only THEN does
	// the row disappear. Subscribers that want the terminal state
	// can listen for `.deleted` once the worker emits it (BACKLOG —
	// reconciler doesn't yet emit per-row events on completion).
	if getErr == nil {
		h.dispatchEvent(ctx, preMark.OwnerTenantID, "paladin.bucket.deleting",
			bucketResourceName(preMark.OwnerTenantID, preMark.BackendID, preMark.BucketName),
			map[string]any{
				"tenant_id":   preMark.OwnerTenantID.String(),
				"backend_id":  preMark.BackendID,
				"bucket_name": preMark.BucketName,
				"mode":        "outbox",
			})
	}
	return nil
}

// ─── helpers ────────────────────────────────────────────────────────────────

func mapNotFound(err error) error {
	if errors.Is(err, admindomain.ErrNotFound) {
		return connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewError(connect.CodeInternal, err)
}

func mapVersion(err error) error {
	if errors.Is(err, admindomain.ErrVersionMismatch) {
		return connect.NewError(connect.CodeAborted, err)
	}
	if errors.Is(err, admindomain.ErrNotFound) {
		return connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewError(connect.CodeInternal, err)
}

// silence unused
var _ = auth.AudienceAdmin
