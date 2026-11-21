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
	"strconv"
	"strings"
	"time"
	"uuid"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	celpkg "github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/publicread"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// EventProducer mirrors the seam used by tenanth / bucketh — narrow
// interface so handler tests can stub the dispatcher. *worker.EventRecorder
// implements it. nil-safe via dispatchEvent's guard.
type EventProducer interface {
	// RecordTx records the event on the caller's tx so it commits
	// atomically with the collection mutation (ADR-0003).
	RecordTx(ctx context.Context, tx pgx.Tx, tenantID string, evt worker.Event) error
}

type Collection struct {
	TenantID       uuid.UUID
	Collection     string
	DisplayName    string
	BackendID      string
	BucketName     string
	CedarPolicy    string
	LifecycleRules []byte // JSONB bytes; parsed by caller if needed
	// PublicRead: anyone may read the objects by URL (ADR-0027). Fixed at
	// creation, and equal to the bucket's.
	PublicRead bool
	// CacheControl is what a public collection stores objects with; "" for
	// a private one.
	CacheControl    string
	ResourceVersion int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
	// Usage is what the collection holds, set on a read and zero on the
	// answer to a change.
	Usage CollectionUsage
}

// CollectionUsage is what a collection holds: its available objects, not
// those still uploading or in the trash.
type CollectionUsage struct {
	ObjectCount int64
	TotalBytes  int64
	// LastCommittedAt is when the newest was stored; nil with none.
	LastCommittedAt *time.Time
}

// CollectionKey names a collection across tenants.
type CollectionKey struct {
	TenantID   uuid.UUID
	Collection string
}

