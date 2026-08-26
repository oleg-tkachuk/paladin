// Package tenant implements the TenantService business logic.
//
// Tenant creation is an admin-only operation. The AdminBucket action (with an
// empty collection) is reused as the "administrative" guard for tenant writes —
// production deployments can swap this for a dedicated platform-admin role
// check if Cedar gets a separate Tenant action.
package tenant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	celpkg "github.com/oleg-tkachuk/paladin/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/internal/worker"
)

// EventProducer is the narrow seam the handler uses to fan out tenant
// lifecycle events to subscribers. Defined here (not imported as
// `worker.Dispatcher`) so tests can stub it without spinning up the
// real outbox + Postgres. nil-safe: when the wiring layer omits the
// producer (e.g. in unit tests, or before the dispatcher is enabled
// in a fresh deployment) the handler logs a debug breadcrumb and
// continues — fan-out is best-effort, never blocks the RPC reply.
//
// Why a method named `Dispatch` and not `Publish` / `Fire`: this is
// the same vocabulary `worker.Dispatcher.Dispatch` uses; matching
// names lets the production wiring pass `*worker.Dispatcher`
// directly with no adapter.
type EventProducer interface {
	Dispatch(ctx context.Context, tenantID string, evt worker.Event) (int, error)
	// DispatchTx fans the event out on the caller's tx so the outbox rows
	// commit atomically with the tenant mutation (ADR-0003).
	DispatchTx(ctx context.Context, tx pgx.Tx, tenantID string, evt worker.Event) (int, error)
}

// Use apiutil.RolePlatformAdmin as the canonical role string ("platform.admin").
// A previous local copy here used the hyphen form which silently failed every
// Cedar permit because the JWT issuer mints dot-form roles.

type Tenant struct {
	TenantID             uuid.UUID
	Slug                 string
	DisplayName          string
	Labels               []byte // JSONB
	InheritedCedarPolicy string
	InheritedPolicyHash  []byte
	ResourceVersion      int64
	CreatedAt            time.Time
	UpdatedAt            time.Time
	// DeletedAt is zero (== time.Time{}) for active tenants. Migration
	// 036 added the underlying column; the soft-delete RPCs populate
	// it; restore clears it back to zero.
	DeletedAt time.Time
	// DefaultBucket is the resource name of the tenant's default (backend,
	// bucket) binding — "storageBackends/{backend_id}/buckets/{bucket_name}"
	// — sourced from tenant_default_bindings on read. Empty when unbound.
	// Populated only by the read paths (Get / GetBySlug / List).
	DefaultBucket string
	// StorageLayout — "shared" or "dedicated" (ADR-0011). Populated on read.
	StorageLayout string
}

type CreateTenantArgs struct {
	TenantID uuid.UUID
	// Slug — human-readable, required, validated by apiutil.ValidateTenantSlug.
	Slug                 string
	DisplayName          string
	Labels               []byte
	InheritedCedarPolicy string
	// DefaultBackendID + DefaultBucketName — required for tenants
	// landing under the canonical-resource-names model. Together they
	// pin the (backend, bucket) where this tenant's objects will live;
	// the repository inserts a corresponding row into
	// `tenant_default_bindings` in the same tx as the tenant insert.
	// Empty strings = no binding (legacy / scripted-bootstrap path).
	DefaultBackendID  string
	DefaultBucketName string
	// StorageLayout — "shared" (default) or "dedicated" (ADR-0011). Settable
	// only at create; empty defaults to "shared".
	StorageLayout string
	// DedicatedBackend is set by the handler (not the client) for a dedicated
	// tenant: the backend its own bucket is provisioned on. The repo derives
	// the bucket name and inserts a pending row + default binding in the
	// create tx; the bucket reconciler provisions it physically.
	DedicatedBackend string
}

type UpdateTenantArgs struct {
	TenantID             uuid.UUID
	ExpectedVersion      int64
	DisplayName          *string
	Labels               []byte
	InheritedCedarPolicy *string
}

// DefaultBinding is a tenant's default (backend, bucket) route for the bare
// collection name shape (ADR-0010 Phase 3 / the schema baseline (001_initial_schema.sql)).
type DefaultBinding struct {
	TenantID uuid.UUID
	// BucketID is the stored reference; BackendName and BucketName are carried
	// alongside it because the resource name the API returns is built from
	// names, and a name made of uuids resolves to nothing a client can use.
	BucketID    uuid.UUID
	BackendName string
	BucketName  string
	SetAt       time.Time
	SetBy       string
}

