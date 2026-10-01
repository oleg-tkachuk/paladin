// Package collection implements the CollectionService business logic.
//
// Buckets are logical namespaces mapped onto a physical storage backend.
// Create/Update/Delete operations go through Cedar authorization. Delete is
// restricted if any non-DELETED objects still reference the collection (FK).
package collectionh

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

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	celpkg "github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// EventProducer mirrors the seam used by tenanth / bucketh — narrow
// interface so handler tests can stub the dispatcher. *worker.Dispatcher
// implements it. nil-safe via dispatchEvent's guard.
type EventProducer interface {
	Dispatch(ctx context.Context, tenantID string, evt worker.Event) (int, error)
	// DispatchTx fans the event out on the caller's tx so the outbox rows
	// commit atomically with the collection mutation (ADR-0003).
	DispatchTx(ctx context.Context, tx pgx.Tx, tenantID string, evt worker.Event) (int, error)
}

type Collection struct {
	TenantID        uuid.UUID
	Collection      string
	DisplayName     string
	BackendID       string
	BucketName      string
	CedarPolicy     string
	LifecycleRules  []byte // JSONB bytes; parsed by caller if needed
	ResourceVersion int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type CollectionStats struct {
	ObjectCountAvailable int64
	ObjectCountPending   int64
	ObjectCountDeleted   int64
	SizeBytesAvailable   int64
}

type CreateCollectionArgs struct {
	TenantID       uuid.UUID
	Collection     string
	DisplayName    string
	BackendID      string
	BucketName     string
	CedarPolicy    string
	LifecycleRules []byte
}

type UpdateCollectionArgs struct {
	TenantID        uuid.UUID
	Collection      string
	ExpectedVersion int64
	DisplayName     *string
	CedarPolicy     *string
	LifecycleRules  []byte
}

type ListCollectionsArgs struct {
	TenantID  uuid.UUID
	PageSize  int32
	PageToken string
	// BackendID + BucketName are optional server-side filters. When both
	// are set, only Collections bound to that (backend, bucket) pair are
	// returned. Used by the storage-first UI browser to avoid pulling
	// every OK platform-wide just to client-filter a handful per bucket.
	BackendID  string
	BucketName string
	// Filter is a CEL expression over CollectionSchema. The repo pushes its
	// SQL-expressible conjuncts into the query and the handler evaluates the
	// whole expression over the page it gets back; the repo cursor is
	// returned unchanged.
	Filter string
}

type Repository interface {
	Create(ctx context.Context, args CreateCollectionArgs) (Collection, error)
	Get(ctx context.Context, tenantID uuid.UUID, collection string) (Collection, error)
	Update(ctx context.Context, args UpdateCollectionArgs) (Collection, error)
	Delete(ctx context.Context, tenantID uuid.UUID, collection string, expectedVersion int64) error
	List(ctx context.Context, args ListCollectionsArgs) ([]Collection, string, error)
	Stats(ctx context.Context, tenantID uuid.UUID, collection string) (CollectionStats, error)
	// Rebind atomically swaps the (backend_id, bucket_name) target. DB
	// trigger enforces tenancy on single-tenant buckets.
	Rebind(ctx context.Context, tenantID uuid.UUID, collection, backendID, bucketName string, expectedVersion int64) error

	// RunInTx + the *Tx variants are the ADR-0003 seam: mutation + outbox
	// fan-out on one tx so a crash can't leave a committed change without
	// its event.
	RunInTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error
	CreateTx(ctx context.Context, tx pgx.Tx, args CreateCollectionArgs) (Collection, error)
	UpdateTx(ctx context.Context, tx pgx.Tx, args UpdateCollectionArgs) (Collection, error)
	DeleteTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, collection string, expectedVersion int64) error
}

type Handler struct {
	repo   Repository
	policy cedar.Authorizer

	events EventProducer
	log    *zap.Logger
	// cel compiles and caches List filters (program cache only).
	cel *celpkg.Evaluator
}

func NewHandler(repo Repository, policy cedar.Authorizer) *Handler {
	return &Handler{cel: celpkg.NewEvaluator(), repo: repo, policy: policy, log: zap.NewNop()}
}