type CreateCollectionArgs struct {
	TenantID       uuid.UUID
	Collection     string
	DisplayName    string
	BackendID      string
	BucketName     string
	CedarPolicy    string
	LifecycleRules []byte
	// PublicRead declares a public collection; CacheControl is its requested
	// Cache-Control, "" for the default (ADR-0027).
	PublicRead   bool
	CacheControl string
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
	// Usage is the usage of each collection keys names; one that holds no
	// available object, or is not on record, is absent.
	Usage(ctx context.Context, keys []CollectionKey) (map[CollectionKey]CollectionUsage, error)
	// Rebind atomically swaps the (backend_id, bucket_name) target. DB
	// trigger enforces tenancy on single-tenant buckets.
	Rebind(ctx context.Context, tenantID uuid.UUID, collection, backendID, bucketName string, expectedVersion int64) error
	// Holding counts what keeps the collection from being deleted.
	Holding(ctx context.Context, tenantID uuid.UUID, collection string) (CollectionHolding, error)

	// RunInTx + the *Tx variants are the ADR-0003 seam: mutation + event
	// record on one tx so a crash can't leave a committed change without
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

// answer maps err for the caller of op on the tenant's collection and logs it
// by what it is: a server fault at error, a refusal the caller can act on at
// warn.
func (h *Handler) answer(op string, tenantID uuid.UUID, collection string, err error) error {
	mapped := apiutil.MapError(err)
	code := connect.CodeOf(mapped)
	fields := []zap.Field{
		zap.String("op", op), zap.String("tenant_id", tenantID.String()), zap.String("collection", collection),
		zap.String("code", code.String()), zap.Error(err),
	}
	if apiutil.ServerFault(code) {
		h.log.Error("collection operation failed", fields...)
	} else {
		h.log.Warn("collection operation refused", fields...)
	}
	return mapped
}

// dispatchEventTx records the event on the caller's tx so it commits
// atomically with the collection mutation (ADR-0003). Returns the
// error (caller rolls back); nil-safe.
func (h *Handler) dispatchEventTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, eventType, resourceName string, payload map[string]any) error {
	if h.events == nil {
		return nil
	}
	actor := ""
	if p, err := auth.PrincipalFromContext(ctx); err == nil {
		actor = p.Subject
	}
	err := h.events.RecordTx(ctx, tx, tenantID.String(), worker.Event{
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
	if args.TenantID, err = createTenant(args.TenantID, callerTenantID, principal); err != nil {
		return nil, err
	}
	// A backend must be named explicitly — there is no default. The connectshim
	// resolves it from the named bucket or the tenant's default binding before
	// we get here; an empty id at this point means neither was supplied.
	if args.BackendID == "" {
		return nil, h.answer(opCreate, args.TenantID, args.Collection, connect.NewError(connect.CodeInvalidArgument,
			"backend_id is required (name a bucket or set a tenant default binding; there is no default backend)"))
	}
	if err := h.authorizeFull(ctx, principal, args.TenantID, args.Collection, args.BackendID, args.BucketName, cedar.ActionManageCollection); err != nil {
		return nil, err
	}
	if err := h.admitVisibility(ctx, principal, &args); err != nil {
		return nil, h.answer(opCreate, args.TenantID, args.Collection, err)
	}
	// Scope the connection's RLS tenant to the row's owner, now that Cedar
	// has allowed this caller to act on it. Without this a platform admin
	// creating for another tenant writes a row WITH CHECK rejects.
	ctx = auth.WithActingTenant(ctx, args.TenantID)
	b, err := h.createRow(ctx, args)
	if err != nil {
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
		return nil, h.answer(opCreate, args.TenantID, args.Collection, err)
	}
	// writes into the slot the audit mw installed
	apiutil.StashResource(ctx, CanonicalName(b.BackendID, b.BucketName, b.TenantID, b.Collection))
	return &b, nil
}

// createTenant is the tenant a new collection belongs to. By default it's the
// caller's tenant. A platform.admin can explicitly target a different tenant
// by supplying one (the connectshim parses it from `parent: "tenants/{id}"`
// in the request); anyone else is refused, so a non-admin can't write into
// someone else's namespace.
//
// platform.tenant-provisioner too: creating another tenant's object keys IS
// provisioning, and it is the step that makes a freshly created tenant usable.
// Its authority stops there — the role carries no data-plane action, so it can
// name the namespace and never read or write an object in it.
func createTenant(requested, caller uuid.UUID, principal *auth.Principal) (uuid.UUID, error) {
	if requested == uuid.Nil() || requested == caller {
		return caller, nil
	}
	if !principal.HasRole(apiutil.RolePlatformAdmin) && !principal.HasRole(apiutil.RoleTenantProvisioner) {
		return uuid.Nil(), connect.NewError(connect.CodePermissionDenied,
			"cross-tenant CreateCollection requires platform.admin or platform.tenant-provisioner")
	}
	return requested, nil
}

// createRow writes the collection args names and paladin.collection.created
// in one tx (ADR-0003), and returns the stored row.
func (h *Handler) createRow(ctx context.Context, args CreateCollectionArgs) (Collection, error) {
	var b Collection
	err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var e error
		b, e = h.repo.CreateTx(ctx, tx, args)
		if e != nil {
			return e
		}
		return h.dispatchEventTx(ctx, tx, b.TenantID, EventCollectionCreated,
			CanonicalName(b.BackendID, b.BucketName, b.TenantID, b.Collection),
			map[string]any{
				"tenant_id":    b.TenantID.String(),
				"collection":   b.Collection,
				"display_name": b.DisplayName,
				"backend_id":   b.BackendID,
				"bucket_name":  b.BucketName,
			})
	})
	return b, err
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
	if err := validateEnsure(args); err != nil {
		return false, h.answer(opEnsure, args.TenantID, args.Collection, err)
	}
	// Fast idempotent path: an existing key is a success no-op.
	switch _, gerr := h.repo.Get(ctx, args.TenantID, args.Collection); {
	case gerr == nil:
		return false, nil
	case !errors.Is(gerr, ErrCollectionNotFound):
		return false, h.answer(opEnsure, args.TenantID, args.Collection, gerr)
	}
	if _, err := h.createRow(ctx, args); err != nil {
		// Lost a race to a concurrent create: the key now exists, so honour
		// idempotency and report it existing rather than surfacing the
		// unique-violation.
		if _, gerr := h.repo.Get(ctx, args.TenantID, args.Collection); gerr == nil {
			return false, nil
		}
		// A refusal the repository named — a bucket the tenant may not bind
		// to, one that is not registered — keeps its code; anything else is
		// Internal.
		return false, h.answer(opEnsure, args.TenantID, args.Collection, fmt.Errorf("ensure collection: %w", err))
	}
	return true, nil
}