type Repository interface {
	Create(ctx context.Context, args CreateTenantArgs) (Tenant, error)
	// GetDefaultBinding returns the tenant's default (backend, bucket) route.
	// ErrNotFound when none is set.
	GetDefaultBinding(ctx context.Context, tenantID uuid.UUID) (DefaultBinding, error)
	// SetDefaultBinding upserts the tenant's default route and returns it.
	// ErrDefaultBindingBucketMissing when (backend, bucket) is not a real
	// bucket (the FK rejects it).
	SetDefaultBinding(ctx context.Context, tenantID uuid.UUID, bucket, setBy string) (DefaultBinding, error)
	// ClearDefaultBinding removes the tenant's default route. Idempotent —
	// clearing an absent binding is a no-op success.
	ClearDefaultBinding(ctx context.Context, tenantID uuid.UUID) error
	Get(ctx context.Context, tenantID uuid.UUID) (Tenant, error)
	// GetBySlug looks up a tenant by its kebab-case slug. ErrNotFound
	// when no row matches. Used by handlers accepting the slug-form
	// resource name (`tenants/{tenant_id_or_slug}`) — see
	// apiutil.ParseTenantNameRef.
	GetBySlug(ctx context.Context, slug string) (Tenant, error)
	Update(ctx context.Context, args UpdateTenantArgs) (Tenant, error)
	// SoftDelete sets deleted_at = now() on an active row. Returns
	// ErrAlreadyDeleted when the row is already trashed.
	SoftDelete(ctx context.Context, tenantID uuid.UUID, expectedVersion int64) error
	// Restore clears deleted_at on a trashed row. Returns
	// ErrNotTrashed when the row is currently active.
	Restore(ctx context.Context, tenantID uuid.UUID) (Tenant, error)
	List(ctx context.Context, args ListTenantsArgs) ([]Tenant, string, error)
	// Rename atomically updates tenants.slug AND rewrites every
	// `Tenant::"<old>"` reference in the tenant's
	// inherited_cedar_policy + every collections row's cedar_policy
	// for that tenant. Returns the renamed Tenant (with the new
	// resource_version). ErrVersionMismatch on OCC failure.
	Rename(ctx context.Context, args RenameTenantSlugArgs) (Tenant, error)
	// LookupRenamedSlug finds the most recent rotation away FROM oldSlug
	// within `window` (0 = unbounded), reading the tenant_slug_history table
	// written by Rename. found=false when no matching rotation exists. Used
	// by ResolveRenamedSlug to back a 404 "did you mean?" redirect.
	LookupRenamedSlug(ctx context.Context, oldSlug string, window time.Duration) (res RenamedSlug, found bool, err error)

	// RunInTx + the *Tx mutation variants are the ADR-0003 seam: the
	// handler runs a mutation and its outbox fan-out on one tx so a crash
	// can't leave a committed change without its event. RunInTx supplies
	// the tx; the *Tx methods run the mutation on it.
	RunInTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error
	CreateTx(ctx context.Context, tx pgx.Tx, args CreateTenantArgs) error
	UpdateTx(ctx context.Context, tx pgx.Tx, args UpdateTenantArgs) (Tenant, error)
	SoftDeleteTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, expectedVersion int64) error
	HardDeleteTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, expectedVersion int64) error
	RestoreTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) (Tenant, error)

	// StartStorageMigration provisions the tenant's dedicated bucket
	// (provision_state='pending', owned by the tenant) and inserts the
	// tenant_storage_migrations row (state='provisioning') in one transaction
	// (ADR-0011 Phase 3). ErrStorageMigrationExists when a migration row is
	// already present for the tenant. Returns the created migration.
	StartStorageMigration(ctx context.Context, args StartStorageMigrationArgs) (StorageMigration, error)
	// GetStorageMigration returns the tenant's migration status. ErrNotFound
	// when none was ever started.
	GetStorageMigration(ctx context.Context, tenantID uuid.UUID) (StorageMigration, error)
	// TenantSourceBucket returns the (backend, bucket) the tenant's objects
	// physically live in today — the DISTINCT binding across its collections.
	// A shared tenant's keys normally share one bucket; ErrSourceBucketAmbiguous
	// when they span more than one (unsupported in slice 1), ErrNotFound when
	// the tenant has no collections.
	TenantSourceBucket(ctx context.Context, tenantID uuid.UUID) (backendID, bucketName string, err error)
}

// StartStorageMigrationArgs is the input for Repository.StartStorageMigration.
type StartStorageMigrationArgs struct {
	TenantID          uuid.UUID
	SourceBackendName string
	SourceBucketName  string
	TargetBackendName string
	TargetBucketName  string
	// CleanupRetentionSeconds is how long the old (shared) copies are kept
	// after the migration completes before the cleanup phase deletes them.
	CleanupRetentionSeconds int64
}

// StorageMigration is a tenant's shared->dedicated copy-job status
// (tenant_storage_migrations row).
type StorageMigration struct {
	TenantID          uuid.UUID
	SourceBackendName string
	SourceBucketName  string
	TargetBackendName string
	TargetBucketName  string
	State             string
	ObjectsTotal      int64
	ObjectsCopied     int64
	Error             string
}

// ErrStorageMigrationExists is returned by StartStorageMigration when a
// migration row already exists for the tenant.
var ErrStorageMigrationExists = errors.New("storage migration already exists for tenant")

// ErrSourceBucketAmbiguous is returned by TenantSourceBucket when the tenant's
// collections span more than one (backend, bucket) — slice 1 migrates from a
// single source bucket.
var ErrSourceBucketAmbiguous = errors.New("tenant collections span multiple buckets; single-source migration only")

// RenameTenantSlugArgs is the input shape for Repository.Rename and
// Handler.RenameTenantSlug.
type RenameTenantSlugArgs struct {
	TenantID        uuid.UUID
	NewSlug         string
	ExpectedVersion int64
}

