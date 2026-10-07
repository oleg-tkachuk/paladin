// Package bucketh implements the admin BucketService — physical bucket
// CRUD + per-bucket policy / lifecycle / lock / versioning / replication.
package bucketh

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	celpkg "github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/publicread"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
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

// Provisioner provisions/de-provisions the underlying S3 bucket. nil-safe:
// when not wired, CreateBucket(provision_on_backend=true) returns Unavailable.
type Provisioner interface {
	CreateBucket(ctx context.Context, backendID, bucketName, region string) error
	DeleteBucket(ctx context.Context, backendID, bucketName string) error
	// BucketExists reports whether the backend holds the bucket, whoever owns it.
	BucketExists(ctx context.Context, backendID, bucketName string) (bool, error)
}

type Handler struct {
	repo        Repository
	provisioner Provisioner
	// backends reads a backend's probed features (ADR-0026); see SetBackends.
	backends BackendReader
	policy   cedar.Authorizer
	// cel compiles and caches List filters (program cache only).
	cel *celpkg.Evaluator

	events EventProducer
	log    *zap.Logger
	// configured holds the backend ids declared in storage.backends. The
	// data plane and the worker build storage clients from that list only, so
	// a bucket on any other backend could be neither provisioned nor reached.
	// nil (unset) refuses nothing; wiring always sets it.
	configured map[string]bool
	// reserved are the buckets Paladin uses itself; see SetReservedBuckets.
	reserved ReservedBuckets
}

func NewHandler(r Repository, p Provisioner, policyEngine cedar.Authorizer) *Handler {
	if policyEngine == nil {
		panic("bucketh: policy authorizer is required")
	}
	return &Handler{repo: r, provisioner: p, policy: policyEngine, cel: celpkg.NewEvaluator(), log: zap.NewNop()}
}