// SetEventProducer / SetLogger — same opt-in contract as tenanth /
// bucketh. nil-safe; an unset producer makes dispatchEvent a no-op
// so unit tests don't need to stand up the outbox.
func (h *Handler) SetEventProducer(p EventProducer) { h.events = p }
func (h *Handler) SetLogger(l *zap.Logger) {
	if l != nil {
		h.log = l
	}
}

// dispatchEventTx fans the event out on the caller's tx so the outbox rows
// commit atomically with the collection mutation (ADR-0003). Returns the
// error (caller rolls back); nil-safe.
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

// collectionResourceName — kept as the C-shape emitter for callers
// that don't have (backend, bucket) in scope. Phase 1 prefers
// CanonicalName when the full tuple is available (event payloads
// after Create / Update / Delete read the row's backend/bucket and
// can canonicalize). Plain Delete with no row read still uses C.
func collectionResourceName(tenantID uuid.UUID, key string) string {
	return TenantPathName(tenantID, key)
}

func (h *Handler) CreateCollection(ctx context.Context, args CreateCollectionArgs) (*Collection, error) {
	callerTenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	// Resource-owner tenant: by default it's the caller's tenant. A
	// platform.admin can explicitly target a different tenant by
	// supplying `args.TenantID` (the connectshim parses this from
	// `parent: "tenants/{id}"` in the request). Without the
	// platform.admin gate the override is silently ignored so a
	// non-admin can't write into someone else's namespace.
	if args.TenantID == uuid.Nil {
		args.TenantID = callerTenantID
	} else if args.TenantID != callerTenantID {
		// platform.tenant-provisioner too: creating another tenant's object
		// keys IS provisioning, and it is the step that makes a freshly created
		// tenant usable. Its authority stops there — the role carries no
		// data-plane action, so it can name the namespace and never read or
		// write an object in it.
		if !principal.HasRole(apiutil.RolePlatformAdmin) &&
			!principal.HasRole(apiutil.RoleTenantProvisioner) {
			return nil, connect.NewError(connect.CodePermissionDenied,
				errors.New("cross-tenant CreateCollection requires platform.admin or platform.tenant-provisioner"))
		}
	}
	// A backend must be named explicitly — there is no default. The connectshim
	// resolves it from the named bucket or the tenant's default binding before
	// we get here; an empty id at this point means neither was supplied.
	if args.BackendID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("backend_id is required (name a bucket or set a tenant default binding; there is no default backend)"))
	}
	if err := h.authorizeFull(ctx, principal, args.TenantID, args.Collection, args.BackendID, args.BucketName, cedar.ActionManageCollection); err != nil {
		return nil, err
	}
	// Scope the connection's RLS tenant to the row's owner, now that Cedar
	// has allowed this caller to act on it. Without this a platform admin
	// creating for another tenant writes a row WITH CHECK rejects.
	ctx = auth.WithActingTenant(ctx, args.TenantID)
	// Create + paladin.collection.created in one tx (ADR-0003).
	var b Collection
	if err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var e error
		b, e = h.repo.CreateTx(ctx, tx, args)
		if e != nil {
			return e
		}
		return h.dispatchEventTx(ctx, tx, b.TenantID, "paladin.collection.created",
			CanonicalName(b.BackendID, b.BucketName, b.TenantID, b.Collection),
			map[string]any{
				"tenant_id":    b.TenantID.String(),
				"collection":   b.Collection,
				"display_name": b.DisplayName,
				"backend_id":   b.BackendID,
				"bucket_name":  b.BucketName,
			})
	}); err != nil {
		// MapError, not a hardcoded CodeInternal. The repository classifies
		// what it can — a duplicate name is ErrCollectionExists — and forcing
		// Internal here threw that away, so the mapping below the handler was
		// unreachable and every duplicate came back as a 500 carrying raw
		// SQLSTATE text. Anything genuinely unrecognised still lands on
		// Internal, which is MapError's own fallback.
		//
		// The prefix is gone with it: the adapter already labels its errors
		// "create collection: …", and adding a second one produced
		// "create collection: create collection: ERROR: …".
		return nil, apiutil.MapError(err)
	}
	// writes into the slot the audit mw installed
	apiutil.StashResource(ctx, CanonicalName(b.BackendID, b.BucketName, b.TenantID, b.Collection))
	return &b, nil
}

