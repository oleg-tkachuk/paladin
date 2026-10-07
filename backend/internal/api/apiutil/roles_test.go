package apiutil

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

func ctxWithRoles(roles ...string) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject: "u-1",
		Roles:   roles,
	})
}

func TestRequireRole(t *testing.T) {
	t.Run("no principal -> Unauthenticated", func(t *testing.T) {
		err := RequireRole(context.Background(), RoleTenantAdmin)
		if connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("code = %v, want Unauthenticated (err=%v)", connect.CodeOf(err), err)
		}
	})
	t.Run("missing role -> PermissionDenied", func(t *testing.T) {
		err := RequireRole(ctxWithRoles(RoleTenantUser), RoleTenantAdmin)
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("code = %v, want PermissionDenied", connect.CodeOf(err))
		}
	})
	t.Run("has role -> nil", func(t *testing.T) {
		if err := RequireRole(ctxWithRoles(RoleTenantAdmin), RoleTenantAdmin); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})
}

func TestRequireAnyRole(t *testing.T) {
	t.Run("no principal -> Unauthenticated", func(t *testing.T) {
		err := RequireAnyRole(context.Background(), RoleTenantAdmin, RolePlatformAdmin)
		if connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("code = %v, want Unauthenticated", connect.CodeOf(err))
		}
	})
	t.Run("none of the roles -> PermissionDenied", func(t *testing.T) {
		err := RequireAnyRole(ctxWithRoles(RoleTenantUser), RoleTenantAdmin, RolePlatformAdmin)
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("code = %v, want PermissionDenied", connect.CodeOf(err))
		}
	})
	t.Run("holds one of them -> nil", func(t *testing.T) {
		err := RequireAnyRole(ctxWithRoles(RolePlatformAdmin), RoleTenantAdmin, RolePlatformAdmin)
		if err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})
}

func TestHasRole(t *testing.T) {
	if HasRole(context.Background(), RoleTenantAdmin) {
		t.Error("HasRole with no principal should be false")
	}
	if !HasRole(ctxWithRoles(RoleTenantAdmin, RoleBucketAdmin), RoleBucketAdmin) {
		t.Error("HasRole should be true when the principal holds the role")
	}
	if HasRole(ctxWithRoles(RoleTenantUser), RoleTenantAdmin) {
		t.Error("HasRole should be false when the principal lacks the role")
	}
}