// RenamedSlug is one resolved rotation row from tenant_slug_history: the
// tenant that owns the new slug, the new slug itself, and when it rotated.
type RenamedSlug struct {
	TenantID  uuid.UUID
	NewSlug   string
	RenamedAt time.Time
}

// ListTenantsArgs replaces the previous List(pageSize, afterID) so
// callers can opt into seeing soft-deleted rows. Default behaviour
// (both flags false) returns the active set only.
type ListTenantsArgs struct {
	PageSize       int32
	AfterID        uuid.UUID
	IncludeTrashed bool
	OnlyTrashed    bool
	// Filter is a CEL expression over TenantSchema. The repo pushes its
	// SQL-expressible conjuncts into the query and the handler evaluates the
	// whole expression over the page it gets back; the repo cursor is
	// returned unchanged so paging continues past a page whose rows all
	// failed the predicate.
	Filter string
}

// Tenant gains a single bit of derived state for callers that need
// to render Active/Trashed differently. Internal callers read
// DeletedAt directly.
type TenantState string

const (
	TenantStateActive  TenantState = "active"
	TenantStateTrashed TenantState = "trashed"
)

type Handler struct {
	repo   Repository
	policy cedar.Authorizer
	// cel compiles and caches List filters. Owned here rather than injected:
	// it holds only a program cache, and every handler that filters needs one.
	cel *celpkg.Evaluator

	// events is optional — when nil, lifecycle Dispatch calls are
	// silent no-ops. Set via SetEventProducer once the dispatcher is
	// wired in build_listeners_admin (after repos + policy are
	// constructed but before the listener starts serving).
	events EventProducer
	log    *zap.Logger // best-effort sink for fan-out failures
}

// NewHandler builds a tenant handler. policyEngine is required — production
// wiring passes the live Cedar engine; tests inject a fake Authorizer.
func NewHandler(repo Repository, policyEngine cedar.Authorizer) *Handler {
	if policyEngine == nil {
		panic("tenant: policy authorizer is required")
	}
	return &Handler{repo: repo, policy: policyEngine, cel: celpkg.NewEvaluator(), log: zap.NewNop()}
}

// SetEventProducer attaches the optional outbox producer. nil clears
// the wiring (useful in tests). Production wiring lives in
// build_listeners_admin.go alongside SetDispatcher on the eventsubh
// handler.
func (h *Handler) SetEventProducer(p EventProducer) { h.events = p }

// SetLogger attaches a non-nop logger so fan-out failures surface in
// the admin pod's structured log. Without this, dispatch errors
// silently disappear — the handler still returns success because the
// underlying lifecycle write committed.
func (h *Handler) SetLogger(l *zap.Logger) {
	if l != nil {
		h.log = l
	}
}

// dispatchEventTx fans the event out on the caller's tx so the outbox rows
// commit atomically with the tenant mutation (ADR-0003). Unlike
// dispatchEvent, an error here is RETURNED so the caller rolls the mutation
// back — the client's at-least-once retry re-runs both. nil-safe.
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