// EnsureCollection idempotently binds an object-key to (backend, bucket) under
// the CALLER's tenant for a caller that a higher layer has ALREADY authorized.
// Unlike CreateCollection there is NO Cedar ManageCollection check — the
// DATA-plane StorageBootstrapService gates on the EnsureTenantStorage Cedar
// action, and that action IS the authorization for the whole self-provision
// bundle. Do NOT mount this behind a surface that has not already authorized
// the caller.
//
// It is ALWAYS self-scoped: the tenant is taken from the request principal and
// any tenant on args is overwritten with the caller's own. Reuses
// CreateCollection's create + outbox/event path. Returns created=false when the
// key already existed (a no-op), created=true when a new row was written.
func (h *Handler) EnsureCollection(ctx context.Context, args CreateCollectionArgs) (bool, error) {
	callerTenantID, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return false, err
	}
	// Self-scope: the operation is ALWAYS the caller's own tenant. Never trust
	// a tenant id from the request.
	args.TenantID = callerTenantID
	if args.Collection == "" {
		return false, connect.NewError(connect.CodeInvalidArgument,
			errors.New("collection is required"))
	}
	if args.BackendID == "" || args.BucketName == "" {
		return false, connect.NewError(connect.CodeInvalidArgument,
			errors.New("backend_id and bucket_name are required"))
	}
	// Fast idempotent path: an existing key is a success no-op.
	switch _, gerr := h.repo.Get(ctx, args.TenantID, args.Collection); {
	case gerr == nil:
		return false, nil
	case errors.Is(gerr, pgx.ErrNoRows):
		// fall through to create
	default:
		return false, connect.NewError(connect.CodeInternal, gerr)
	}
	// Create + paladin.collection.created in one tx (ADR-0003), mirroring
	// CreateCollection.
	var b Collection
	if err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var e error
		b, e = h.repo.CreateTx(ctx, tx, args)
		if e != nil {
			return e
		}
		return h.dispatchEventTx(ctx, tx, b.TenantID, "paladin.collection.created",
			CanonicalName(b.BackendID, b.BucketName, b.TenantID, b.Collection),
			map[string]any{
				"tenant_id":    b.TenantID.String(),
				"collection":   b.Collection,
				"display_name": b.DisplayName,
				"backend_id":   b.BackendID,
				"bucket_name":  b.BucketName,
			})
	}); err != nil {
		// Lost a race to a concurrent create: the key now exists, so honour
		// idempotency and report it existing rather than surfacing the
		// unique-violation.
		if _, gerr := h.repo.Get(ctx, args.TenantID, args.Collection); gerr == nil {
			return false, nil
		}
		return false, connect.NewError(connect.CodeInternal, fmt.Errorf("ensure collection: %w", err))
	}
	return true, nil
}

// GetCollection reads one Collection under tenantID. tenantID comes from the
// resource name (resolve.ResolveCollectionName) so a platform-admin can open
// any tenant's Collection via /tenants/{t}/object-keys/{ok}; uuid.Nil (a bare
// name) defaults to the caller's tenant. A target tenant other than the
// caller's is platform.admin-only — the same cross-tenant gate ListCollections
// enforces, so a regular tenant can't read a sibling's Collection by crafting
// the name. Cedar then authorizes against the (target) tenant.
func (h *Handler) GetCollection(ctx context.Context, tenantID uuid.UUID, collection string) (*Collection, error) {
	callerTenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	if tenantID == uuid.Nil {
		tenantID = callerTenantID
	}
	// A provisioner reads before it creates — the ensure step is what makes
	// re-running provisioning a no-op instead of a conflict.
	if tenantID != callerTenantID &&
		!principal.HasRole(apiutil.RolePlatformAdmin) &&
		!principal.HasRole(apiutil.RoleTenantProvisioner) {
		return nil, connect.NewError(connect.CodePermissionDenied,
			errors.New("cross-tenant GetCollection requires platform.admin or platform.tenant-provisioner"))
	}
	if err := h.authorize(ctx, principal, tenantID, collection, cedar.ActionManageCollection); err != nil {
		return nil, err
	}
	ctx = auth.WithActingTenant(ctx, tenantID)
	b, err := h.repo.Get(ctx, tenantID, collection)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return &b, nil
}

