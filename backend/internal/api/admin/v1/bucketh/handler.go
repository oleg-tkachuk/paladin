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
	"github.com/jackc/pgx/v5"
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
	// DispatchTx fans the event out on the caller's tx so the outbox rows
	// commit atomically with the bucket mutation (ADR-0003).
	DispatchTx(ctx context.Context, tx pgx.Tx, tenantID string, evt worker.Event) (int, error)
}

// Repository is the admindomain BucketRepository plus the ADR-0003 tx seam
// (RunInTx + *Tx mutations + GetTx for the in-tx owner read). Kept local so
// admindomain stays pgx-free; the concrete adapter satisfies both.
type Repository interface {
	admindomain.BucketRepository
	RunInTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error
	CreateTx(ctx context.Context, tx pgx.Tx, b admindomain.Bucket) error
	UpdateBasicTx(ctx context.Context, tx pgx.Tx, b admindomain.Bucket, expectedVersion int64, mask []string) error
	GetTx(ctx context.Context, tx pgx.Tx, backendID, bucketName string) (admindomain.Bucket, error)
	DeleteTx(ctx context.Context, tx pgx.Tx, backendID, bucketName string, expectedVersion int64) error
	MarkDeletingTx(ctx context.Context, tx pgx.Tx, backendID, bucketName string, expectedVersion int64) error
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
	repo        Repository
	provisioner Provisioner
	policy      cedar.Authorizer

	events EventProducer
	log    *zap.Logger
}

func NewHandler(r Repository, p Provisioner, policyEngine cedar.Authorizer) *Handler {
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

// dispatchEventTx fans the event out on the caller's tx so the outbox rows
// commit atomically with the bucket mutation (ADR-0003). Returns the error
// so the caller rolls back; nil-safe.
func (h *Handler) dispatchEventTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, eventType, resourceName string, payload map[string]any) error {
	if h.events == nil {
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
	// Refuse binding a bucket to a disabled backend (feature 002). The
	// object-path resolver gate covers reads/writes; this is the one
	// admin-plane op that does not go through that resolver, so it gets
	// its own pre-check before the row is persisted.
	switch enabled, err := h.repo.BackendEnabled(ctx, in.Bucket.BackendID); {
	case errors.Is(err, admindomain.ErrNotFound):
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("backend %q does not exist", in.Bucket.BackendID))
	case err != nil:
		return nil, connect.NewError(connect.CodeInternal, err)
	case !enabled:
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("backend %q is disabled", in.Bucket.BackendID))
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
	// Create + paladin.bucket.created in one tx (ADR-0003). The event needs the
	// stored row (owner tenant_id, provision_state), so GetTx reads it back
	// on the same tx; `got` is reused for the response.
	var got admindomain.Bucket
	if err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if e := h.repo.CreateTx(ctx, tx, in.Bucket); e != nil {
			return e
		}
		var e error
		got, e = h.repo.GetTx(ctx, tx, in.Bucket.BackendID, in.Bucket.BucketName)
		if e != nil {
			return e
		}
		return h.dispatchEventTx(ctx, tx, got.OwnerTenantID, "paladin.bucket.created",
			bucketResourceName(got.OwnerTenantID, got.BackendID, got.BucketName),
			map[string]any{
				"tenant_id":       got.OwnerTenantID.String(),
				"backend_id":      got.BackendID,
				"bucket_name":     got.BucketName,
				"region":          got.Region,
				"provision_state": string(got.ProvisionState),
			})
	}); err != nil {
		// ErrConflict (FK / unique violations the repo types) → FailedPrecondition
		// via the central registry (ADR-0002), so clients see a readable code.
		return nil, apiutil.MapError(err)
	}
	return &got, nil
}