// GetDefaultBinding returns the tenant's default (backend, bucket) route.
// Gated on read access to the tenant.
func (h *Handler) GetDefaultBinding(ctx context.Context, tenantID uuid.UUID) (*DefaultBinding, error) {
	if err := h.authorize(ctx, cedar.ActionReadTenant, tenantID); err != nil {
		return nil, err
	}
	b, err := h.repo.GetDefaultBinding(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// SetDefaultBinding upserts the tenant's default route. Gated on manage access;
// set_by is the calling principal's subject.
func (h *Handler) SetDefaultBinding(ctx context.Context, tenantID uuid.UUID, bucket string) (*DefaultBinding, error) {
	if err := h.authorize(ctx, cedar.ActionManageTenant, tenantID); err != nil {
		return nil, err
	}
	// authorize() above already resolved the principal, so a failure here is
	// an internal inconsistency, not a caller error. Recording "" would hide
	// it and leave a binding nobody can be held to.
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal,
			errors.New("set default binding: authorized request carries no principal"))
	}
	b, err := h.repo.SetDefaultBinding(ctx, tenantID, bucket, p.Subject)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// ClearDefaultBinding removes the tenant's default route. Gated on manage
// access; idempotent.
func (h *Handler) ClearDefaultBinding(ctx context.Context, tenantID uuid.UUID) error {
	if err := h.authorize(ctx, cedar.ActionManageTenant, tenantID); err != nil {
		return err
	}
	return h.repo.ClearDefaultBinding(ctx, tenantID)
}

// authorize evaluates Cedar against the Tenant resource.
func (h *Handler) authorize(ctx context.Context, action string, tenantID uuid.UUID) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipal(p),
		action,
		&cedar.Resource{TenantID: tenantID},
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

func (h *Handler) CreateTenant(ctx context.Context, args CreateTenantArgs) (*Tenant, error) {
	// Creating a tenant is provisioning, not tenant lifecycle: it brings a
	// consumer's account into existence and takes nothing away. Delete, purge,
	// restore and rename stay platform.admin-only below, so the provisioner
	// role can add tenants and never remove one.
	if err := requireProvisioningAuthority(ctx); err != nil {
		return nil, err
	}
	// tenant_id: client-supplied or server-generated. Zero UUID is
	// reserved as the in-memory sentinel meaning "no value" — reject
	// it explicitly so a caller passing all-zeros doesn't silently get
	// a server-generated row.
	if args.TenantID == uuid.Nil {
		args.TenantID = uuid.Must(uuid.NewV7())
	}
	// Slug is required. Migration 033 makes the column NOT NULL UNIQUE
	// and the API contract follows: no auto-derivation from tenant_id.
	// Operators who don't have a slug yet must pick one before they
	// can land a tenant — matches the user-facing identity model where
	// tenant_id is a UUID and slug is the human handle.
	if args.Slug == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("slug is required"))
	}
	if err := apiutil.ValidateTenantSlug(args.Slug); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("slug: %w", err))
	}
	// display_name defaults to slug when omitted. Trim before checking
	// so " " also triggers the default. Length + format are enforced
	// at the DB layer (tenants_display_name_format CHECK from migr 033).
	args.DisplayName = strings.TrimSpace(args.DisplayName)
	if args.DisplayName == "" {
		args.DisplayName = args.Slug
	}
	// Default binding (SHARED layout): (backend_id, bucket_name) must be both
	// empty or both set. Prevents half-formed bindings where the operator
	// picked a backend but forgot the bucket (or vice-versa) and ended up with
	// a tenant whose objects had no destination. The DEDICATED layout is exempt
	// — it legitimately carries backend-only (bucket derived) and has its own
	// validation in the switch arm below.
	args.DefaultBackendID = strings.TrimSpace(args.DefaultBackendID)
	args.DefaultBucketName = strings.TrimSpace(args.DefaultBucketName)
	if args.StorageLayout != "dedicated" &&
		(args.DefaultBackendID == "") != (args.DefaultBucketName == "") {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("default_binding requires both backend and bucket"))
	}
	// storage_layout defaults to "shared"; "dedicated" (ADR-0011) is accepted
	// and persisted here. The dedicated-bucket provisioning is wired in a
	// follow-up; until then a dedicated tenant behaves like a shared one.
	switch args.StorageLayout {
	case "", "shared":
		args.StorageLayout = "shared"
	case "dedicated":
		// A dedicated tenant gets its own bucket, and the caller MUST name the
		// backend to provision it on — there is no default. The backend is
		// carried in default_binding.backend_id; the bucket name is derived
		// (paladin-<tenant_uuid>), so a bucket name must NOT be supplied.
		if args.DefaultBackendID == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				errors.New("storage_layout 'dedicated' requires default_binding.backend_id naming the backend to provision on (there is no default backend)"))
		}
		if args.DefaultBucketName != "" {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				errors.New("default_binding.bucket_name cannot be combined with storage_layout 'dedicated' (the bucket name is derived)"))
		}
		// Consume the caller's backend as the dedicated backend and clear the
		// shared-binding fields so the dedicated provisioning path owns it.
		args.DedicatedBackend = args.DefaultBackendID
		args.DefaultBackendID = ""
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("storage_layout: must be 'shared' or 'dedicated', got %q", args.StorageLayout))
	}
	if err := h.authorize(ctx, cedar.ActionManageTenant, args.TenantID); err != nil {
		return nil, err
	}
	// A tenant with no inherited policy would be deny-all at the Cedar layer
	// (empty policy set → no permit rule matches). Seed a sensible default so
	// newly-created tenants can immediately read/write their own objects.
	// The default policy keys on the slug, so policies remain readable even
	// when the platform regenerates them after a rename.
	if args.InheritedCedarPolicy == "" {
		args.InheritedCedarPolicy = renderDefaultPolicy(args.TenantID, args.Slug)
	}
	// Create + paladin.tenant.created in one tx (ADR-0003). The event payload
	// is built from args (== the inserted row), so the row can be enqueued
	// without a read-back inside the tx; the full row is fetched post-commit
	// for the response.
	if err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if e := h.repo.CreateTx(ctx, tx, args); e != nil {
			return e
		}
		return h.dispatchEventTx(ctx, tx, args.TenantID, "paladin.tenant.created",
			fmt.Sprintf("tenants/%s", args.TenantID),
			map[string]any{
				"tenant_id":    args.TenantID.String(),
				"slug":         args.Slug,
				"display_name": args.DisplayName,
			})
	}); err != nil {
		return nil, apiutil.MapError(fmt.Errorf("create tenant: %w", err))
	}
	t, err := h.repo.Get(ctx, args.TenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create tenant: read back: %w", err))
	}
	return &t, nil
}

