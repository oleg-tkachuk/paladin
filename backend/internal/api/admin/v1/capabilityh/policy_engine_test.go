package capabilityh

import (
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar/cedartest"
	"github.com/oleg-tkachuk/paladin/capability"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// These run the handler against the real Cedar engine with an empty tenant
// policy, so what they assert is the built-in policy's answer for each role.

const roleCapabilityIssuer = "platform.capability-issuer"

// A consumer reads back the capabilities it issued for the tenants it serves.
func TestList_CapabilityIssuerMayListAnotherTenant(t *testing.T) {
	store := &recordingStore{}
	h := NewHandler(mkIssuer(t, &store.fakeStore), store, nil, cedartest.Engine(""))
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

// Delegate is left out of the issuer's grant: the admin path reads the parent
// under the caller's own tenant, so it could never reach what the issuer
// minted for others. Cedar refuses before the store is asked.
func TestDelegate_CapabilityIssuerIsRefused(t *testing.T) {
	parent := mkParent(uuid.New(), capability.OpGet)
	store := &fakeStore{cap: &parent}
	h := NewHandler(mkIssuer(t, store), store, nil, cedartest.Engine(""))

	_, err := h.Delegate(callerCtx(uuid.New(), roleCapabilityIssuer), &adminv1.CapabilityServiceDelegateRequest{
		ParentId: parent.ID.String(),
	})
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", codeOf(err))
	}
}

// A caller with no role and no tenant grant gets nothing: deny by default
// holds for every capability action.
func TestCapabilityActions_DeniedWithoutAGrant(t *testing.T) {
	store := &recordingStore{}
	h := NewHandler(mkIssuer(t, &store.fakeStore), store, nil, cedartest.Engine(""))
	own := uuid.New()
	ctx := callerCtx(own)

	_, issueErr := h.Issue(ctx, &adminv1.CapabilityServiceIssueRequest{
		Subject: &adminv1.CapabilityPrincipal{
			Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_USER, TenantId: own.String(), Subject: "self",
		},
		Audience: []string{"paladin-data"}, TtlSeconds: 300,
		Caveats: &adminv1.CapabilityCaveats{Ops: []string{string(capability.OpGet)}},
	})
	_, revokeErr := h.Revoke(ctx, &adminv1.CapabilityServiceRevokeRequest{Id: uuid.New().String()})
	_, listErr := h.List(ctx, &adminv1.CapabilityServiceListRequest{TenantId: own.String()})

	for call, err := range map[string]error{"Issue": issueErr, "Revoke": revokeErr, "List": listErr} {
		if codeOf(err) != connect.CodePermissionDenied {
			t.Errorf("%s code = %v, want PermissionDenied", call, codeOf(err))
		}
	}
}