// EnsureBucket idempotently ensures a bucket row exists for a caller that a
// higher layer has ALREADY authorized. Unlike CreateBucket there is NO role
// gate and NO Cedar check here — the DATA-plane StorageBootstrapService gates
// on the EnsureTenantStorage Cedar action before calling this, and that action
// IS the authorization. Do NOT mount this behind a surface that has not
// already authorized the caller.
//
// It reuses CreateBucket's exact repo + provision-state + outbox/event path,
// so a shared bucket self-provisioned this way is indistinguishable from one
// an admin created. Returns created=false when the bucket already existed (a
// no-op), created=true when a new row was written.
func (h *Handler) EnsureBucket(ctx context.Context, in CreateBucketInput) (*admindomain.Bucket, bool, error) {
	if _, err := auth.PrincipalFromContext(ctx); err != nil {
		return nil, false, connect.NewError(connect.CodeUnauthenticated, err)
	}
	if in.Bucket.BackendID == "" || in.Bucket.BucketName == "" {
		return nil, false, connect.NewError(connect.CodeInvalidArgument,
			errors.New("backend_id and bucket_name required"))
	}
	// Fast idempotent path: an existing row is a success no-op. This also keeps
	// the common "already provisioned" startup call off the write path.
	switch existing, err := h.repo.Get(ctx, in.Bucket.BackendID, in.Bucket.BucketName); {
	case err == nil:
		return &existing, false, nil
	case errors.Is(err, admindomain.ErrNotFound):
		// fall through to create
	default:
		return nil, false, connect.NewError(connect.CodeInternal, err)
	}
	// Refuse binding to an unknown/disabled backend — same guard CreateBucket
	// uses (feature 002); a tenant must not create backends.
	switch enabled, err := h.repo.BackendEnabled(ctx, in.Bucket.BackendID); {
	case errors.Is(err, admindomain.ErrNotFound):
		return nil, false, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("backend %q does not exist", in.Bucket.BackendID))
	case err != nil:
		return nil, false, connect.NewError(connect.CodeInternal, err)
	case !enabled:
		return nil, false, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("backend %q is disabled", in.Bucket.BackendID))
	}
	if in.ProvisionOnBackend {
		if h.provisioner == nil {
			return nil, false, connect.NewError(connect.CodeUnavailable,
				errors.New("backend provisioning not wired"))
		}
		in.Bucket.ProvisionState = admindomain.BucketProvisionStatePending
	} else {
		in.Bucket.ProvisionState = admindomain.BucketProvisionStateReady
	}
	var got admindomain.Bucket
	if err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if e := h.repo.CreateTx(ctx, tx, in.Bucket); e != nil {
			return e
		}
		var e error
		got, e = h.repo.GetTx(ctx, tx, in.Bucket.BackendID, in.Bucket.BucketName)
		if e != nil {
			return e
		}
		return h.dispatchEventTx(ctx, tx, got.OwnerTenantID, "paladin.bucket.created",
			bucketResourceName(got.OwnerTenantID, got.BackendID, got.BucketName),
			map[string]any{
				"tenant_id":       got.OwnerTenantID.String(),
				"backend_id":      got.BackendID,
				"bucket_name":     got.BucketName,
				"region":          got.Region,
				"provision_state": string(got.ProvisionState),
			})
	}); err != nil {
		// Lost a race to a concurrent create (unique violation → ErrConflict):
		// the bucket now exists, so honour idempotency and report it existing.
		if errors.Is(err, admindomain.ErrConflict) {
			if existing, gerr := h.repo.Get(ctx, in.Bucket.BackendID, in.Bucket.BucketName); gerr == nil {
				return &existing, false, nil
			}
		}
		return nil, false, apiutil.MapError(err)
	}
	return &got, true, nil
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
	// Update + paladin.bucket.updated in one tx (ADR-0003). GetTx reads the
	// post-update row back on the same tx (resource_version, owner).
	var got admindomain.Bucket
	if err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if e := h.repo.UpdateBasicTx(ctx, tx, in.Bucket, in.ExpectedVersion, in.UpdateMask); e != nil {
			return e
		}
		var e error
		got, e = h.repo.GetTx(ctx, tx, in.Bucket.BackendID, in.Bucket.BucketName)
		if e != nil {
			return e
		}
		return h.dispatchEventTx(ctx, tx, got.OwnerTenantID, "paladin.bucket.updated",
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
	}); err != nil {
		return nil, mapVersion(err)
	}
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
		// Snapshot before delete (pre-tx read) for the event's owner
		// tenant_id; the delete + paladin.bucket.deleted commit in one tx
		// (ADR-0003). When the pre-read failed we skip the event but still
		// run the OCC-guarded delete.
		preDelete, getErr := h.repo.Get(ctx, in.BackendID, in.BucketName)
		if err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			if e := h.repo.DeleteTx(ctx, tx, in.BackendID, in.BucketName, in.ExpectedVersion); e != nil {
				return e
			}
			if getErr != nil {
				return nil
			}
			return h.dispatchEventTx(ctx, tx, preDelete.OwnerTenantID, "paladin.bucket.deleted",
				bucketResourceName(preDelete.OwnerTenantID, preDelete.BackendID, preDelete.BucketName),
				map[string]any{
					"tenant_id":   preDelete.OwnerTenantID.String(),
					"backend_id":  preDelete.BackendID,
					"bucket_name": preDelete.BucketName,
					"mode":        "immediate",
				})
		}); err != nil {
			return mapVersion(err)
		}
		return nil
	}
	// Outbox-mode delete fires a `.deleting` (not `.deleted`) event because
	// the bucket isn't actually gone yet — the reconciler worker drives the
	// physical S3 DeleteBucket and only THEN does the row disappear.
	// Subscribers that want the terminal state can listen for `.deleted`
	// once the worker emits it (BACKLOG — reconciler doesn't yet emit
	// per-row events on completion). Mark + event commit in one tx (ADR-0003).
	preMark, getErr := h.repo.Get(ctx, in.BackendID, in.BucketName)
	if err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if e := h.repo.MarkDeletingTx(ctx, tx, in.BackendID, in.BucketName, in.ExpectedVersion); e != nil {
			return e
		}
		if getErr != nil {
			return nil
		}
		return h.dispatchEventTx(ctx, tx, preMark.OwnerTenantID, "paladin.bucket.deleting",
			bucketResourceName(preMark.OwnerTenantID, preMark.BackendID, preMark.BucketName),
			map[string]any{
				"tenant_id":   preMark.OwnerTenantID.String(),
				"backend_id":  preMark.BackendID,
				"bucket_name": preMark.BucketName,
				"mode":        "outbox",
			})
	}); err != nil {
		return mapVersion(err)
	}
	return nil
}

// ─── helpers ────────────────────────────────────────────────────────────────

func mapNotFound(err error) error { return apiutil.MapError(err) }

func mapVersion(err error) error { return apiutil.MapError(err) }

// silence unused
var _ = auth.AudienceAdmin
