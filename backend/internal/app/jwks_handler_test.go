package app

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/oleg-tkachuk/limes/jwks"
)

// jwksPath is where the admin plane serves the key set.
const jwksPath = "/.well-known/jwks.json"

// The handler serves the bundle's keys as a JWK Set that parses back to the
// same keys, cacheable for jwksMaxAge.
func TestJWKSHandlerServesTheKeysForMaxAge(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	kid := jwks.KeyID(pub)
	h := JWKSHandler(map[string]ed25519.PublicKey{kid: pub})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, jwksPath, nil))
	res := rec.Result()
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	if got, want := res.Header.Get("Cache-Control"), fmt.Sprintf("public, max-age=%d", int64(jwksMaxAge.Seconds())); got != want {
		t.Errorf("Cache-Control = %q, want %q", got, want)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := jwks.Parse(body)
	if err != nil {
		t.Fatalf("parse served JWKS: %v", err)
	}
	if len(keys) != 1 || !keys[kid].Equal(pub) {
		t.Errorf("served keys = %v, want only %s", keys, kid)
	}
}

// Anything but GET or HEAD is refused.
func TestJWKSHandlerRefusesWrites(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	JWKSHandler(map[string]ed25519.PublicKey{jwks.KeyID(pub): pub}).
		ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, jwksPath, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}