// validateEnsure refuses a self-provisioned collection that names no
// collection or binding, or that would be public: self-service carries no
// ConfigurePublicRead check, so it never publishes (ADR-0027).
func validateEnsure(args CreateCollectionArgs) error {
	if args.Collection == "" {
		return connect.NewError(connect.CodeInvalidArgument, "collection is required")
	}
	if args.BackendID == "" || args.BucketName == "" {
		return connect.NewError(connect.CodeInvalidArgument, "backend_id and bucket_name are required")
	}
	if args.PublicRead || args.CacheControl != "" {
		return publicread.Rulef("a public collection is created through CreateCollection only")
	}
	return nil
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
	if tenantID == uuid.Nil() {
		tenantID = callerTenantID
	}
	// A provisioner reads before it creates — the ensure step is what makes
	// re-running provisioning a no-op instead of a conflict.
	if tenantID != callerTenantID &&
		!principal.HasRole(apiutil.RolePlatformAdmin) &&
		!principal.HasRole(apiutil.RoleTenantProvisioner) {
		return nil, connect.NewError(connect.CodePermissionDenied,
			"cross-tenant GetCollection requires platform.admin or platform.tenant-provisioner")
	}
	if err := h.authorize(ctx, principal, tenantID, collection, cedar.ActionManageCollection); err != nil {
		return nil, err
	}
	ctx = auth.WithActingTenant(ctx, tenantID)
	b, err := h.repo.Get(ctx, tenantID, collection)
	if err != nil {
		return nil, h.answer(opGet, tenantID, collection, err)
	}
	page := []Collection{b}
	if err := h.withUsage(ctx, page); err != nil {
		return nil, h.answer(opGet, tenantID, collection, err)
	}
	return &page[0], nil
}

// withUsage sets each collection's Usage, read in one query for the page.
func (h *Handler) withUsage(ctx context.Context, page []Collection) error {
	if len(page) == 0 {
		return nil
	}
	keys := make([]CollectionKey, len(page))
	for i, c := range page {
		keys[i] = CollectionKey{TenantID: c.TenantID, Collection: c.Collection}
	}
	usage, err := h.repo.Usage(ctx, keys)
	if err != nil {
		return fmt.Errorf("collection usage: %w", err)
	}
	for i := range page {
		page[i].Usage = usage[keys[i]]
	}
	return nil
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
	if target == uuid.Nil() || target == caller {
		return caller, nil
	}
	if !principal.HasRole(apiutil.RolePlatformAdmin) {
		return uuid.Nil(), connect.Errorf(connect.CodePermissionDenied,
			"cross-tenant %s requires platform.admin", op)
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
		return h.dispatchEventTx(ctx, tx, b.TenantID, EventCollectionUpdated,
			CanonicalName(b.BackendID, b.BucketName, b.TenantID, b.Collection),
			map[string]any{
				"tenant_id":        b.TenantID.String(),
				"collection":       b.Collection,
				"backend_id":       b.BackendID,
				"bucket_name":      b.BucketName,
				"resource_version": b.ResourceVersion,
			})
	}); err != nil {
		return nil, h.answer(opUpdate, tenantID, args.Collection, err)
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
		return h.dispatchEventTx(ctx, tx, tenantID, EventCollectionDeleted,
			resourceName,
			map[string]any{
				"tenant_id":        tenantID.String(),
				"collection":       collection,
				"resource_version": expectedVersion,
			})
	}); err != nil {
		if errors.Is(err, ErrCollectionHasObjects) {
			err = h.notEmpty(ctx, tenantID, collection)
		}
		return h.answer(opDelete, tenantID, collection, err)
	}
	apiutil.StashResource(ctx, resourceName)
	return nil
}

func (h *Handler) ListCollections(ctx context.Context, args ListCollectionsArgs) ([]Collection, string, error) {
	callerTenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, "", err
	}
	if args.TenantID, err = listTenant(args, callerTenantID, principal); err != nil {
		return nil, "", err
	}
	// One tenant-scoped Cedar check up front; per-row filtering would
	// dominate pagination cost so we don't repeat it for every collection.
	// For cross-tenant listing we authorize against the caller's tenant
	// (the principal-tenant invariant the Cedar engine encodes).
	crossTenant := args.TenantID == uuid.Nil()
	authzTenant := args.TenantID
	if crossTenant {
		authzTenant = callerTenantID
	}
	if err := h.authorize(ctx, principal, authzTenant, "", cedar.ActionManageCollection); err != nil {
		return nil, "", err
	}
	if crossTenant {
		// The platform.admin gate above already passed: the platform's
		// collection catalogue, or every collection on one bucket. Scoping to
		// one tenant cannot express either — it returned only the admin's own
		// collections, so a catalogue of every tenant read as one tenant's.
		ctx = auth.WithCrossTenantRead(ctx)
	} else {
		ctx = auth.WithActingTenant(ctx, authzTenant)
	}
	page, next, err := h.listPage(ctx, args)
	if err != nil {
		return nil, "", h.answer(opList, authzTenant, "", err)
	}
	return page, next, nil
}