// SetConfiguredBackends records the backend ids declared in storage.backends.
func (h *Handler) SetConfiguredBackends(ids []string) {
	h.configured = make(map[string]bool, len(ids))
	for _, id := range ids {
		h.configured[id] = true
	}
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
func (h *Handler) authorize(ctx context.Context, action cedar.Action, backendID, bucketName string, ownerTenantID uuid.UUID) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipal(p),
		action,
		&cedar.Resource{
			BackendID:     backendID,
			BucketName:    bucketName,
			OwnerTenantID: ownerTenantID,
		},
		cedar.RequestContext{Now: time.Now()},
	)
	if err != nil {
		return apiutil.MapError(fmt.Errorf("authz: %w", err))
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
	// platform.tenant-provisioner too (ADR-0011): a consumer's tenant is
	// unusable until its bucket exists, and provisioning has to run without a
	// human. Creating a bucket only adds a destination — the role carries no
	// authority to reconfigure or remove one (see Update/Delete/Configure*
	// below, which stay bucket-admin), and none at all over its contents.
	if err := apiutil.RequireAnyRole(ctx,
		apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin, apiutil.RoleTenantProvisioner); err != nil {
		return nil, err
	}
	if in.Bucket.BackendID == "" || in.Bucket.BucketName == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("backend_id and bucket_name required"))
	}
	// The data plane enforces these on every upload; constraints no upload
	// could satisfy are refused here rather than discovered there.
	if err := in.Bucket.Constraints.Validate(); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("constraints: %w", err))
	}
	if err := h.authorize(ctx, cedar.ActionManageBucket, in.Bucket.BackendID, in.Bucket.BucketName, in.Bucket.OwnerTenantID); err != nil {
		return nil, err
	}
	if err := h.checkPublicBucket(ctx, in); err != nil {
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
	if h.configured != nil && !h.configured[in.Bucket.BackendID] {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"backend %q is not declared in storage.backends; the data plane and the worker "+
				"build their storage clients from the configuration only, so a bucket on it "+
				"could not be provisioned or reached — declare the backend there first", in.Bucket.BackendID))
	}
	if err := h.checkOwnership(ctx, in.Bucket.BackendID, in.Bucket.BucketName, in.ProvisionOnBackend); err != nil {
		return nil, err
	}
	// Outbox model: the DB row is the source of truth. When the caller
	// asked us to create the physical bucket too, we mark the row
	// 'pending' and let the reconciler worker drive the backend
	// CreateBucket — that way a DB-write failure can never leave an
	// orphan in S3, and a backend-side failure is observable on the row
	// instead of being lost to a 5xx that never made it to the client.
	// Paladin creates a bucket it provisions — checkOwnership saw the backend
	// without it — so that bucket is Paladin's to delete there.
	in.Bucket.CreatedOnBackend = in.ProvisionOnBackend
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
		// The repo types what it can: a duplicate is ErrAlreadyExists, an
		// unregistered backend or a live reference is ErrConflict. The central
		// registry (ADR-0002) turns those into AlreadyExists and
		// FailedPrecondition respectively.
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
	if err := in.Bucket.Constraints.Validate(); err != nil {
		return nil, false, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("constraints: %w", err))
	}
	// Self-service carries no ConfigurePublicRead check, so it never publishes
	// (ADR-0027).
	if in.Bucket.PublicRead || in.Bucket.PublicBaseURL != "" {
		return nil, false, apiutil.MapError(publicread.Rulef("a public bucket is created through CreateBucket only"))
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
	// A tenant creates its shared bucket; it never takes one that exists —
	// that would hand it a bucket it does not own — nor Paladin's own.
	if err := h.checkOwnership(ctx, in.Bucket.BackendID, in.Bucket.BucketName, in.ProvisionOnBackend); err != nil {
		return nil, false, err
	}
	in.Bucket.CreatedOnBackend = in.ProvisionOnBackend
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
		// Lost a race to a concurrent create (unique violation →
		// ErrAlreadyExists): the bucket now exists, so honour idempotency and
		// report it existing.
		//
		// This sentinel moved when the duplicate stopped being ErrConflict.
		// Matching the old one here would have kept compiling and quietly
		// broken the race path — the branch that exists precisely because it
		// is hard to reach on purpose.
		if errors.Is(err, admindomain.ErrAlreadyExists) {
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
	// A provisioner reads before it creates — that read is what makes adopting
	// an existing bucket a no-op instead of a conflict. It falls into the
	// redacted branch below with tenant admins, so it never sees policy text.
	if err := apiutil.RequireAnyRole(ctx,
		apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin, apiutil.RoleTenantAdmin,
		apiutil.RoleTenantProvisioner); err != nil {
		return nil, err
	}
	b, err := h.repo.Get(ctx, backendID, bucketName)
	if err != nil {
		return nil, mapNotFound(err)
	}
	// Cedar runs after the read so the engine sees authoritative ownership.
	if err := h.authorize(ctx, cedar.ActionReadBucket, b.BackendID, b.BucketName, b.OwnerTenantID); err != nil {
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
	page, next, err := h.repo.List(ctx, args)
	if err != nil {
		return nil, "", err
	}
	page, err = celpkg.FilterPage(h.cel, celpkg.PhysicalBucketSchema, args.Filter, page, bucketRow)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("filter: %w", err))
	}
	return page, next, nil
}

