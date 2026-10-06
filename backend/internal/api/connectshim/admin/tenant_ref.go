package admin

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
)

// TenantResolver is the slug → id lookup a server needs to accept the slug
// form of "tenants/{tenant_id_or_slug}", kept as an interface so a server
// does not depend on the whole tenant handler. GetTenantBySlug runs the tenant
// handler's authz, so a slug never resolves a tenant the caller cannot see.
type TenantResolver interface {
	GetTenantBySlug(ctx context.Context, slug string) (*tenanth.Tenant, error)
}

// errSlugNeedsResolver is returned for a slug where no resolver is wired, rather
// than guessing a scope.
var errSlugNeedsResolver = errors.New("tenant must be named by its uuid here")

// resolveTenantName maps "tenants/{tenant_id_or_slug}" to a tenant id. A
// malformed name is InvalidArgument; a resolver error passes through unchanged.
func resolveTenantName(ctx context.Context, tenants TenantResolver, name string) (uuid.UUID, error) {
	ref, err := apiutil.ParseTenantNameRef(name)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return resolveTenantRef(ctx, tenants, ref)
}

// resolveTenantRef turns a parsed tenant reference into its id, looking the
// slug form up through tenants.
func resolveTenantRef(ctx context.Context, tenants TenantResolver, ref apiutil.TenantRef) (uuid.UUID, error) {
	if ref.HasID() {
		return ref.ID, nil
	}
	if tenants == nil {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument, errSlugNeedsResolver)
	}
	t, err := tenants.GetTenantBySlug(ctx, ref.Slug)
	if err != nil {
		return uuid.Nil, err
	}
	return t.TenantID, nil
}
