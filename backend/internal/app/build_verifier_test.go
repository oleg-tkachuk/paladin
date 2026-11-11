package app

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

// A role that verifies sessions with neither a JWKS nor a signing key would
// otherwise build an HMAC verifier over the empty key, which accepts any
// token HMAC'd with nothing. Only the roles that mint require the key now
// (ADR-0047), so the verifier refuses to be built instead.
func TestBuildVerifierRefusesNoKeySource(t *testing.T) {
	_, err := BuildVerifier(context.Background(), config.Auth{Issuer: "paladin"}, "paladin-admin", zap.NewNop())
	if err == nil || !strings.Contains(err.Error(), "auth.signing_key") {
		t.Fatalf("BuildVerifier(no key) error = %v, want one naming auth.signing_key", err)
	}
}

func TestBuildVerifierTakesTheSigningKey(t *testing.T) {
	v, err := BuildVerifier(context.Background(), config.Auth{Issuer: "paladin", SigningKey: "k"}, "paladin-admin", zap.NewNop())
	if err != nil || v == nil {
		t.Fatalf("BuildVerifier(signing key) = %v, %v", v, err)
	}
}
