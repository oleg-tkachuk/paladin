// Package objectKey implements the ObjectKeyService business logic.
//
// Buckets are logical namespaces mapped onto a physical storage backend.
// Create/Update/Delete operations go through Cedar authorization. Delete is
// restricted if any non-DELETED objects still reference the objectKey (FK).
package objectkey

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

	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/internal/worker"
)

// EventProducer mirrors the seam used by tenanth / bucketh — narrow
// interface so handler tests can stub the dispatcher. *worker.Dispatcher
// implements it. nil-safe via dispatchEvent's guard.
type EventProducer interface {
	Dispatch(ctx context.Context, tenantID string, evt worker.Event) (int, error)
	// DispatchTx fans the event out on the caller's tx so the outbox rows
	// commit atomically with the object_key mutation (ADR-0003).
	DispatchTx(ctx context.Context, tx pgx.Tx, tenantID string, evt worker.Event) (int, error)
}

type ObjectKey struct {
	TenantID        uuid.UUID
	ObjectKey       string
	DisplayName     string
	BackendID       string
	BucketName      string
	CedarPolicy     string
	LifecycleRules  []byte // JSONB bytes; parsed by caller if needed
	ResourceVersion int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type ObjectKeyStats struct {
	ObjectCountAvailable int64
	ObjectCountPending   int64
	ObjectCountDeleted   int64
	SizeBytesAvailable   int64
}

type CreateObjectKeyArgs struct {
	TenantID       uuid.UUID
	ObjectKey      string
	DisplayName    string
	BackendID      string
	BucketName     string
	CedarPolicy    string
	LifecycleRules []byte
}

type UpdateObjectKeyArgs struct {
	TenantID        uuid.UUID
	ObjectKey       string
	ExpectedVersion int64
	DisplayName     *string
	CedarPolicy     *string
	LifecycleRules  []byte
}

type ListObjectKeysArgs struct {
	TenantID  uuid.UUID
	PageSize  int32
	PageToken string
	// BackendID + BucketName are optional server-side filters. When both
	// are set, only ObjectKeys bound to that (backend, bucket) pair are
	// returned. Used by the storage-first UI browser to avoid pulling
	// every OK platform-wide just to client-filter a handful per bucket.
	BackendID  string
	BucketName string
}

type Repository interface {
	Create(ctx context.Context, args CreateObjectKeyArgs) (ObjectKey, error)
	Get(ctx context.Context, tenantID uuid.UUID, objectKey string) (ObjectKey, error)
	Update(ctx context.Context, args UpdateObjectKeyArgs) (ObjectKey, error)
	Delete(ctx context.Context, tenantID uuid.UUID, objectKey string, expectedVersion int64) error
	List(ctx context.Context, args ListObjectKeysArgs) ([]ObjectKey, string, error)
	Stats(ctx context.Context, tenantID uuid.UUID, objectKey string) (ObjectKeyStats, error)
	// Rebind atomically swaps the (backend_id, bucket_name) target. DB
	// trigger enforces tenancy on single-tenant buckets.
	Rebind(ctx context.Context, tenantID uuid.UUID, objectKey, backendID, bucketName string, expectedVersion int64) error

	// RunInTx + the *Tx variants are the ADR-0003 seam: mutation + outbox
	// fan-out on one tx so a crash can't leave a committed change without
	// its event.
	RunInTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error
	CreateTx(ctx context.Context, tx pgx.Tx, args CreateObjectKeyArgs) (ObjectKey, error)
	UpdateTx(ctx context.Context, tx pgx.Tx, args UpdateObjectKeyArgs) (ObjectKey, error)
	DeleteTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, objectKey string, expectedVersion int64) error
}

type Handler struct {
	repo           Repository
	policy         cedar.Authorizer
	defaultBackend string

	events EventProducer
	log    *zap.Logger
}

func NewHandler(repo Repository, policy cedar.Authorizer, defaultBackend string) *Handler {
	return &Handler{repo: repo, policy: policy, defaultBackend: defaultBackend, log: zap.NewNop()}
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
		h.log.Warn("object_key event fan-out failed",
			zap.String("event_type", eventType),
			zap.String("tenant_id", tenantID.String()),
			zap.String("resource", resourceName),
			zap.Error(err),
		)
		return
	}
	h.log.Debug("object_key event queued",
		zap.String("event_type", eventType),
		zap.String("tenant_id", tenantID.String()),
		zap.Int("subscriptions_matched", queued),
	)
}

// dispatchEventTx fans the event out on the caller's tx so the outbox rows
// commit atomically with the object_key mutation (ADR-0003). Returns the
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

