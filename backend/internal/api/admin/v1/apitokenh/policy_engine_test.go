package apitokenh

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar/cedartest"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// These run the handler against the real Cedar engine with an empty tenant
// policy, so what they assert is the built-in policy's answer for each role.

// The built-in grants platform.admin every token action.
func TestCreate_PlatformAdminThroughTheRealEngine(t *testing.T) {
	iss := &stubIssuer{}
	h := newHandler(iss, &fakeStore{}, cedartest.Engine(""))
	target := uuid.New()

	if _, err := h.Create(ctxAs("platform.admin"), connect.NewRequest(&adminv1.APITokenServiceCreateRequest{
		Parent: "tenants/" + target.String(), DisplayName: "acme-service",
	})); err != nil {
		t.Fatalf("Create as platform.admin: %v", err)
	}
	if iss.req.TenantID != target {
		t.Fatalf("issued for %v, want %v", iss.req.TenantID, target)
	}
}

// platform.capability-issuer mints capabilities, which are short-lived and
// caveated; it must not reach the long-lived credential. Every call targets
// the caller's OWN tenant, so the refusal is Cedar's, not the handler's
// cross-tenant gate.
func TestTokenActions_CapabilityIssuerIsRefused(t *testing.T) {
	own := uuid.New()
	ctx := ctxAsTenant(own, "platform.capability-issuer")
	iss := &stubIssuer{}
	store := &fakeStore{}
	h := newHandler(iss, store, cedartest.Engine(""))
	tokenName := "tenants/" + own.String() + "/apiTokens/" + uuid.NewString()

	_, createErr := h.Create(ctx, connect.NewRequest(&adminv1.APITokenServiceCreateRequest{
		Parent: "tenants/" + own.String(), DisplayName: "escalation",
	}))
	_, revokeErr := h.Revoke(ctx, connect.NewRequest(&adminv1.APITokenServiceRevokeRequest{Name: tokenName}))
	_, listErr := h.List(ctx, connect.NewRequest(&adminv1.APITokenServiceListRequest{Parent: "tenants/" + own.String()}))
	_, usageErr := h.GetUsage(ctx, connect.NewRequest(&adminv1.APITokenServiceGetUsageRequest{Name: tokenName}))

	for call, err := range map[string]error{
		"Create": createErr, "Revoke": revokeErr, "List": listErr, "GetUsage": usageErr,
	} {
		if code(err) != connect.CodePermissionDenied {
			t.Errorf("%s code = %v, want PermissionDenied", call, code(err))
		}
	}
	if iss.req.Name != "" || store.revokeCalls != 0 {
		t.Fatalf("a refused call reached the issuer or the store: issued %q, revokes %d", iss.req.Name, store.revokeCalls)
	}
}