// targetTenant decides which tenant a name-addressed write applies to.
//
// The resource name carries one ("tenants/{tid}/collections/{ok}") and the
// caller's token carries another. Get and List have always honoured the name,
// gating a foreign tenant on platform.admin; Delete and Update did not read it
// at all and silently used the caller's. For a platform admin naming another
// tenant's collection that is the worst shape a write can take: it addresses a
// row that exists, and operates on a different one — or, when the caller's
// tenant has no collection by that name, reports a version mismatch, which
// says the row moved rather than that it was never looked at.
//
// nil target (a bare name, or an explicit self) means the caller's own tenant.
func targetTenant(
	target, caller uuid.UUID, principal *auth.Principal, op string,
) (uuid.UUID, error) {
	if target == uuid.Nil || target == caller {
		return caller, nil
	}
	if !principal.HasRole(apiutil.RolePlatformAdmin) {
		return uuid.Nil, connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("cross-tenant %s requires platform.admin", op))
	}
	return target, nil
}

func (h *Handler) UpdateCollection(ctx context.Context, args UpdateCollectionArgs) (*Collection, error) {
	callerTenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := targetTenant(args.TenantID, callerTenantID, principal, "UpdateCollection")
	if err != nil {
		return nil, err
	}
	args.TenantID = tenantID
	if err := h.authorize(ctx, principal, tenantID, args.Collection, cedar.ActionManageCollection); err != nil {
		return nil, err
	}
	ctx = auth.WithActingTenant(ctx, tenantID)
	// Update + paladin.collection.updated in one tx (ADR-0003). UpdateTx reads
	// the post-update row back on the same tx (for resource_version).
	var b Collection
	if err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var e error
		b, e = h.repo.UpdateTx(ctx, tx, args)
		if e != nil {
			return e
		}
		return h.dispatchEventTx(ctx, tx, b.TenantID, "paladin.collection.updated",
			CanonicalName(b.BackendID, b.BucketName, b.TenantID, b.Collection),
			map[string]any{
				"tenant_id":        b.TenantID.String(),
				"collection":       b.Collection,
				"backend_id":       b.BackendID,
				"bucket_name":      b.BucketName,
				"resource_version": b.ResourceVersion,
			})
	}); err != nil {
		return nil, mapVersionErr(err)
	}
	apiutil.StashResource(ctx, CanonicalName(b.BackendID, b.BucketName, b.TenantID, b.Collection))
	return &b, nil
}

func (h *Handler) DeleteCollection(
	ctx context.Context, targetTenantID uuid.UUID, collection string, expectedVersion int64,
) error {
	callerTenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := targetTenant(targetTenantID, callerTenantID, principal, "DeleteCollection")
	if err != nil {
		return err
	}
	if err := h.authorize(ctx, principal, tenantID, collection, cedar.ActionManageCollection); err != nil {
		return err
	}
	ctx = auth.WithActingTenant(ctx, tenantID)
	// Read the row before delete so the event payload can carry the
	// canonical resource name (which needs backend + bucket). Best-
	// effort: if Get fails we fall back to the C-shape resource name —
	// the delete itself still runs through the OCC guard below.
	pre, getErr := h.repo.Get(ctx, tenantID, collection)
	resourceName := collectionResourceName(tenantID, collection)
	if getErr == nil {
		resourceName = CanonicalName(pre.BackendID, pre.BucketName, tenantID, collection)
	}
	// Delete + paladin.collection.deleted in one tx (ADR-0003). The resource
	// name comes from the pre-read above (best-effort; the OCC guard still
	// runs inside the tx).
	if err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if e := h.repo.DeleteTx(ctx, tx, tenantID, collection, expectedVersion); e != nil {
			return e
		}
		return h.dispatchEventTx(ctx, tx, tenantID, "paladin.collection.deleted",
			resourceName,
			map[string]any{
				"tenant_id":        tenantID.String(),
				"collection":       collection,
				"resource_version": expectedVersion,
			})
	}); err != nil {
		return mapVersionErr(err)
	}
	apiutil.StashResource(ctx, resourceName)
	return nil
}