func (h *Handler) GetTenant(ctx context.Context, tenantID uuid.UUID) (*Tenant, error) {
	callerTenant, err := auth.TenantFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	// Non-admins may only view their own tenant. A provisioner reads the
	// tenant it is about to provision — to learn whether it exists at all, and
	// to carry its resource_version into the policy write — so it is admitted
	// here for the same reason it may create one.
	if callerTenant != tenantID {
		if err := requireProvisioningAuthority(ctx); err != nil {
			return nil, err
		}
	}
	if err := h.authorize(ctx, cedar.ActionReadTenant, tenantID); err != nil {
		return nil, err
	}
	t, err := h.repo.Get(ctx, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return &t, nil
}

// GetTenantBySlug resolves a slug to a tenant row and applies the
// same authorization gating as GetTenant. The tenant resource name
// format `tenants/{tenant_id_or_slug}` requires this — handlers
// receiving a non-UUID body have no other way to materialise the
// UUID needed for the standard auth path.
//
// Resolution is split (slug → row, row → authz) so the unauthorised
// caller still gets NotFound instead of "you aren't allowed to know
// this tenant exists" — keeps slug enumeration off the table.
//
// The authz block mirrors GetTenant verbatim — duplicated rather
// than delegated so the connectshim coverage test can statically
// see the gate markers (h.authorize / requirePlatformAdmin) in this
// method's body.
func (h *Handler) GetTenantBySlug(ctx context.Context, slug string) (*Tenant, error) {
	// Resolve slug → row first, then run auth. To prevent slug
	// enumeration via timing (existing-slug-but-denied vs.
	// missing-slug round-trip times), we always run the same
	// principal-extraction + authorize sequence and collapse all
	// non-success outcomes to CodeNotFound. The trade-off: a real
	// authz denial loses its specific reason, but slug enumeration
	// gains nothing.
	t, lookupErr := h.repo.GetBySlug(ctx, slug)
	// Always do the auth dance, even on lookup-miss, so the response
	// time is dominated by Cedar evaluation rather than the DB
	// round-trip. We resolve auth against `t` when present and
	// against the caller's own tenant when not (a no-op Cedar query
	// that still costs the engine ~the same).
	target := t.TenantID
	if lookupErr != nil {
		if ct, err := auth.TenantFromContext(ctx); err == nil {
			target = ct
		}
	}
	if _, err := auth.PrincipalFromContext(ctx); err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	callerTenant, _ := auth.TenantFromContext(ctx)
	if lookupErr == nil && callerTenant != t.TenantID {
		if err := requirePlatformAdmin(ctx); err != nil {
			// Constant-time: same outcome shape as a missing row.
			return nil, connect.NewError(connect.CodeNotFound, ErrNotFound)
		}
	}
	if err := h.authorize(ctx, cedar.ActionReadTenant, target); err != nil {
		// Same — opaque NotFound rather than PermissionDenied keeps
		// existing-vs-missing slug indistinguishable.
		return nil, connect.NewError(connect.CodeNotFound, ErrNotFound)
	}
	if lookupErr != nil {
		return nil, connect.NewError(connect.CodeNotFound, lookupErr)
	}
	return &t, nil
}

// MigrateTenantStorageLayout starts a shared->dedicated migration (ADR-0011
// Phase 3): it validates the tenant is currently shared, provisions the
// tenant's dedicated bucket, and records the copy job. The async
// StorageMigrationWorker does the actual object copy + rebind. Returns the
// initial migration status.
func (h *Handler) MigrateTenantStorageLayout(ctx context.Context, tenantID uuid.UUID, targetBackendID string, cleanupRetentionSeconds int64) (*StorageMigration, error) {
	if err := requirePlatformAdmin(ctx); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, cedar.ActionManageTenant, tenantID); err != nil {
		return nil, err
	}

	t, err := h.repo.Get(ctx, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if t.StorageLayout != "shared" {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("tenant is %q, not 'shared'; only shared tenants can migrate to dedicated", t.StorageLayout))
	}

	// Source is where the tenant's objects physically live today — the
	// (backend, bucket) its collections bind to. (Not the default binding,
	// which is only for the bare-name shape and is often unset on shared
	// tenants whose keys carry explicit bindings.)
	srcBackend, srcBucket, err := h.repo.TenantSourceBucket(ctx, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("cannot determine source bucket to migrate from: %w", err))
	}

	targetBackend := targetBackendID
	if targetBackend == "" {
		targetBackend = srcBackend
	}
	// Retention before the old copies are deleted; default 24h when unset.
	retention := cleanupRetentionSeconds
	if retention <= 0 {
		retention = 86400
	}
	args := StartStorageMigrationArgs{
		TenantID:                tenantID,
		SourceBackendName:       srcBackend,
		SourceBucketName:        srcBucket,
		TargetBackendName:       targetBackend,
		TargetBucketName:        "paladin-" + tenantID.String(),
		CleanupRetentionSeconds: retention,
	}
	m, err := h.repo.StartStorageMigration(ctx, args)
	if err != nil {
		if errors.Is(err, ErrStorageMigrationExists) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &m, nil
}

// GetTenantStorageMigration returns a tenant's migration status.
func (h *Handler) GetTenantStorageMigration(ctx context.Context, tenantID uuid.UUID) (*StorageMigration, error) {
	if err := requirePlatformAdmin(ctx); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, cedar.ActionReadTenant, tenantID); err != nil {
		return nil, err
	}
	m, err := h.repo.GetStorageMigration(ctx, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return &m, nil
}

