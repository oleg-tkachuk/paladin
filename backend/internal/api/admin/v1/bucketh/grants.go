package bucketh

import (
	"context"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// Grants (ADR-0032): every bucket has one owner, and the owner lets other
// tenants bind collections to it by granting the bucket to them. Writing one
// is managing the bucket — the owner's ManageBucket — and reading them is
// reading it.
//
// Granting is open to platform.tenant-provisioner as well (ADR-0011): letting
// a tenant it provisions into the pool it is provisioned on is the same
// decision as creating that tenant with a default binding there, which the
// role makes already. Revoking takes something away and stays bucket-admin.

// grantRoles may grant a bucket; manageRoles may revoke and read grants.
var (
	grantRoles  = []string{apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin, apiutil.RoleTenantProvisioner}
	manageRoles = []string{apiutil.RolePlatformAdmin, apiutil.RoleBucketAdmin}
)

// GrantBucket lets tenantID bind collections to the bucket. Idempotent: a
// tenant holding a grant keeps it, with its first granter and time. Granting
// the owner is refused: it binds already, and a grant would outlive a move.
func (h *Handler) GrantBucket(ctx context.Context, backendID, bucketName string, tenantID uuid.UUID) (admindomain.BucketGrant, error) {
	stored, err := h.grantable(ctx, backendID, bucketName, cedar.ActionManageBucket, grantRoles)
	if err != nil {
		return admindomain.BucketGrant{}, err
	}
	if tenantID == stored.OwnerTenantID {
		return admindomain.BucketGrant{}, connect.NewError(connect.CodeInvalidArgument,
			"the tenant owns the bucket; a grant is for another tenant")
	}
	g, err := h.grants.Grant(ctx, backendID, bucketName, tenantID, grantedBy(ctx))
	if err != nil {
		return admindomain.BucketGrant{}, apiutil.MapError(err)
	}
	return g, nil
}

// RevokeBucketGrant takes tenantID's grant back. Refused with
// ErrBucketGrantInUse while that tenant still has collections in the bucket.
func (h *Handler) RevokeBucketGrant(ctx context.Context, backendID, bucketName string, tenantID uuid.UUID) error {
	if _, err := h.grantable(ctx, backendID, bucketName, cedar.ActionManageBucket, manageRoles); err != nil {
		return err
	}
	// Whether the tenant still has collections there is a question about
	// rows under row-level security; asked across tenants, or it would see
	// none and revoke a grant they still stand on.
	if err := h.grants.RevokeGrant(auth.WithCrossTenantRead(ctx), backendID, bucketName, tenantID); err != nil {
		return apiutil.MapError(err)
	}
	return nil
}

// ListBucketGrants returns the bucket's grants, oldest first.
func (h *Handler) ListBucketGrants(ctx context.Context, backendID, bucketName string) ([]admindomain.BucketGrant, error) {
	if _, err := h.grantable(ctx, backendID, bucketName, cedar.ActionReadBucket, manageRoles); err != nil {
		return nil, err
	}
	grants, err := h.grants.ListGrants(ctx, backendID, bucketName)
	if err != nil {
		return nil, apiutil.MapError(err)
	}
	return grants, nil
}

// grantable gates a grant operation: one of roles, the bucket as stored, and
// action on it with its owner.
func (h *Handler) grantable(ctx context.Context, backendID, bucketName string, action cedar.Action, roles []string) (admindomain.Bucket, error) {
	if err := apiutil.RequireAnyRole(ctx, roles...); err != nil {
		return admindomain.Bucket{}, err
	}
	if h.grants == nil {
		return admindomain.Bucket{}, connect.NewError(connect.CodeUnavailable, "bucket grants not wired")
	}
	stored, err := h.repo.Get(ctx, backendID, bucketName)
	if err != nil {
		return admindomain.Bucket{}, apiutil.MapError(err)
	}
	if err := h.authorize(ctx, action, backendID, bucketName, stored.OwnerTenantID); err != nil {
		return admindomain.Bucket{}, err
	}
	return stored, nil
}

// grantedBy is who a grant is recorded as made by: the caller's subject.
func grantedBy(ctx context.Context) string {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil || p == nil {
		return ""
	}
	return p.Subject
}
