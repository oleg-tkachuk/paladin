package capabilityh

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
	"github.com/oleg-tkachuk/paladin/backend/internal/capop"
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

// forbidDelegation is a tenant policy that refuses every delegation.
const forbidDelegation = `forbid(principal, action == Action::"DelegateCapability", resource);`

// presentedCtx is a request presenting parent on behalf of a tenant admin,
// with the per-request operation holder the capability interceptor installs.
func presentedCtx(parent limes.VerifiedCapability) context.Context {
	return capop.WithHolder(auth.WithCapability(callerCtx(uuid.New(), "tenant.admin"), parent))
}

// A capability that allows share delegates under the built-in policy alone.
func TestDelegate_BuiltinPolicyAllowsShare(t *testing.T) {
	m := captest.NewMinter(t)
	parent := mintParent(t, m, limes.OpGet, limes.OpShare)
	h := NewHandler(m.Issuer, m.Verifier, m.Store, nil, cedartest.Engine(""))

	resp, err := h.Delegate(presentedCtx(parent), delegateReq(parent))
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if got := resp.GetCapability().GetParentId(); got != parent.Capability().ID.String() {
		t.Errorf("parent_id = %q, want %s", got, parent.Capability().ID)
	}
}

// A tenant policy that forbids DelegateCapability beats the built-in permit:
// the caller is told it was denied by policy, and nothing is minted.
func TestDelegate_TenantPolicyCanForbid(t *testing.T) {
	m := captest.NewMinter(t)
	parent := mintParent(t, m, limes.OpGet, limes.OpShare)
	h := NewHandler(m.Issuer, m.Verifier, m.Store, nil, cedartest.Engine(forbidDelegation))

	_, err := h.Delegate(presentedCtx(parent), delegateReq(parent))
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied (%v)", codeOf(err), err)
	}
	if ce, ok := errors.AsType[*connect.Error](err); !ok || ce.Message() != apiutil.DeniedByPolicy {
		t.Errorf("err = %v, want the message %q", err, apiutil.DeniedByPolicy)
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
