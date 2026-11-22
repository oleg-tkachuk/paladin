package apitokenh

import (
	"slices"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// Cedar authorises a mint on the caller's own tenant; the cross-tenant gate is
// what admits a platform admin to the tenant it mints for, so the audit row is
// filed in that tenant's trail. A caller the gate refuses admits nothing.
func TestCreateAdmitsTheTenantItMintsFor(t *testing.T) {
	target := uuid.New()
	req := func() *adminv1.APITokenServiceCreateRequest {
		return &adminv1.APITokenServiceCreateRequest{
			RateLimit: unlimitedRate(),
			Parent:    "tenants/" + target.String(), DisplayName: "svc", Audience: []string{"data"},
		}
	}
	for _, tc := range []struct {
		role     string
		admitted bool
	}{
		{"platform.admin", true},
		{"tenant.admin", false},
	} {
		t.Run(tc.role, func(t *testing.T) {
			h := newHandler(&stubIssuer{}, &fakeStore{}, allowAuthorizer{})
			ctx := apiutil.WithAdmissionSlot(ctxAsTenant(uuid.New(), tc.role))
			_, _ = h.Create(ctx, req())
			if got := slices.Contains(apiutil.AdmittedTenants(ctx), target); got != tc.admitted {
				t.Errorf("admitted to %s: %t, want %t", target, got, tc.admitted)
			}
		})
	}
}

// The guard of every tenant-addressed RPC admits the tenant it let the
// caller cross into, and nothing when it refuses.
func TestScopeToTenantAdmits(t *testing.T) {
	target := uuid.New()
	for _, tc := range []struct {
		role     string
		admitted bool
	}{
		{"platform.admin", true},
		{"tenant.admin", false},
	} {
		t.Run(tc.role, func(t *testing.T) {
			ctx := apiutil.WithAdmissionSlot(ctxAsTenant(uuid.New(), tc.role))
			p, err := auth.PrincipalFromContext(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = scopeToTenant(ctx, p, target, "revoke")
			if got := slices.Contains(apiutil.AdmittedTenants(ctx), target); got != tc.admitted {
				t.Errorf("admitted to %s: %t, want %t", target, got, tc.admitted)
			}
		})
	}
}