// bucketRow projects a Bucket onto the variables PhysicalBucketSchema declares.
func bucketRow(b admindomain.Bucket) map[string]any {
	owner := ""
	if b.OwnerTenantID != uuid.Nil {
		owner = b.OwnerTenantID.String()
	}
	return map[string]any{
		"bucket_id":           b.BucketName,
		"backend_id":          b.BackendID,
		"display_name":        b.DisplayName,
		"versioning_enabled":  b.Versioning.Enabled,
		"object_lock_enabled": b.ObjectLock.Enabled,
		"replication_enabled": b.Replication.Enabled,
		"owner_tenant_id":     owner,
		"created_at":          b.CreatedAt,
		// Must stay identical to the SQL in ListBucketsV2's `search_like`
		// clause. They are two spellings of one definition, and the pushdown
		// contract — narrow only, never drop a row CEL accepts — holds only
		// while they agree. TestSearchFieldMatchesSQLDefinition pins them.
		// Includes the backend id: the command palette matches on it, and a
		// bucket is as often identified by where it lives as by its own name.
		"search": celpkg.SearchText(b.BucketName, b.DisplayName, b.BackendID),
	}
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
	if err := h.authorize(ctx, cedar.ActionManageBucket, in.Bucket.BackendID, in.Bucket.BucketName, in.Bucket.OwnerTenantID); err != nil {
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
	if err := h.refuseLifecycleOnPublic(ctx, backendID, bucketName, rules); err != nil {
		return nil, err
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
	// Object lock attaches retention to a VERSION, so a bucket without
	// versioning has nothing to attach it to: every SetObjectRetention would
	// fail on "no current version" while the bucket reported object lock as
	// enabled. S3 has the same precondition, for the same reason.
	if lock.Enabled {
		current, err := h.repo.Get(ctx, backendID, bucketName)
		if err != nil {
			return nil, mapVersion(err)
		}
		if !current.Versioning.Enabled {
			return nil, connect.NewError(connect.CodeFailedPrecondition,
				errors.New("object lock requires versioning; enable versioning on the bucket first"))
		}
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
	// The other half of the same precondition. Turning versioning off under a
	// lock-enabled bucket would strand every existing retention: the rows
	// stay, the trigger keeps enforcing them, and nothing can create the
	// version a future lock would need. Refusing here means the operator has
	// to disable object lock first, which is the decision they are actually
	// making.
	if !v.Enabled {
		current, err := h.repo.Get(ctx, backendID, bucketName)
		if err != nil {
			return nil, mapVersion(err)
		}
		if current.ObjectLock.Enabled {
			return nil, connect.NewError(connect.CodeFailedPrecondition,
				errors.New("cannot disable versioning while object lock is enabled; disable object lock first"))
		}
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

// heldBy renders the non-empty reference counts as "2 collections, 1
// tenant_default_bindings", or "" when nothing holds the bucket. Relation
// names are the table names on purpose: they are what the constraint error
// would have said, what the operator can query, and what stays true when the
// API's vocabulary and the schema's drift apart.
func heldBy(refs []admindomain.BucketReference) string {
	var parts []string
	for _, r := range refs {
		if r.Count > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", r.Count, r.Relation))
		}
	}
	return strings.Join(parts, ", ")
}

func (h *Handler) DeleteBucket(ctx context.Context, in DeleteBucketInput) error {
	if err := apiutil.RequireAnyRole(ctx, apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin); err != nil {
		return err
	}
	if err := h.authorize(ctx, cedar.ActionManageBucket, in.BackendID, in.BucketName, uuid.Nil); err != nil {
		return err
	}
	// Refuse while anything still holds this bucket under ON DELETE RESTRICT.
	// Without the check the only thing standing in the way is the foreign key
	// itself, and a foreign key is not an API contract: the caller got
	// CodeInternal with a raw "violates foreign key constraint ... (SQLSTATE
	// 23503)" string. The outbox path got further still — MarkDeleting is an
	// UPDATE, so no constraint objects, and the state commits; the reconciler
	// then retries a row delete that can never land.
	//
	// All six relations, not just collections. The first version of this
	// guard counted collections alone, which is the most obvious holder and
	// far from the only one: a bucket with no collections but a tenant
	// default binding sailed past it and produced the very error the guard
	// was added to remove. The list lives in the query and is checked against
	// pg_constraint by an integration test, so adding a seventh relation
	// fails that test instead of quietly reopening this.
	//
	// The count runs cross-tenant deliberately. Buckets are platform-level
	// and nearly every relation holding them is tenant-scoped, so under the
	// RLS pool a tenant-scoped session counts zero and reports a bucket full
	// of another tenant's data as free. Measured, not assumed: as paladin_app
	// with no session tenant the same count returns 0 where the owner sees 1,
	// while the FK check — an internal trigger that does not consult RLS —
	// sees the row either way. The platform.admin / bucket.admin gate above
	// is what licenses the wider read.
	refs, err := h.repo.CountBucketReferences(
		auth.WithCrossTenantRead(ctx), in.BackendID, in.BucketName)
	if err != nil {
		return connect.NewError(connect.CodeInternal,
			fmt.Errorf("count references to bucket: %w", err))
	}
	if held := heldBy(refs); held != "" {
		return connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("bucket %q is still referenced by %s; remove those first",
				in.BucketName, held))
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
	if in.DeleteOnBackend {
		current, err := h.repo.Get(ctx, in.BackendID, in.BucketName)
		if err != nil {
			return apiutil.MapError(err)
		}
		if !current.CreatedOnBackend {
			return apiutil.MapError(fmt.Errorf(
				"%w: %q holds data Paladin did not write; delete the row only, and the bucket out of band",
				ErrBucketNotCreatedByPaladin, in.BucketName))
		}
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
