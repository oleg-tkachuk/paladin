package apiutil

import (
	"slices"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// The OAuth grant reaches Cedar through this helper and only through it, so
// a principal built here without it would drop the consent (ADR-0035).
func TestCedarPrincipal_CarriesTheOAuthGrant(t *testing.T) {
	scopes := []string{auth.OAuthScopeRead}
	cp := CedarPrincipal(&auth.Principal{Subject: "u", OAuthClientID: "desktop-agent", OAuthScopes: scopes})
	if cp.OAuthClientID != "desktop-agent" || !slices.Equal(cp.OAuthScopes, scopes) {
		t.Errorf("grant = %q %v, want desktop-agent %v", cp.OAuthClientID, cp.OAuthScopes, scopes)
	}
}