// defaultRenameGraceWindow bounds how long after a rename the old slug still
// resolves. A candidate config knob; 30d matches the BACKLOG default and a
// typical bookmark-staleness horizon.
const defaultRenameGraceWindow = 30 * 24 * time.Hour

// ResolveRenamedSlug maps oldSlug → the slug its tenant uses now, for a 404
// "did you mean?" redirect. Authorized by READ access to the RESOLVED target
// tenant — so a member of that tenant (or a platform admin) can follow a stale
// link, but nobody else. Every non-success outcome (no history, outside the
// grace window, read-denied) collapses to NotFound, the same anti-enumeration
// contract as GetTenantBySlug: the endpoint can't be used to map slugs to
// tenants the caller has no access to.
func (h *Handler) ResolveRenamedSlug(ctx context.Context, oldSlug string) (string, time.Time, error) {
	res, found, lookupErr := h.repo.LookupRenamedSlug(ctx, oldSlug, defaultRenameGraceWindow)

	// Run the auth dance regardless of the lookup outcome so existing-but-
	// denied and missing are timing-indistinguishable. Authorize ReadTenant
	// on the resolved tenant when present, else on the caller's own (a no-op
	// query of ~the same cost).
	target := res.TenantID
	if !found || lookupErr != nil {
		if ct, terr := auth.TenantFromContext(ctx); terr == nil {
			target = ct
		}
	}
	if _, perr := auth.PrincipalFromContext(ctx); perr != nil {
		return "", time.Time{}, connect.NewError(connect.CodeUnauthenticated, perr)
	}
	if authErr := h.authorize(ctx, cedar.ActionReadTenant, target); authErr != nil {
		return "", time.Time{}, connect.NewError(connect.CodeNotFound, ErrNotFound)
	}
	if lookupErr != nil {
		return "", time.Time{}, connect.NewError(connect.CodeInternal, lookupErr)
	}
	if !found {
		return "", time.Time{}, connect.NewError(connect.CodeNotFound, ErrNotFound)
	}
	return res.NewSlug, res.RenamedAt, nil
}

func (h *Handler) UpdateTenant(ctx context.Context, args UpdateTenantArgs) (*Tenant, error) {
	// SetInheritedPolicy lands here, and it is the one edit a provisioner must
	// make: a freshly created tenant is unusable until its policy admits the
	// consumer. Everything else UpdateTenant can change — display name, labels
	// — stays platform.admin, so the gate is on the SHAPE of the update rather
	// than on the RPC that produced it. A provisioner that tried to rename a
	// tenant while setting its policy is refused outright rather than having
	// the extra field quietly dropped.
	if err := requirePlatformAdmin(ctx); err != nil {
		policyOnly := args.InheritedCedarPolicy != nil &&
			args.DisplayName == nil && args.Labels == nil
		if !policyOnly {
			return nil, err
		}
		if provErr := apiutil.RequireRole(ctx, apiutil.RoleTenantProvisioner); provErr != nil {
			return nil, err
		}
	}
	if err := h.authorize(ctx, cedar.ActionManageTenant, args.TenantID); err != nil {
		return nil, err
	}
	// display_name validation: trim and reject empty edits — an empty
	// string would clear the column (UpdateTenant uses COALESCE on
	// nullable args, but the API path passes a *string so empty means
	// "set to empty"). The DB CHECK would reject it; surface a clearer
	// error before the round-trip.
	if args.DisplayName != nil {
		trimmed := strings.TrimSpace(*args.DisplayName)
		if trimmed == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				errors.New("display_name must not be empty"))
		}
		args.DisplayName = &trimmed
	}
	// Update + paladin.tenant.updated in one tx (ADR-0003). The event needs the
	// post-update row (resource_version), so UpdateTx reads it back on the
	// same tx. Snapshot semantics: subscribers diff against their cached
	// state; we don't ship a `fields_changed` list (the FieldMask lives
	// upstream of the handler).
	var t Tenant
	if err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var e error
		t, e = h.repo.UpdateTx(ctx, tx, args)
		if e != nil {
			return e
		}
		return h.dispatchEventTx(ctx, tx, t.TenantID, "paladin.tenant.updated",
			fmt.Sprintf("tenants/%s", t.TenantID),
			map[string]any{
				"tenant_id":        t.TenantID.String(),
				"slug":             t.Slug,
				"display_name":     t.DisplayName,
				"resource_version": t.ResourceVersion,
			})
	}); err != nil {
		return nil, apiutil.MapError(err)
	}
	return &t, nil
}

