package capabilityh

import (
	"slices"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/limes"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar/cedartest"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// A capability issuer spans tenants by its role gate, not by Cedar, which
// authorises the action on the caller's own tenant. The gate admits the
// tenant it let the caller into, so the audit row of an issue or a run
// revocation there is filed in that tenant's trail; a refused caller admits
// nothing.
func TestCapabilityIssuerAdmitsTheTenantItActsIn(t *testing.T) {
	other := uuid.New()

	t.Run("issue", func(t *testing.T) {
		h := mkHandler(t, &fakeStore{}, nil, cedartest.Engine(""))
		ctx := apiutil.WithAdmissionSlot(issuerCtx(uuid.New()))
		if _, err := h.Issue(ctx, &adminv1.CapabilityServiceIssueRequest{
			Subject: &adminv1.CapabilityPrincipal{
				Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_USER, TenantId: other.String(), Subject: "svc",
			},
			Audience:   []string{"paladin-data"},
			TtlSeconds: 300,
			Caveats:    openCaveats(limes.OpGet),
		}); err != nil {
			t.Fatalf("issue: %v", err)
		}
		if !slices.Contains(apiutil.AdmittedTenants(ctx), other) {
			t.Errorf("admitted %v, want %s among them", apiutil.AdmittedTenants(ctx), other)
		}
	})

	t.Run("revoke a run", func(t *testing.T) {
		h := mkHandler(t, &fakeStore{}, nil, &allowAuthorizer{}).WithRunRevocation(&fakeRuns{})
		ctx := apiutil.WithAdmissionSlot(issuerCtx(uuid.New()))
		if _, err := h.RevokeRun(ctx, runRequest(other, uuid.New())); err != nil {
			t.Fatalf("revoke run: %v", err)
		}
		if !slices.Contains(apiutil.AdmittedTenants(ctx), other) {
			t.Errorf("admitted %v, want %s among them", apiutil.AdmittedTenants(ctx), other)
		}
	})

	t.Run("revoke another tenant's capability", func(t *testing.T) {
		target := mkParent(other, limes.OpGet)
		store := &lookupStore{recordingStore: recordingStore{fakeStore: fakeStore{cap: &target}}}
		h := mkHandler(t, store, nil, &allowAuthorizer{})
		ctx := apiutil.WithAdmissionSlot(issuerCtx(uuid.New()))
		if _, err := h.Revoke(ctx, &adminv1.CapabilityServiceRevokeRequest{Id: target.ID.String()}); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		if !slices.Contains(apiutil.AdmittedTenants(ctx), other) {
			t.Errorf("admitted %v, want %s among them", apiutil.AdmittedTenants(ctx), other)
		}
	})

	t.Run("refused", func(t *testing.T) {
		h := mkHandler(t, &fakeStore{}, nil, &allowAuthorizer{}).WithRunRevocation(&fakeRuns{})
		ctx := apiutil.WithAdmissionSlot(tenantCtx(uuid.New()))
		if _, err := h.RevokeRun(ctx, runRequest(other, uuid.New())); err == nil {
			t.Fatal("a caller confined to its tenant revoked another's run")
		}
		if slices.Contains(apiutil.AdmittedTenants(ctx), other) {
			t.Errorf("a refused caller was admitted to %s", other)
		}
	})
}
