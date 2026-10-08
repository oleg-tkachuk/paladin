package auth

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/capability"
)

// TestExtractCapabilityToken covers the header parsing branches:
// X-Paladin-Capability wins over Authorization, Authorization with the
// "Capability" scheme is parsed, other schemes are ignored, malformed
// values return empty.
func TestExtractCapabilityToken(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		xlegate string
		authz   string
		want    string
	}{
		"x-paladin wins":       {xlegate: "tok-a", authz: "Capability tok-b", want: "tok-a"},
		"authz capability":     {xlegate: "", authz: "Capability tok-b", want: "tok-b"},
		"authz lowercase":      {xlegate: "", authz: "capability tok-b", want: "tok-b"},
		"authz bearer ignored": {xlegate: "", authz: "Bearer tok-b", want: ""},
		"authz malformed":      {xlegate: "", authz: "Capability", want: ""},
		"authz empty":          {xlegate: "", authz: "", want: ""},
		"x-paladin whitespace": {xlegate: "  tok-c  ", authz: "", want: "tok-c"},
		"both empty":           {xlegate: "   ", authz: "", want: ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := extractCapabilityToken(tc.xlegate, tc.authz)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCapabilityInterceptor_NilVerifier confirms the interceptor is a
// pass-through when the capability subsystem is disabled: a call carrying
// a capability reaches the handler unchanged, with no capability verified
// onto its context.
func TestCapabilityInterceptor_NilVerifier(t *testing.T) {
	t.Parallel()
	const unverifiable = "eyJ0eXAiOiJKV1QifQ.e30.sig"
	i := CapabilityInterceptor(nil, planeData, nil, 0, "")
	if i == nil {
		t.Fatalf("nil interceptor returned")
	}
	c := callProbe(context.Background(), []connect.ServerInterceptor{i}, HeaderCapability, unverifiable)
	if c.err != nil {
		t.Fatalf("expected a pass-through, got %v", c.err)
	}
	if _, ok := CapabilityFromContext(c.handlerCtx); ok {
		t.Error("a disabled subsystem put a capability on the context")
	}
}

// ─── capability as an identity ──────────────────────────────────────────────

// A verified capability must authenticate its OWN tenant. Before this the
// interceptor was additive only, so a capability could narrow a caller who was
// already authenticated but could never reach a tenant on its own — which made
// it unusable as the mechanism for serving a tenant whose long-lived credential
// we deliberately do not hold.
func TestWithCapabilityPrincipal_EstablishesTheCapabilitysTenant(t *testing.T) {
	tenant := uuid.New()
	capID := uuid.New()
	i := &capabilityInterceptor{audience: "paladin-data", establishPrincipal: true}

	ctx, err := i.withCapabilityPrincipal(context.Background(), &capability.Capability{
		ID:      capID,
		Subject: capability.Principal{TenantID: tenant, Subject: "svc"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	p, perr := PrincipalFromContext(ctx)
	if perr != nil {
		t.Fatalf("no principal established: %v", perr)
	}
	if p.TenantID != tenant {
		t.Errorf("tenant = %v, want the capability's %v", p.TenantID, tenant)
	}
	if p.Kind != PrincipalKindCapability {
		t.Errorf("kind = %v, want PrincipalKindCapability", p.Kind)
	}
	if len(p.Roles) != 0 {
		t.Errorf("roles = %v; a capability must never satisfy a role-gated policy", p.Roles)
	}
	if p.Subject != "capability:"+capID.String() {
		t.Errorf("subject = %q, want the capability id so audit can trace it", p.Subject)
	}
}

// A capability presented alongside a JWT or API token stays additive: the
// established identity wins, so adding one to an existing call cannot silently
// re-scope it to another tenant.
func TestWithCapabilityPrincipal_ExistingPrincipalWins(t *testing.T) {
	original := &Principal{TenantID: uuid.New(), Subject: "user-1", Kind: PrincipalKindUser}
	i := &capabilityInterceptor{audience: "paladin-data", establishPrincipal: true}

	ctx, err := i.withCapabilityPrincipal(WithPrincipal(context.Background(), original),
		&capability.Capability{ID: uuid.New(), Subject: capability.Principal{TenantID: uuid.New()}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	p, _ := PrincipalFromContext(ctx)
	if p.TenantID != original.TenantID || p.Kind != PrincipalKindUser {
		t.Errorf("principal was replaced: %+v", p)
	}
}

// A tenant-less capability cannot scope anything. The verifier rejects one, so
// this is the belt-and-braces path — it must refuse rather than establish a
// principal the tenant gate cannot scope.
func TestWithCapabilityPrincipal_RefusesATenantlessCapability(t *testing.T) {
	i := &capabilityInterceptor{audience: "paladin-data", establishPrincipal: true}

	_, err := i.withCapabilityPrincipal(context.Background(),
		&capability.Capability{ID: uuid.New(), Subject: capability.Principal{}})

	if err == nil {
		t.Fatal("want an error for a capability with no tenant")
	}
}

// The additive interceptor must stay additive: nothing establishes identity
// unless the wiring asked for it.
func TestWithCapabilityPrincipal_NoopWhenNotEstablishing(t *testing.T) {
	i := &capabilityInterceptor{audience: "paladin-data"}

	ctx, err := i.withCapabilityPrincipal(context.Background(),
		&capability.Capability{ID: uuid.New(), Subject: capability.Principal{TenantID: uuid.New()}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, perr := PrincipalFromContext(ctx); perr == nil {
		t.Error("a non-establishing interceptor must not stamp a principal")
	}
}

// RequireAudience compares against the canonical plane audience, so the
// principal must carry that rather than the capability plane label.
func TestWithCapabilityPrincipal_MapsThePlaneAudience(t *testing.T) {
	i := &capabilityInterceptor{audience: "data", establishPrincipal: true}

	ctx, err := i.withCapabilityPrincipal(context.Background(), &capability.Capability{
		ID: uuid.New(), Subject: capability.Principal{TenantID: uuid.New()},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	p, _ := PrincipalFromContext(ctx)
	if p.Audience != AudienceData {
		t.Errorf("audience = %q, want the canonical %q", p.Audience, AudienceData)
	}
}