// DeleteTenant supports two modes:
//   - force=false (default): soft-delete. Sets deleted_at; tenant
//     becomes recoverable via RestoreTenant within the retention TTL.
//   - force=true: hard-delete. Skips the trash entirely.
//
// The force flag preserves the legacy "rip and run" path that E2E
// cleanups + emergency procedures rely on, while the default protects
// operators from undoable accidents.
// DeleteTenant moves the tenant to the trash. It is the only thing this call
// does now: the `force` flag that used to make it hard-delete instead is gone,
// because landing the tenant in a different state is a different transition,
// and that transition already has a name — PurgeTenant. A caller that wants
// the tenant gone makes two calls, the destructive one saying so.
func (h *Handler) DeleteTenant(ctx context.Context, tenantID uuid.UUID, expectedVersion int64) error {
	if err := requirePlatformAdmin(ctx); err != nil {
		return err
	}
	if err := h.authorize(ctx, cedar.ActionManageTenant, tenantID); err != nil {
		return err
	}
	// Soft delete + lifecycle event in one tx (ADR-0003).
	if err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if e := h.repo.SoftDeleteTx(ctx, tx, tenantID, expectedVersion); e != nil {
			return e
		}
		return h.dispatchEventTx(ctx, tx, tenantID, "paladin.tenant.trashed",
			fmt.Sprintf("tenants/%s", tenantID),
			map[string]any{
				"tenant_id":        tenantID.String(),
				"resource_version": expectedVersion,
			})
	}); err != nil {
		// ErrVersionMismatch→Aborted, ErrNotFound→NotFound,
		// ErrAlreadyDeleted/ErrTenantHasChildren→FailedPrecondition — all
		// via the central registry (ADR-0002).
		return apiutil.MapError(err)
	}
	return nil
}

// RestoreTenant returns a soft-deleted tenant to the active set.
// Slug + display_name UNIQUE constraints span both sets, so a slug
// claimed by a fresh tenant since soft-delete will trip the unique
// constraint at the DB level and surface as ALREADY_EXISTS.
func (h *Handler) RestoreTenant(ctx context.Context, tenantID uuid.UUID) (*Tenant, error) {
	if err := requirePlatformAdmin(ctx); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, cedar.ActionManageTenant, tenantID); err != nil {
		return nil, err
	}
	// Restore + paladin.tenant.restored in one tx (ADR-0003).
	var t Tenant
	if err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var e error
		t, e = h.repo.RestoreTx(ctx, tx, tenantID)
		if e != nil {
			return e
		}
		return h.dispatchEventTx(ctx, tx, tenantID, "paladin.tenant.restored",
			fmt.Sprintf("tenants/%s", tenantID),
			map[string]any{
				"tenant_id":    tenantID.String(),
				"slug":         t.Slug,
				"display_name": t.DisplayName,
			})
	}); err != nil {
		return nil, apiutil.MapError(err)
	}
	return &t, nil
}

// PurgeTenant hard-deletes a soft-deleted row. Requires the row to be
// trashed first; on an active tenant returns FailedPrecondition. The
// rare "skip the trash" path is DeleteTenant(force=true).
func (h *Handler) PurgeTenant(ctx context.Context, tenantID uuid.UUID) error {
	if err := requirePlatformAdmin(ctx); err != nil {
		return err
	}
	if err := h.authorize(ctx, cedar.ActionManageTenant, tenantID); err != nil {
		return err
	}
	// Pre-read so we can reject purging an active tenant before any
	// destructive call lands.
	t, err := h.repo.Get(ctx, tenantID)
	if err != nil {
		return connect.NewError(connect.CodeNotFound, err)
	}
	if t.DeletedAt.IsZero() {
		return connect.NewError(connect.CodeFailedPrecondition,
			errors.New("tenant is active; soft-delete it first or use DeleteTenant(force=true)"))
	}
	// expectedVersion=0 — the row is already trashed and OCC was
	// enforced at SoftDelete time. Purge is monotonically destructive.
	// Delete + paladin.tenant.purged in one tx (ADR-0003).
	if err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if e := h.repo.HardDeleteTx(ctx, tx, tenantID, 0); e != nil {
			return e
		}
		return h.dispatchEventTx(ctx, tx, tenantID, "paladin.tenant.purged",
			fmt.Sprintf("tenants/%s", tenantID),
			map[string]any{
				"tenant_id": tenantID.String(),
			})
	}); err != nil {
		return apiutil.MapError(err)
	}
	return nil
}

// RenameTenantSlug rewrites the tenant's slug AND rewrites every
// `Tenant::"<old_slug>"` reference in the tenant's inherited
// cedar_policy plus every collection's cedar_policy for the tenant.
// Single transaction in the repository; OCC-guarded against
// args.ExpectedVersion. Platform-admin only.
//
// Out of scope: per-bucket cedar_policy is not touched today — buckets
// don't carry slug references in current default policies. If a future
// bucket policy template starts referring to the slug, extend
// Repository.Rename to cover it.
func (h *Handler) RenameTenantSlug(ctx context.Context, args RenameTenantSlugArgs) (*Tenant, error) {
	if err := requirePlatformAdmin(ctx); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, cedar.ActionManageTenant, args.TenantID); err != nil {
		return nil, err
	}
	if err := apiutil.ValidateTenantSlug(args.NewSlug); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("new_slug: %w", err))
	}
	t, err := h.repo.Rename(ctx, args)
	if err != nil {
		return nil, apiutil.MapError(err)
	}
	return &t, nil
}