func (h *Handler) ListCollections(ctx context.Context, args ListCollectionsArgs) ([]Collection, string, error) {
	callerTenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, "", err
	}
	// Cross-tenant listing (TenantID==Nil + a (backend, bucket) filter)
	// is reserved for platform.admin — used by the storage-first
	// browser to enumerate every OK on a given bucket. Without the
	// gate any tenant could enumerate sibling tenants' OKs by sending
	// an empty parent + a known bucket.
	if args.TenantID == uuid.Nil {
		if args.BackendID == "" || args.BucketName == "" {
			// Tenant-scoped list: pin to caller's tenant.
			args.TenantID = callerTenantID
		} else if !principal.HasRole(apiutil.RolePlatformAdmin) {
			return nil, "", connect.NewError(connect.CodePermissionDenied,
				errors.New("cross-tenant ListCollections requires platform.admin"))
		}
	} else if args.TenantID != callerTenantID {
		if !principal.HasRole(apiutil.RolePlatformAdmin) {
			return nil, "", connect.NewError(connect.CodePermissionDenied,
				errors.New("cross-tenant ListCollections requires platform.admin"))
		}
	}
	// One tenant-scoped Cedar check up front; per-row filtering would
	// dominate pagination cost so we don't repeat it for every collection.
	// For cross-tenant listing we authorize against the caller's tenant
	// (the principal-tenant invariant the Cedar engine encodes).
	crossTenant := args.TenantID == uuid.Nil
	authzTenant := args.TenantID
	if authzTenant == uuid.Nil {
		authzTenant = callerTenantID
	}
	if err := h.authorize(ctx, principal, authzTenant, "", cedar.ActionManageCollection); err != nil {
		return nil, "", err
	}
	if crossTenant {
		// The platform.admin gate above already passed; this is the
		// storage-first browser enumerating every collection on a bucket.
		// Scoping to one tenant cannot express that — it returned only the
		// admin's own collections, so a bucket full of other tenants' data
		// read as empty.
		ctx = auth.WithCrossTenantRead(ctx)
	} else {
		ctx = auth.WithActingTenant(ctx, authzTenant)
	}
	page, next, err := h.repo.List(ctx, args)
	if err != nil {
		return nil, "", err
	}
	page, err = celpkg.FilterPage(h.cel, celpkg.CollectionSchema, args.Filter, page, collectionRow)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("filter: %w", err))
	}
	return page, next, nil
}

// collectionRow projects a Collection onto the variables CollectionSchema
// declares. storage_backend is the (backend, bucket) pair the collection is
// bound to, rendered the way the console shows it.
func collectionRow(c Collection) map[string]any {
	backend := c.BackendID
	if c.BucketName != "" {
		backend = c.BackendID + "/" + c.BucketName
	}
	return map[string]any{
		"collection":      c.Collection,
		"storage_backend": backend,
		"display_name":    c.DisplayName,
		"created_at":      c.CreatedAt,
		// Identical to ListCollections' `search_like` clause. Note this joins
		// the COLLECTION name, not the storage_backend composite above: the
		// console's search box offers collection name and display name, and
		// the SQL narrows on those two columns.
		"search": celpkg.SearchText(c.Collection, c.DisplayName),
	}
}

func (h *Handler) GetCollectionStats(ctx context.Context, collection string) (*CollectionStats, error) {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, principal, tenantID, collection, cedar.ActionManageCollection); err != nil {
		return nil, err
	}
	ctx = auth.WithActingTenant(ctx, tenantID)
	s, err := h.repo.Stats(ctx, tenantID, collection)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &s, nil
}

// BindCollectionToBucket rebinds the namespace to a different bucket. Cedar
// authorization uses ActionBindCollectionToBucket on the namespace.
func (h *Handler) BindCollectionToBucket(
	ctx context.Context, targetTenantID uuid.UUID, collection, bucket string, expectedVersion int64,
) (*Collection, error) {
	callerTenantID, p, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := targetTenant(targetTenantID, callerTenantID, p, "BindCollectionToBucket")
	if err != nil {
		return nil, err
	}
	backendID, bucketName, err := splitBucketResourceName(bucket)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := h.authorizeFull(ctx, p, tenantID, collection, backendID, bucketName, cedar.ActionBindCollectionToBucket); err != nil {
		return nil, err
	}
	if err := h.repo.Rebind(ctx, tenantID, collection, backendID, bucketName, expectedVersion); err != nil {
		if errors.Is(err, ErrVersionMismatch) {
			return nil, connect.NewError(connect.CodeAborted, err)
		}
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("rebind: %w", err))
	}
	updated, err := h.repo.Get(ctx, tenantID, collection)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &updated, nil
}

