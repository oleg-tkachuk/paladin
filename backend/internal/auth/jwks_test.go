package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// rsaJWK is priv's public half as a JWK under kid, used for use.
func rsaJWK(priv *rsa.PrivateKey, kid, use string) map[string]any {
	enc := base64.RawURLEncoding
	k := map[string]any{
		"kid": kid,
		"kty": "RSA",
		"n":   enc.EncodeToString(priv.N.Bytes()),
		"e":   enc.EncodeToString([]byte{0x01, 0x00, 0x01}), // 65537
	}
	if use != "" {
		k["use"] = use
	}
	return k
}

// Of what a set publishes, only RSA and EC signing keys with a kid are kept:
// not an encryption key, a shared secret, a key without a kid, or one that
// does not parse.
func TestJWKSSigningKeys(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, rsaTestBits)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"keys": []map[string]any{
		rsaJWK(priv, "sig", "sig"),
		rsaJWK(priv, "unmarked", ""),
		rsaJWK(priv, "enc", "enc"),
		rsaJWK(priv, "", "sig"),
		{"kid": "secret", "kty": "oct", "k": "c2VjcmV0"},
		{"kid": "broken", "kty": "RSA", "n": "!", "e": "AQAB"},
	}})
	keys, err := signingKeys(raw)
	if err != nil {
		t.Fatalf("signingKeys: %v", err)
	}
	for _, kid := range []string{"sig", "unmarked"} {
		if _, ok := keys.LookupKeyID(kid); !ok {
			t.Errorf("signing key %q left out", kid)
		}
	}
	for _, kid := range []string{"enc", "secret", "broken"} {
		if _, ok := keys.LookupKeyID(kid); ok {
			t.Errorf("key %q kept, want it left out", kid)
		}
	}
	if keys.Len() != 2 {
		t.Errorf("kept %d keys, want 2", keys.Len())
	}
}

func TestJWKSEmptyResponseRejected(t *testing.T) {
	if _, err := signingKeys([]byte(`{"keys":[]}`)); err == nil {
		t.Error("empty key set should error")
	}
	if _, err := signingKeys([]byte(`{"keys":[{"kid":"secret","kty":"oct","k":"c2VjcmV0"}]}`)); err == nil {
		t.Error("a set of shared secrets alone should error")
	}
}

func TestJWKSVerifierUnknownKidForcesRefresh(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, rsaTestBits)
	if err != nil {
		t.Fatal(err)
	}
	keyBody := map[string]any{"keys": []map[string]any{rsaJWK(priv, "k-fresh", "sig")}}
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_ = json.NewEncoder(w).Encode(keyBody)
	}))
	defer srv.Close()

	v := NewJWKSVerifier(srv.URL)
	v.HTTPClient = srv.Client()
	if _, err := v.keySetWith(t.Context(), "k-fresh"); err != nil {
		t.Fatalf("keySetWith: %v", err)
	}
	if hits != 1 {
		t.Errorf("expected one fetch on miss, got %d", hits)
	}
}