// objectKeyResourceName — kept as the C-shape emitter for callers
// that don't have (backend, bucket) in scope. Phase 1 prefers
// CanonicalName when the full tuple is available (event payloads
// after Create / Update / Delete read the row's backend/bucket and
// can canonicalize). Plain Delete with no row read still uses C.
func objectKeyResourceName(tenantID uuid.UUID, key string) string {
	return TenantPathName(tenantID, key)
}

func (h *Handler) CreateObjectKey(ctx context.Context, args CreateObjectKeyArgs) (*ObjectKey, error) {
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
		if !principal.HasRole(apiutil.RolePlatformAdmin) {
			return nil, connect.NewError(connect.CodePermissionDenied,
				errors.New("cross-tenant CreateObjectKey requires platform.admin"))
		}
	}
	// Fall back to the configured default backend when the caller omits it.
	// The backend name is a FK to storage_backends.id, so an empty string
	// would fail the constraint.
	if args.BackendID == "" {
		args.BackendID = h.defaultBackend
	}
	if err := h.authorizeFull(ctx, principal, args.TenantID, args.ObjectKey, args.BackendID, args.BucketName, cedar.ActionManageObjectKey); err != nil {
		return nil, err
	}
	// Create + paladin.object_key.created in one tx (ADR-0003).
	var b ObjectKey
	if err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var e error
		b, e = h.repo.CreateTx(ctx, tx, args)
		if e != nil {
			return e
		}
		return h.dispatchEventTx(ctx, tx, b.TenantID, "paladin.object_key.created",
			CanonicalName(b.BackendID, b.BucketName, b.TenantID, b.ObjectKey),
			map[string]any{
				"tenant_id":    b.TenantID.String(),
				"object_key":   b.ObjectKey,
				"display_name": b.DisplayName,
				"backend_id":   b.BackendID,
				"bucket_name":  b.BucketName,
			})
	}); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create objectKey: %w", err))
	}
	// writes into the slot the audit mw installed
	apiutil.StashResource(ctx, CanonicalName(b.BackendID, b.BucketName, b.TenantID, b.ObjectKey))
	return &b, nil
}

func (h *Handler) GetObjectKey(ctx context.Context, objectKey string) (*ObjectKey, error) {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, principal, tenantID, objectKey, cedar.ActionManageObjectKey); err != nil {
		return nil, err
	}
	b, err := h.repo.Get(ctx, tenantID, objectKey)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return &b, nil
}

func (h *Handler) UpdateObjectKey(ctx context.Context, args UpdateObjectKeyArgs) (*ObjectKey, error) {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	args.TenantID = tenantID
	if err := h.authorize(ctx, principal, tenantID, args.ObjectKey, cedar.ActionManageObjectKey); err != nil {
		return nil, err
	}
	// Update + paladin.object_key.updated in one tx (ADR-0003). UpdateTx reads
	// the post-update row back on the same tx (for resource_version).
	var b ObjectKey
	if err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var e error
		b, e = h.repo.UpdateTx(ctx, tx, args)
		if e != nil {
			return e
		}
		return h.dispatchEventTx(ctx, tx, b.TenantID, "paladin.object_key.updated",
			CanonicalName(b.BackendID, b.BucketName, b.TenantID, b.ObjectKey),
			map[string]any{
				"tenant_id":        b.TenantID.String(),
				"object_key":       b.ObjectKey,
				"backend_id":       b.BackendID,
				"bucket_name":      b.BucketName,
				"resource_version": b.ResourceVersion,
			})
	}); err != nil {
		return nil, mapVersionErr(err)
	}
	apiutil.StashResource(ctx, CanonicalName(b.BackendID, b.BucketName, b.TenantID, b.ObjectKey))
	return &b, nil
}

func (h *Handler) DeleteObjectKey(ctx context.Context, objectKey string, expectedVersion int64) error {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return err
	}
	if err := h.authorize(ctx, principal, tenantID, objectKey, cedar.ActionManageObjectKey); err != nil {
		return err
	}
	// Read the row before delete so the event payload can carry the
	// canonical resource name (which needs backend + bucket). Best-
	// effort: if Get fails we fall back to the C-shape resource name —
	// the delete itself still runs through the OCC guard below.
	pre, getErr := h.repo.Get(ctx, tenantID, objectKey)
	resourceName := objectKeyResourceName(tenantID, objectKey)
	if getErr == nil {
		resourceName = CanonicalName(pre.BackendID, pre.BucketName, tenantID, objectKey)
	}
	// Delete + paladin.object_key.deleted in one tx (ADR-0003). The resource
	// name comes from the pre-read above (best-effort; the OCC guard still
	// runs inside the tx).
	if err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if e := h.repo.DeleteTx(ctx, tx, tenantID, objectKey, expectedVersion); e != nil {
			return e
		}
		return h.dispatchEventTx(ctx, tx, tenantID, "paladin.object_key.deleted",
			resourceName,
			map[string]any{
				"tenant_id":        tenantID.String(),
				"object_key":       objectKey,
				"resource_version": expectedVersion,
			})
	}); err != nil {
		return mapVersionErr(err)
	}
	apiutil.StashResource(ctx, resourceName)
	return nil
}