// splitBucketResourceName parses "storageBackends/{backend}/buckets/{bucket}".
func splitBucketResourceName(name string) (backend, bucket string, err error) {
	parts := strings.Split(name, "/")
	if len(parts) != 4 || parts[0] != "storageBackends" || parts[2] != "buckets" {
		return "", "", fmt.Errorf("invalid bucket name %q", name)
	}
	return parts[1], parts[3], nil
}

// authorize is the binding-less variant used by the read-before-authz
// operations (Get / Update / Delete / Stats). These authorize BEFORE reading
// the collection row on purpose: reading first would let an unauthorized
// caller distinguish "exists" (→ PermissionDenied) from "not found" (→
// NotFound), leaking existence. So the (backend, bucket) binding is not
// available here, and under ADR-0014 the Cedar Collection EUID intentionally
// falls back to the legacy `{tid}/{ok}` form for these calls — canonicalizing
// them would require the pre-authz row read we deliberately avoid. Create /
// BindCollectionToBucket carry the binding in the request and use authorizeFull,
// so they get the canonical EUID when the flag is on.
func (h *Handler) authorize(ctx context.Context, p *auth.Principal, tenantID uuid.UUID, collection, action string) error {
	return h.authorizeFull(ctx, p, tenantID, collection, "", "", action)
}

// authorizeFull is the bucket-aware variant that passes the bucket binding
// to Cedar so the engine emits the full Collection←Bucket←StorageBackend
// hierarchy. Use whenever the caller has the binding in hand (Create,
// BindCollectionToBucket, post-Get on Update).
func (h *Handler) authorizeFull(
	ctx context.Context,
	p *auth.Principal,
	tenantID uuid.UUID,
	collection, backendID, bucketName, action string,
) error {
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipalFor(p, tenantID),
		action,
		&cedar.Resource{
			TenantID:   tenantID,
			Collection: collection,
			BackendID:  backendID,
			BucketName: bucketName,
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

// ErrCollectionHasObjects is returned when a Delete is blocked because
// the Collection still has rows in `objects` referencing it. The FK
// constraint is ON DELETE RESTRICT — operators must purge / move the
// objects first. Handler maps this to FAILED_PRECONDITION so the UI
// can surface a clear "remove the files first" prompt.
var ErrCollectionHasObjects = errors.New(
	"collection has live objects; remove or move them before deleting")

// ErrCollectionExists is returned when a Create is refused because the tenant
// already has a Collection by that name — UNIQUE (tenant_id, collection).
// Trying to create something that is already there is an ordinary answer to
// an ordinary request, not a server fault: before this existed the caller got
// `duplicate key value violates unique constraint
// "collections_tenant_id_name_key" (SQLSTATE 23505)` under CodeInternal.
var ErrCollectionExists = errors.New("collection already exists in this tenant")

// ErrBucketNotFound is returned when a Create names a (backend, bucket) pair
// that is not registered — or was deleted between the caller listing it and
// creating against it. It used to surface as a NOT NULL violation on
// collections.bucket_id under CodeInternal.
var ErrBucketNotFound = errors.New("bucket not found on this backend")

// ErrVersionMismatch is returned when optimistic-concurrency control fails.
// Repositories should surface it so handlers can map to CodeAborted.
var ErrVersionMismatch = errors.New("resource_version mismatch")

func mapVersionErr(err error) error {
	return apiutil.MapError(err)
}

// Register this package's sentinels with the central error→Connect-code
// mapper (ADR-0002). mapVersionErr now delegates to apiutil.MapError; the
// registry — not a per-handler if/else — decides the code.
func init() {
	apiutil.RegisterError(ErrVersionMismatch, connect.CodeAborted)
	apiutil.RegisterError(ErrCollectionHasObjects, connect.CodeFailedPrecondition)
	apiutil.RegisterError(ErrCollectionExists, connect.CodeAlreadyExists)
	apiutil.RegisterError(ErrBucketNotFound, connect.CodeNotFound)
	// A stored Cedar policy that will not compile is a state of the data, not
	// a fault of the server. Registered here rather than in the cedar package
	// because apiutil's registry is the API layer's, and cedar sits below it.
	apiutil.RegisterError(cedar.ErrPolicyUnparseable, connect.CodeFailedPrecondition)
}
