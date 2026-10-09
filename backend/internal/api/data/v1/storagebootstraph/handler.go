// Package storagebootstrap implements the DATA-plane StorageBootstrapService:
// self-service storage provisioning for a tenant using its EXISTING data-plane
// API token (aud=data), with no platform-admin credential.
//
// The single RPC, EnsureTenantStorage, idempotently ensures a shared Paladin
// bucket exists and that each requested object-key is bound to it under the
// caller's tenant. The core least-privilege guarantee: the tenant is ALWAYS
// resolved from the request principal (auth.TenantFromContext), never from the
// request body, so a caller can only ever provision storage for its OWN
// tenant. A single Cedar action — EnsureTenantStorage — authorizes the whole
// bundle; the bucket-ensure and object-key-ensure paths it delegates to are
// pre-authorized and therefore skip their own role/Cedar gates.
package storagebootstraph

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

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
}

// NewHandler wires the self-provisioning handler. policy is required — a nil
// authorizer would leave the operation ungated.
func NewHandler(buckets BucketEnsurer, collections CollectionEnsurer, backends BackendChecker, policy cedar.Authorizer) *Handler {
	if policy == nil {
		panic("storagebootstrap: policy authorizer is required")
	}
	return &Handler{buckets: buckets, collections: collections, backends: backends, policy: policy}
}

// EnsureTenantStorage idempotently ensures the shared bucket + the requested
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
	if backendID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "backend_id is required")
	}
	if bucket == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "bucket is required")
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
	switch enabled, err := h.backends.BackendEnabled(ctx, backendID); {
	case errors.Is(err, admindomain.ErrNotFound):
		return nil, connect.Errorf(connect.CodeFailedPrecondition,
			"unknown storage backend %q", backendID)
	case err != nil:
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	case !enabled:
		return nil, connect.Errorf(connect.CodeFailedPrecondition,
			"storage backend %q is disabled", backendID)
	}

	// Ensure the shared bucket. OwnerTenantID is left empty (uuid.Nil) so the
	// bucket is SHARED — matching how the shared consumer bucket is modeled today.
	// provision_on_backend=true reuses the admin physical-provisioning path.
	_, bucketCreated, err := h.buckets.EnsureBucket(ctx, bucketh.CreateBucketInput{
		Bucket: admindomain.Bucket{
			BackendID:  backendID,
			BucketName: bucket,
		},
		ProvisionOnBackend: true,
	})
	if err != nil {
		return nil, err
	}

	res := &Result{BucketCreated: bucketCreated}
	// Ensure each object-key, self-scoped to the caller's tenant.
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
			return nil, err
		}
		if created {
			res.CollectionsCreated = append(res.CollectionsCreated, key)
		} else {
			res.CollectionsExisting = append(res.CollectionsExisting, key)
		}
	}
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
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipal(p),
		cedar.ActionEnsureTenantStorage,
		&cedar.Resource{TenantID: tenantID},
		cedar.RequestContext{Now: time.Now()},
	)
	if err != nil {
		return apiutil.MapError(fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied, "denied by policy")
	}
	return nil
}