func (h *Handler) ListObjectKeys(ctx context.Context, args ListObjectKeysArgs) ([]ObjectKey, string, error) {
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
				errors.New("cross-tenant ListObjectKeys requires platform.admin"))
		}
	} else if args.TenantID != callerTenantID {
		if !principal.HasRole(apiutil.RolePlatformAdmin) {
			return nil, "", connect.NewError(connect.CodePermissionDenied,
				errors.New("cross-tenant ListObjectKeys requires platform.admin"))
		}
	}
	// One tenant-scoped Cedar check up front; per-row filtering would
	// dominate pagination cost so we don't repeat it for every objectKey.
	// For cross-tenant listing we authorize against the caller's tenant
	// (the principal-tenant invariant the Cedar engine encodes).
	authzTenant := args.TenantID
	if authzTenant == uuid.Nil {
		authzTenant = callerTenantID
	}
	if err := h.authorize(ctx, principal, authzTenant, "", cedar.ActionManageObjectKey); err != nil {
		return nil, "", err
	}
	return h.repo.List(ctx, args)
}

func (h *Handler) GetObjectKeyStats(ctx context.Context, objectKey string) (*ObjectKeyStats, error) {
	tenantID, principal, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, principal, tenantID, objectKey, cedar.ActionManageObjectKey); err != nil {
		return nil, err
	}
	s, err := h.repo.Stats(ctx, tenantID, objectKey)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &s, nil
}

// BindObjectKeyToBucket rebinds the namespace to a different bucket. Cedar
// authorization uses ActionBindObjectKeyToBucket on the namespace.
func (h *Handler) BindObjectKeyToBucket(ctx context.Context, objectKey, bucket string, expectedVersion int64) (*ObjectKey, error) {
	tenantID, p, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	backendID, bucketName, err := splitBucketResourceName(bucket)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := h.authorizeFull(ctx, p, tenantID, objectKey, backendID, bucketName, cedar.ActionBindObjectKeyToBucket); err != nil {
		return nil, err
	}
	if err := h.repo.Rebind(ctx, tenantID, objectKey, backendID, bucketName, expectedVersion); err != nil {
		if errors.Is(err, ErrVersionMismatch) {
			return nil, connect.NewError(connect.CodeAborted, err)
		}
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("rebind: %w", err))
	}
	updated, err := h.repo.Get(ctx, tenantID, objectKey)
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

func (h *Handler) authorize(ctx context.Context, p *auth.Principal, tenantID uuid.UUID, objectKey, action string) error {
	return h.authorizeFull(ctx, p, tenantID, objectKey, "", "", action)
}

// authorizeFull is the bucket-aware variant that passes the bucket binding
// to Cedar so the engine emits the full ObjectKey←Bucket←StorageBackend
// hierarchy. Use whenever the caller has the binding in hand (Create,
// BindObjectKeyToBucket, post-Get on Update).
func (h *Handler) authorizeFull(
	ctx context.Context,
	p *auth.Principal,
	tenantID uuid.UUID,
	objectKey, backendID, bucketName, action string,
) error {
	decision, err := h.policy.IsAuthorized(ctx,
		&cedar.Principal{Subject: p.Subject, TenantID: tenantID, TenantSlug: p.TenantSlug, Roles: p.Roles, Scopes: apiutil.ScopeStrings(p.Scopes)},
		action,
		&cedar.Resource{
			TenantID:   tenantID,
			ObjectKey:  objectKey,
			BackendID:  backendID,
			BucketName: bucketName,
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

// ErrObjectKeyHasObjects is returned when a Delete is blocked because
// the ObjectKey still has rows in `objects` referencing it. The FK
// constraint is ON DELETE RESTRICT — operators must purge / move the
// objects first. Handler maps this to FAILED_PRECONDITION so the UI
// can surface a clear "remove the files first" prompt.
var ErrObjectKeyHasObjects = errors.New(
	"object_key has live objects; remove or move them before deleting")

// ErrVersionMismatch is returned when optimistic-concurrency control fails.
// Repositories should surface it so handlers can map to CodeAborted.
var ErrVersionMismatch = errors.New("resource_version mismatch")

func mapVersionErr(err error) error {
	if errors.Is(err, ErrVersionMismatch) {
		return connect.NewError(connect.CodeAborted, err)
	}
	if errors.Is(err, ErrObjectKeyHasObjects) {
		return connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewError(connect.CodeInternal, err)
}
