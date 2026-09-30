package iam

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
)

// countingSlugs resolves one slug and counts every lookup, so a test can tell
// a refusal that looked the slug up from one that did not.
type countingSlugs struct {
	slug    string
	id      uuid.UUID
	lookups int
}

func (c *countingSlugs) GetBySlug(_ context.Context, slug string) (tenanth.Tenant, error) {
	c.lookups++
	if slug != c.slug {
		return tenanth.Tenant{}, tenanth.ErrNotFound
	}
	return tenanth.Tenant{TenantID: c.id, Slug: slug}, nil
}

func asCaller(tenant uuid.UUID, slug string, roles ...string) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		TenantID: tenant, TenantSlug: slug, Roles: roles,
	})
}

// CreateUser and ListUsers take `tenants/{tenant_id_or_slug}`, as the proto
// documents; a slug used to be refused as an invalid UUID.
func TestUserRPCsResolveASlugParent(t *testing.T) {
	t.Parallel()

	own := uuid.New()
	other := uuid.New()
	cases := map[string]struct {
		ctx         context.Context
		slug        string
		wantTenant  uuid.UUID
		wantCode    connect.Code
		wantLookups int
	}{
		"the caller's own slug comes from its token": {
			ctx: asCaller(own, "acme", "tenant.admin"), slug: "acme",
			wantTenant: own, wantLookups: 0,
		},
		"a platform admin's slug is looked up": {
			ctx: asCaller(own, "platform", apiutil.RolePlatformAdmin), slug: "globex",
			wantTenant: other, wantLookups: 1,
		},
		"a platform admin's unknown slug is not found": {
			ctx: asCaller(own, "platform", apiutil.RolePlatformAdmin), slug: "nobody",
			wantCode: connect.CodeNotFound, wantLookups: 1,
		},
		// Refused before any lookup, so the answer cannot tell an existing
		// tenant from a missing one.
		"another tenant's slug is refused without a lookup": {
			ctx: asCaller(own, "acme", "tenant.admin"), slug: "globex",
			wantCode: connect.CodePermissionDenied, wantLookups: 0,
		},
		"a missing slug is refused the same way": {
			ctx: asCaller(own, "acme", "tenant.admin"), slug: "nobody",
			wantCode: connect.CodePermissionDenied, wantLookups: 0,
		},
		"no principal is refused": {
			ctx: context.Background(), slug: "globex",
			wantCode: connect.CodePermissionDenied, wantLookups: 0,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for rpc, call := range map[string]func(*UserServer, *listingUser) (uuid.UUID, error){
				"ListUsers": func(s *UserServer, h *listingUser) (uuid.UUID, error) {
					_, err := s.ListUsers(tc.ctx, connect.NewRequest(&pb.ListUsersRequest{Parent: "tenants/" + tc.slug}))
					return h.got.TenantID, err
				},
				"CreateUser": func(s *UserServer, h *listingUser) (uuid.UUID, error) {
					_, err := s.CreateUser(tc.ctx, connect.NewRequest(&pb.CreateUserRequest{Parent: "tenants/" + tc.slug, Subject: "u"}))
					return h.created.TenantID, err
				},
			} {
				slugs := &countingSlugs{slug: "globex", id: other}
				h := &listingUser{}
				got, err := call(&UserServer{H: h, tenants: slugs}, h)
				if tc.wantCode != 0 {
					if code := connect.CodeOf(err); code != tc.wantCode {
						t.Errorf("%s: code = %v, want %v (err: %v)", rpc, code, tc.wantCode, err)
					}
				} else if err != nil || got != tc.wantTenant {
					t.Errorf("%s: tenant = %v, err = %v; want %v", rpc, got, err, tc.wantTenant)
				}
				if slugs.lookups != tc.wantLookups {
					t.Errorf("%s: %d lookup(s), want %d", rpc, slugs.lookups, tc.wantLookups)
				}
			}
		})
	}
}
