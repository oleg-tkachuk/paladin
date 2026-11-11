package app

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/issuer"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/sessionkey"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/tests/sessiontest"
)

const (
	verifierIssuer   = "paladin"
	verifierAudience = auth.AudienceAdmin
	verifierSubject  = "operator"
)

// A role that holds no session key and names no JWKS cannot verify a
// session, so the verifier refuses to be built rather than accept nothing or
// anything (ADR-0047).
func TestBuildVerifierRefusesNoKeySource(t *testing.T) {
	_, err := BuildVerifier(context.Background(), config.Auth{Issuer: verifierIssuer}, nil, verifierAudience, zap.NewNop())
	if err == nil || !strings.Contains(err.Error(), "auth.jwks_url") {
		t.Fatalf("BuildVerifier(no keys, no jwks) error = %v, want one naming auth.jwks_url", err)
	}
}

func mint(t *testing.T, keys *sessionkey.Set) string {
	t.Helper()
	iss, err := issuer.New(issuer.Config{Issuer: verifierIssuer, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := iss.MintAccess(issuer.AccessClaims{Subject: verifierSubject, Audience: verifierAudience})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// The role that mints verifies by its own key set: a token the set signed
// passes, and one signed by any other key is refused.
func TestBuildVerifierVerifiesByTheKeysHeld(t *testing.T) {
	keys := sessiontest.Keys(t)
	v, err := BuildVerifier(context.Background(), config.Auth{Issuer: verifierIssuer}, keys, verifierAudience, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.(*auth.KeySetVerifier); !ok {
		t.Fatalf("BuildVerifier(keys) = %T, want *auth.KeySetVerifier", v)
	}
	p, err := v.Verify(context.Background(), mint(t, keys))
	if err != nil || p.Subject != verifierSubject {
		t.Fatalf("a token the set signed: principal %+v, err %v", p, err)
	}
	if _, err := v.Verify(context.Background(), mint(t, sessiontest.Keys(t))); err == nil {
		t.Fatal("a token another key signed verified")
	}
}

// A verifying role whose JWKS is unreachable at start still builds — the
// minting role may not be up yet — and refuses sessions until a fetch
// succeeds.
func TestBuildVerifierStartsWhileTheJWKSIsUnreachable(t *testing.T) {
	gone := httptest.NewServer(nil)
	url := gone.URL + sessionkey.WellKnownPath
	gone.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	v, err := BuildVerifier(ctx, config.Auth{Issuer: verifierIssuer, JWKSURL: url}, nil, verifierAudience, zap.NewNop())
	if err != nil {
		t.Fatalf("BuildVerifier(unreachable jwks) = %v, want it built", err)
	}
	if _, err := v.Verify(ctx, mint(t, sessiontest.Keys(t))); err == nil {
		t.Fatal("a session verified with no JWKS fetched")
	}
}