// listTenant is the tenant a listing reads, uuid.Nil for every tenant. An
// empty parent lists across tenants for a platform admin — every collection,
// or with a (backend, bucket) filter every one bound to that bucket. Anyone
// else asking without a tenant gets their own tenant's: without that pin any
// tenant could enumerate sibling tenants' collections. Naming another tenant
// is a platform admin's.
func listTenant(args ListCollectionsArgs, caller uuid.UUID, principal *auth.Principal) (uuid.UUID, error) {
	admin := principal.HasRole(apiutil.RolePlatformAdmin)
	byBucket := args.BackendID != "" && args.BucketName != ""
	switch {
	case admin:
		return args.TenantID, nil
	case args.TenantID == uuid.Nil() && !byBucket:
		return caller, nil
	case args.TenantID == caller:
		return caller, nil
	}
	return uuid.Nil(), connect.NewError(connect.CodePermissionDenied,
		"cross-tenant ListCollections requires platform.admin")
}

// listPage is one page of the listing args asks for: the store's page with
// the filter applied, and each collection's usage.
func (h *Handler) listPage(ctx context.Context, args ListCollectionsArgs) ([]Collection, string, error) {
	page, next, err := h.repo.List(ctx, args)
	if err != nil {
		return nil, "", err
	}
	page, err = celpkg.FilterPage(h.cel, celpkg.CollectionSchema, args.Filter, page, collectionRow)
	if err != nil {
		return nil, "", rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("filter: %w", err))
	}
	if err := h.withUsage(ctx, page); err != nil {
		return nil, "", err
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
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	if err := h.authorizeFull(ctx, p, tenantID, collection, backendID, bucketName, cedar.ActionBindCollectionToBucket); err != nil {
		return nil, err
	}
	// Write as the row's owner, as Update and Delete do: under row-level
	// security a platform admin's rebind of another tenant's collection
	// otherwise finds no row to change.
	ctx = auth.WithActingTenant(ctx, tenantID)
	// The repository types every refusal it can name — a stale version, a
	// public-collection rule, a bucket not granted or not registered, a
	// collection that is not there; what is left is the store failing.
	if err := h.repo.Rebind(ctx, tenantID, collection, backendID, bucketName, expectedVersion); err != nil {
		return nil, h.answer(opBind, tenantID, collection, fmt.Errorf("rebind: %w", err))
	}
	updated, err := h.repo.Get(ctx, tenantID, collection)
	if err != nil {
		return nil, h.answer(opBind, tenantID, collection, fmt.Errorf("read back: %w", err))
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
func (h *Handler) authorize(ctx context.Context, p *auth.Principal, tenantID uuid.UUID, collection string, action cedar.Action) error {
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
	collection, backendID, bucketName string, action cedar.Action,
) error {
	return apiutil.Decide(ctx, h.policy, apiutil.CedarPrincipalFor(p, tenantID), action, &cedar.Resource{
		TenantID:   tenantID,
		Collection: collection,
		BackendID:  backendID,
		BucketName: bucketName,
	}, cedar.RequestContext{})
}

// ErrCollectionHasObjects is returned when a Delete is blocked because
// the Collection still has rows in `objects` referencing it. The FK
// constraint is ON DELETE RESTRICT — operators must purge / move the
// objects first. Handler maps this to FAILED_PRECONDITION so the UI
// can surface a clear "remove the files first" prompt. DeleteCollection
// answers with a *CollectionNotEmptyError, which says what holds it.
var ErrCollectionHasObjects = errors.New("collection still holds objects")

// The ErrorInfo metadata keys of ERROR_REASON_COLLECTION_NOT_EMPTY, as
// common/v1/error_reason.proto documents them.
const (
	MetaLiveObjects    = "live_objects"
	MetaTrashedObjects = "trashed_objects"
)

// CollectionHolding is what keeps a collection from being deleted: its live
// objects, and those in its trash — which the foreign key counts the same.
type CollectionHolding struct {
	Live    int64
	Trashed int64
}

// CollectionNotEmptyError is ErrCollectionHasObjects with the counts that
// caused it. "has live objects" used to be the whole answer, which was wrong
// whenever the trash alone held the collection.
type CollectionNotEmptyError struct{ Holding CollectionHolding }

func (e *CollectionNotEmptyError) Error() string {
	h := e.Holding
	switch {
	case h.Trashed == 0:
		return fmt.Sprintf("collection still holds %s; delete them first", objectCount(h.Live))
	case h.Live == 0:
		return fmt.Sprintf("collection still holds %s in its trash; purge or restore them first", objectCount(h.Trashed))
	default:
		return fmt.Sprintf("collection still holds %s, %d of them in its trash; delete, purge or restore them first",
			objectCount(h.Live+h.Trashed), h.Trashed)
	}
}

func (e *CollectionNotEmptyError) Is(target error) bool { return target == ErrCollectionHasObjects }

// ErrorMetadata implements apiutil.MetadataError.
func (e *CollectionNotEmptyError) ErrorMetadata() map[string]string {
	return map[string]string{
		MetaLiveObjects:    strconv.FormatInt(e.Holding.Live, 10),
		MetaTrashedObjects: strconv.FormatInt(e.Holding.Trashed, 10),
	}
}

// objectCount is "1 object", "3 objects".
func objectCount(n int64) string {
	if n == 1 {
		return "1 object"
	}
	return fmt.Sprintf("%d objects", n)
}

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

// ErrCollectionNotFound is returned when the tenant has no collection by the
// name: on a read, and on a guarded write that finds none to change. A store
// that fails is neither: that is the server's fault.
var ErrCollectionNotFound = errors.New("collection not found")

// ErrVersionMismatch is returned when optimistic-concurrency control fails.
// Repositories should surface it so handlers can map to CodeAborted.
var ErrVersionMismatch = errors.New("resource_version mismatch")

// notEmpty says what holds a collection whose delete the foreign key refused.
// Counted after the refused transaction, which can run nothing more; a count
// that fails leaves the refusal as it was rather than hiding it.
func (h *Handler) notEmpty(ctx context.Context, tenantID uuid.UUID, collection string) error {
	holding, err := h.repo.Holding(ctx, tenantID, collection)
	if err != nil {
		h.log.Error("collection delete refused: its objects could not be counted",
			zap.String("tenant_id", tenantID.String()), zap.String("collection", collection), zap.Error(err))
		return ErrCollectionHasObjects
	}
	return &CollectionNotEmptyError{Holding: holding}
}

// The lifecycle events the collection RPCs record.
const (
	EventCollectionCreated = "paladin.collection.created"
	EventCollectionUpdated = "paladin.collection.updated"
	EventCollectionDeleted = "paladin.collection.deleted"
)

// The op each log line of the collection RPCs names.
const (
	opCreate = "create"
	opEnsure = "ensure"
	opGet    = "get"
	opList   = "list"
	opUpdate = "update"
	opDelete = "delete"
	opBind   = "bind"
)

// Register this package's sentinels with the central error→Connect-code
// mapper (ADR-0002). mapVersionErr now delegates to apiutil.MapError; the
// registry — not a per-handler if/else — decides the code.
func init() {
	apiutil.RegisterError(ErrVersionMismatch, connect.CodeAborted, commonv1.ErrorReason_ERROR_REASON_VERSION_CONFLICT)
	apiutil.RegisterError(ErrCollectionHasObjects, connect.CodeFailedPrecondition, commonv1.ErrorReason_ERROR_REASON_COLLECTION_NOT_EMPTY)
	apiutil.RegisterError(ErrCollectionExists, connect.CodeAlreadyExists, commonv1.ErrorReason_ERROR_REASON_COLLECTION_EXISTS)
	apiutil.RegisterError(ErrBucketNotFound, connect.CodeNotFound, commonv1.ErrorReason_ERROR_REASON_BUCKET_NOT_FOUND)
	apiutil.RegisterError(ErrCollectionNotFound, connect.CodeNotFound, commonv1.ErrorReason_ERROR_REASON_NOT_FOUND)
	// A stored Cedar policy that will not compile is a state of the data, not
	// a fault of the server. Registered here rather than in the cedar package
	// because apiutil's registry is the API layer's, and cedar sits below it.
	apiutil.RegisterError(cedar.ErrPolicyUnparseable, connect.CodeFailedPrecondition, commonv1.ErrorReason_ERROR_REASON_POLICY_UNPARSEABLE)
}

// admitVisibility settles a new collection's public settings (ADR-0027): a
// private one carries no cache_control, and a public one needs the
// ConfigurePublicRead action and gets a valid cache_control, the default when
// none was asked for. That the bucket matches is the schema's rule.
func (h *Handler) admitVisibility(ctx context.Context, principal *auth.Principal, args *CreateCollectionArgs) error {
	if !args.PublicRead {
		if args.CacheControl != "" {
			return apiutil.MapError(publicread.Rulef("cache_control is for a public collection"))
		}
		return nil
	}
	if err := h.authorizeFull(ctx, principal, args.TenantID, args.Collection, args.BackendID, args.BucketName, cedar.ActionConfigurePublicRead); err != nil {
		return err
	}
	cc, err := publicread.CacheControl(args.CacheControl)
	if err != nil {
		return apiutil.MapError(err)
	}
	args.CacheControl = cc
	return nil
}
