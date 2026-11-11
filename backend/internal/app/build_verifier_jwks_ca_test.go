package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"uuid"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/issuer"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/sessionkey"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/tests/sessiontest"
)

// The session JWKS is served by the minting role under the internal CA; a
// verifier trusting that CA fetches it and verifies what the set signed.
func TestTheJWKSIsFetchedUnderTheConfiguredCA(t *testing.T) {
	keys := sessiontest.Keys(t)
	srv := httptest.NewTLSServer(keys.Handler())
	t.Cleanup(srv.Close)
	caPath := filepath.Join(t.TempDir(), "ca.crt")
	writeCA(t, caPath, srv.TLS)

	v, err := BuildVerifier(context.Background(), config.Auth{
		Issuer: "paladin", JWKSURL: srv.URL + sessionkey.WellKnownPath, JWKSCAPath: caPath,
	}, nil, auth.AudienceAdmin, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	iss, err := issuer.New(issuer.Config{Issuer: "paladin", Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := iss.MintAccess(issuer.AccessClaims{
		Subject: "operator", TenantID: uuid.New(), Audience: auth.AudienceAdmin, Kind: auth.PrincipalKindUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(context.Background(), token); err != nil {
		t.Fatalf("a session the set signed does not verify through the JWKS under the CA: %v", err)
	}
}

// A CA bundle that does not load stops the role at boot, rather than every
// session failing later for a reason no log names.
func TestAnUnreadableJWKSCARefuses(t *testing.T) {
	_, err := BuildVerifier(context.Background(), config.Auth{
		Issuer: "paladin", JWKSURL: "https://iam.invalid" + sessionkey.WellKnownPath,
		JWKSCAPath: filepath.Join(t.TempDir(), "absent.crt"),
	}, nil, auth.AudienceAdmin, zap.NewNop())
	if err == nil || !strings.Contains(err.Error(), "auth.jwks_ca_path") {
		t.Fatalf("BuildVerifier(absent CA) error = %v, want one naming auth.jwks_ca_path", err)
	}
}

func writeCA(t *testing.T, path string, cfg *tls.Config) {
	t.Helper()
	der := cfg.Certificates[0].Certificate[0]
	if _, err := x509.ParseCertificate(der); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}
