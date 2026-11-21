// Package storagebootstrap implements the DATA-plane StorageBootstrapService:
// self-service storage provisioning for a tenant using its EXISTING data-plane
// API token (aud=data), with no platform-admin credential.
//
// The single RPC, EnsureTenantStorage, idempotently ensures a bucket exists —
// created owned by the caller's tenant, or an existing one that tenant owns or
// holds a grant on (ADR-0032) — and that each requested object-key is bound to
// it under the caller's tenant. The core least-privilege guarantee: the tenant is ALWAYS
// resolved from the request principal (auth.TenantFromContext), never from the
// request body, so a caller can only ever provision storage for its OWN
// tenant. A single Cedar action — EnsureTenantStorage — authorizes the whole
// bundle; the bucket-ensure and object-key-ensure paths it delegates to are
// pre-authorized and therefore skip their own role/Cedar gates.
package storagebootstraph

import (
	"context"
	"errors"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/bucketh"
	objectkey "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/collectionh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// BucketEnsurer idempotently ensures the physical Paladin bucket exists, reusing
// the admin BucketService create path (physical garage provisioning + row).
// *bucketh.Handler satisfies it; it is pre-authorized by this package's Cedar
// gate and performs no role/Cedar check of its own.
type BucketEnsurer interface {
	EnsureBucket(ctx context.Context, in bucketh.CreateBucketInput) (*admindomain.Bucket, bool, error)
}

// CollectionEnsurer idempotently binds an object-key to (backend, bucket) under
// the caller's tenant. *collection.Handler satisfies it; it self-scopes to the
// caller's tenant and is pre-authorized by this package's Cedar gate.
type CollectionEnsurer interface {
	EnsureCollection(ctx context.Context, args objectkey.CreateCollectionArgs) (bool, error)
}

// BackendChecker reports whether a storage backend exists (and is enabled).
// admindomain.BucketRepository (repos.BucketV2) satisfies it; a tenant must
// not create backends, so an unknown backend is rejected upstream of any
// mutation. Returns admindomain.ErrNotFound when the backend id is unknown.
type BackendChecker interface {
	BackendEnabled(ctx context.Context, backendID string) (bool, error)
}

// Result is the report EnsureTenantStorage returns.
type Result struct {
	BucketCreated       bool
	CollectionsCreated  []string
	CollectionsExisting []string
}

type Handler struct {
	buckets     BucketEnsurer
	collections CollectionEnsurer
	backends    BackendChecker
	policy      cedar.Authorizer
	log         *zap.Logger
}

// NewHandler wires the self-provisioning handler. policy is required — a nil
// authorizer would leave the operation ungated.
func NewHandler(buckets BucketEnsurer, collections CollectionEnsurer, backends BackendChecker, policy cedar.Authorizer) *Handler {
	if policy == nil {
		panic("storagebootstrap: policy authorizer is required")
	}
	return &Handler{buckets: buckets, collections: collections, backends: backends, policy: policy, log: zap.NewNop()}
}

// SetLogger sets where the handler says what it provisioned and what it
// refused. Unset, it says nothing.
func (h *Handler) SetLogger(l *zap.Logger) {
	if l != nil {
		h.log = l
	}
}

// requireTarget refuses a request that names no backend or no bucket.
func requireTarget(backendID, bucket string) error {
	if backendID == "" {
		return connect.NewError(connect.CodeInvalidArgument, "backend_id is required")
	}
	if bucket == "" {
		return connect.NewError(connect.CodeInvalidArgument, "bucket is required")
	}
	return nil
}

// checkBackend refuses a backend that is not there or is disabled — a tenant
// may not create backends — and answers a failing store as internal. Each
// is logged with the tenant and backend.
func (h *Handler) checkBackend(ctx context.Context, tenantID uuid.UUID, backendID string) error {
	fields := []zap.Field{zap.String("tenant_id", tenantID.String()), zap.String("backend_id", backendID)}
	switch enabled, err := h.backends.BackendEnabled(ctx, backendID); {
	case errors.Is(err, admindomain.ErrNotFound):
		h.log.Warn("tenant storage refused: unknown backend", fields...)
		return connect.Errorf(connect.CodeFailedPrecondition, "unknown storage backend %q", backendID)
	case err != nil:
		h.log.Error("tenant storage failed: backend lookup", append(fields, zap.Error(err))...)
		return connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	case !enabled:
		h.log.Warn("tenant storage refused: backend disabled", fields...)
		return connect.Errorf(connect.CodeFailedPrecondition, "storage backend %q is disabled", backendID)
	}
	return nil
}

// ensureCollections ensures each named object-key on (backendID, bucket),
// self-scoped to the caller's tenant, and reports each as created or already
// there in res. An empty name is skipped; the first failure stops the run.
func (h *Handler) ensureCollections(ctx context.Context, backendID, bucket string, collections []string, res *Result) error {
	for _, key := range collections {
		if key == "" {
			continue
		}
		created, err := h.collections.EnsureCollection(ctx, objectkey.CreateCollectionArgs{
			Collection: key,
			BackendID:  backendID,
			BucketName: bucket,
			// TenantID intentionally left zero — EnsureCollection forces the
			// caller's tenant.
		})
		if err != nil {
			return err
		}
		if created {
			res.CollectionsCreated = append(res.CollectionsCreated, key)
		} else {
			res.CollectionsExisting = append(res.CollectionsExisting, key)
		}
	}
	return nil
}

// EnsureTenantStorage idempotently ensures the bucket + the requested
// object-keys exist under the CALLER's tenant. Safe to call on every startup:
// an already-provisioned tenant/bucket/keys yields an all-no-op success.
func (h *Handler) EnsureTenantStorage(ctx context.Context, backendID, bucket string, collections []string) (*Result, error) {
	// Tenant comes from the principal, NEVER the request — the operation is
	// always scoped to the caller's own tenant.
	tenantID, err := auth.TenantFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	if err := requireTarget(backendID, bucket); err != nil {
		return nil, err
	}

	// A capability can authenticate a data-plane call on its own, and the
	// Cedar permit below is tenant equality — which any capability of this
	// tenant satisfies. Provisioning buckets and collections is tenant
	// administration, so it takes manage; no resource URI bounds it, so a
	// resource-restricted capability is refused.
	if err := auth.AssertCapabilityOp(ctx, limes.OpManage, ""); err != nil {
		return nil, err
	}
	// Authorize the whole self-provision bundle against the caller's own tenant
	// (the built-in tenant_id-equality permit; see authorize).
	if err := h.authorize(ctx, p, tenantID); err != nil {
		return nil, err
	}

	// The backend must already exist — a tenant may not create backends.
	if err := h.checkBackend(ctx, tenantID, backendID); err != nil {
		return nil, err
	}

	// Ensure the bucket, owned by the caller's tenant (ADR-0032). An existing
	// bucket it neither owns nor holds a grant on is refused here — a tenant
	// no longer takes another's bucket by naming it.
	// provision_on_backend=true reuses the admin physical-provisioning path.
	_, bucketCreated, err := h.buckets.EnsureBucket(ctx, bucketh.CreateBucketInput{
		Bucket: admindomain.Bucket{
			BackendID:     backendID,
			BucketName:    bucket,
			OwnerTenantID: tenantID,
		},
		ProvisionOnBackend: true,
	})
	if err != nil {
		return nil, err
	}

	res := &Result{BucketCreated: bucketCreated}
	if err := h.ensureCollections(ctx, backendID, bucket, collections, res); err != nil {
		return nil, err
	}
	h.log.Info("tenant storage ensured",
		zap.String("tenant_id", tenantID.String()),
		zap.String("backend_id", backendID),
		zap.String("bucket_name", bucket),
		zap.Bool("bucket_created", res.BucketCreated),
		zap.Strings("collections_created", res.CollectionsCreated),
		zap.Strings("collections_existing", res.CollectionsExisting),
		zap.String("actor", p.Subject))
	return res, nil
}

// authorize evaluates the EnsureTenantStorage Cedar action against the caller's
// OWN tenant (self-scoped), via the built-in tenant_id-equality permit.
func (h *Handler) authorize(ctx context.Context, p *auth.Principal, tenantID uuid.UUID) error {
	// The resource is the caller's own TENANT (not the bucket): EnsureTenantStorage
	// is a tenant-level self-service provisioning op, authorized by the built-in
	// tenant_id-equality permit (see cedar.builtinPolicy). Modeling it as the
	// Tenant entity exposes resource.tenant_id for that check and keeps the grant
	// universal (no per-tenant policy seed). A scoped PAT is still confined by the
	// scope-enforcement built-in at tenant granularity (scope_keys = tenant:<id>).
	return apiutil.Decide(ctx, h.policy, apiutil.CedarPrincipal(p), cedar.ActionEnsureTenantStorage,
		&cedar.Resource{TenantID: tenantID}, cedar.RequestContext{})
}