func (h *Handler) ListTenants(ctx context.Context, args ListTenantsArgs, pageToken string) ([]Tenant, string, error) {
	if err := requirePlatformAdmin(ctx); err != nil {
		return nil, "", err
	}
	if err := h.authorize(ctx, cedar.ActionReadTenant, uuid.Nil); err != nil {
		return nil, "", err
	}
	if pageToken != "" {
		id, err := uuid.Parse(pageToken)
		if err != nil {
			return nil, "", connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("invalid page_token: %w", err))
		}
		args.AfterID = id
	}
	page, next, err := h.repo.List(ctx, args)
	if err != nil {
		return nil, "", err
	}
	page, err = celpkg.FilterPage(h.cel, celpkg.TenantSchema, args.Filter, page, tenantRow)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("filter: %w", err))
	}
	return page, next, nil
}

// tenantRow projects a Tenant onto the variables TenantSchema declares.
func tenantRow(t Tenant) map[string]any {
	labels := map[string]string{}
	_ = json.Unmarshal(t.Labels, &labels)
	return map[string]any{
		"tenant_id":      t.TenantID.String(),
		"slug":           t.Slug,
		"display_name":   t.DisplayName,
		"storage_layout": t.StorageLayout,
		"labels":         labels,
		"created_at":     t.CreatedAt,
		"updated_at":     t.UpdatedAt,
	}
}

// requireProvisioningAuthority admits platform.admin or the narrow
// platform.tenant-provisioner role.
//
// Split from requirePlatformAdmin so the provisioning RPCs are the ONLY ones
// that widen: every other call site keeps the admin-only gate, and adding a
// role to this helper cannot accidentally grant tenant deletion.
func requireProvisioningAuthority(ctx context.Context) error {
	if err := apiutil.RequireAnyRole(ctx,
		apiutil.RolePlatformAdmin, apiutil.RoleTenantProvisioner); err != nil {
		return err
	}

	return nil
}

func requirePlatformAdmin(ctx context.Context) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	if !p.HasRole(apiutil.RolePlatformAdmin) {
		return connect.NewError(connect.CodePermissionDenied,
			errors.New("platform.admin role required"))
	}
	return nil
}

// ErrVersionMismatch — OCC failure surfaced by Repository.
var ErrVersionMismatch = errors.New("resource_version mismatch")

// ErrNotFound — Repository returns this when the target row does not exist
// and the operation did not use an OCC guard (so a 0-rows result is an
// unambiguous "missing", not a version conflict).
var ErrNotFound = errors.New("tenant not found")

// ErrSlugConflict — Repository.Rename or Create returns this when the
// requested slug is already in use by another tenant.
var ErrSlugConflict = errors.New("tenant slug already in use")

// ErrTenantIDConflict — Repository.Create returns this when the
// caller-supplied tenant_id (UUID) collides with an existing row.
var ErrTenantIDConflict = errors.New("tenant_id already in use")

// ErrDisplayNameConflict — Repository.Create or Update returns this
// when the requested display_name collides with another tenant's.
// display_name is UNIQUE since the schema baseline (001_initial_schema.sql).
var ErrDisplayNameConflict = errors.New("display_name already in use")

// ErrAlreadyDeleted — Repository.SoftDelete returns this when the
// row is already trashed. Surfaced as FAILED_PRECONDITION so UI can
// distinguish a re-delete from a successful one.
var ErrAlreadyDeleted = errors.New("tenant already in trash")

// ErrNotTrashed — Repository.Restore returns this when the row is
// currently active. Surfaced as FAILED_PRECONDITION.
var ErrNotTrashed = errors.New("tenant is not in trash")

// ErrDefaultBindingBucketMissing — Repository.Create returns this
// when the (backend_id, bucket_name) supplied as default binding
// doesn't reference an existing buckets row. The FK check on
// tenant_default_bindings raises 23503; the adapter maps it here.
var ErrDefaultBindingBucketMissing = errors.New(
	"default binding bucket does not exist on the chosen backend")

// ErrTenantHasChildren — Repository.HardDelete returns this when the
// tenant still owns collections / objects (the FK is ON DELETE RESTRICT).
// Surfaced as FAILED_PRECONDITION with actionable text rather than a raw
// Postgres FK-violation string mapped to Internal.
var ErrTenantHasChildren = errors.New(
	"tenant still has object keys or objects; delete them before hard-deleting the tenant")

// Register this package's sentinels with the central error→Connect-code
// mapper (ADR-0002). Each maps consistently to one code across every RPC,
// so the per-handler if/else ladders collapse to apiutil.MapError(err).
func init() {
	apiutil.RegisterError(ErrVersionMismatch, connect.CodeAborted)
	apiutil.RegisterError(ErrNotFound, connect.CodeNotFound)
	apiutil.RegisterError(ErrSlugConflict, connect.CodeAlreadyExists)
	apiutil.RegisterError(ErrTenantIDConflict, connect.CodeAlreadyExists)
	apiutil.RegisterError(ErrDisplayNameConflict, connect.CodeAlreadyExists)
	apiutil.RegisterError(ErrAlreadyDeleted, connect.CodeFailedPrecondition)
	apiutil.RegisterError(ErrNotTrashed, connect.CodeFailedPrecondition)
	apiutil.RegisterError(ErrDefaultBindingBucketMissing, connect.CodeInvalidArgument)
	apiutil.RegisterError(ErrTenantHasChildren, connect.CodeFailedPrecondition)
}
