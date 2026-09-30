package apiutil

import (
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// The kind has to reach Cedar, because the built-in delete permit turns on it.
// If this stops being populated, machine deletes go back to being denied — and
// the denial is silent at the call sites that swallow it.
func TestCedarPrincipal_CarriesTheCredentialKind(t *testing.T) {
	cases := map[auth.PrincipalKind]string{
		auth.PrincipalKindUser:           "user",
		auth.PrincipalKindApiKey:         "api_key",
		auth.PrincipalKindServiceAccount: "service_account",
		auth.PrincipalKindCapability:     "capability",
		auth.PrincipalKindUnspecified:    "",
	}
	for kind, want := range cases {
		t.Run(want, func(t *testing.T) {
			got := CedarPrincipal(&auth.Principal{Subject: "s", TenantID: uuid.New(), Kind: kind})
			if got.Kind != want {
				t.Fatalf("Kind = %q, want %q", got.Kind, want)
			}
		})
	}
}

// The override exists for handlers authorizing against another tenant's
// resource: Cedar compares principal.tenant_id to resource.tenant_id, so a
// principal left on its own tenant would evaluate a tenant-equality policy
// against the wrong side.
func TestCedarPrincipalFor_PinsTheTenant(t *testing.T) {
	own, other := uuid.New(), uuid.New()
	p := &auth.Principal{Subject: "s", TenantID: own, Kind: auth.PrincipalKindApiKey}

	if got := CedarPrincipalFor(p, other); got.TenantID != other {
		t.Fatalf("TenantID = %s, want the override %s", got.TenantID, other)
	}
	if got := CedarPrincipalFor(p, uuid.Nil); got.TenantID != own {
		t.Fatalf("TenantID = %s, want the principal's own %s", got.TenantID, own)
	}
}

// Scopes and slug were silently dropped by the hand-written literals this helper
// replaced; both are load-bearing (scope enforcement is a built-in forbid, and
// the slug is the Cedar Tenant UID policies name).
func TestCedarPrincipal_CarriesSlugAndScopes(t *testing.T) {
	p := &auth.Principal{
		Subject:    "s",
		TenantID:   uuid.New(),
		TenantSlug: "acme",
		Kind:       auth.PrincipalKindApiKey,
		Roles:      []string{"tenant.user"},
		Scopes:     []auth.Scope{{Type: auth.ScopeTenant, Value: "acme"}},
	}

	got := CedarPrincipal(p)
	if got.TenantSlug != "acme" {
		t.Fatalf("TenantSlug = %q", got.TenantSlug)
	}
	if len(got.Roles) != 1 || got.Roles[0] != "tenant.user" {
		t.Fatalf("Roles = %v", got.Roles)
	}
	if len(got.Scopes) != 1 {
		t.Fatalf("Scopes = %v, want one wire-form scope", got.Scopes)
	}
}

// A nil principal must not panic: several handlers authorize before they are
// sure a principal exists, and a panic in an authorization path is an outage.
func TestCedarPrincipal_NilIsSafe(t *testing.T) {
	if got := CedarPrincipal(nil); got == nil || got.Subject != "" {
		t.Fatalf("nil principal produced %#v", got)
	}
}
