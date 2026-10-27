package capabilityh

import (
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar/cedartest"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// These run the handler against the real Cedar engine with an empty tenant
// policy, so what they assert is the built-in policy's answer for each role.

const roleCapabilityIssuer = "platform.capability-issuer"

// A consumer reads back the capabilities it issued for the tenants it serves.
func TestList_CapabilityIssuerMayListAnotherTenant(t *testing.T) {
	store := &recordingStore{}
	h := mkHandler(t, store, nil, cedartest.Engine(""))
	other := uuid.New()

	if _, err := h.List(callerCtx(uuid.New(), roleCapabilityIssuer), &adminv1.CapabilityServiceListRequest{
		TenantId: other.String(),
	}); err != nil {
		t.Fatalf("List as capability-issuer: %v", err)
	}
	if store.listArgs == nil || store.listArgs.TenantID != other {
		t.Fatalf("list ran for %+v, want tenant %v", store.listArgs, other)
	}
}

// Delegate is left out of the issuer's grant: delegation narrows only a
// capability the caller presents, and the role presents none, so the
// caller is told to present one whatever Cedar would say.
func TestDelegate_CapabilityIssuerIsRefused(t *testing.T) {
	m := captest.NewMinter(t)
	parent := mintParent(t, m, limes.OpGet, limes.OpShare)
	h := NewHandler(m.Issuer, m.Verifier, m.Store, nil, cedartest.Engine(""))

	_, err := h.Delegate(callerCtx(uuid.New(), roleCapabilityIssuer), delegateReq(parent))
	if codeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", codeOf(err))
	}
}

// A caller with no role and no tenant grant gets nothing: deny by default
// holds for every capability action.
func TestCapabilityActions_DeniedWithoutAGrant(t *testing.T) {
	store := &recordingStore{}
	h := mkHandler(t, store, nil, cedartest.Engine(""))
	own := uuid.New()
	ctx := callerCtx(own)

	_, issueErr := h.Issue(ctx, &adminv1.CapabilityServiceIssueRequest{
		Subject: &adminv1.CapabilityPrincipal{
			Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_USER, TenantId: own.String(), Subject: "self",
		},
		Audience: []string{"paladin-data"}, TtlSeconds: 300,
		Caveats: openCaveats(limes.OpGet),
	})
	_, revokeErr := h.Revoke(ctx, &adminv1.CapabilityServiceRevokeRequest{Id: uuid.New().String()})
	_, listErr := h.List(ctx, &adminv1.CapabilityServiceListRequest{TenantId: own.String()})

	for call, err := range map[string]error{"Issue": issueErr, "Revoke": revokeErr, "List": listErr} {
		if codeOf(err) != connect.CodePermissionDenied {
			t.Errorf("%s code = %v, want PermissionDenied", call, codeOf(err))
		}
	}
}
