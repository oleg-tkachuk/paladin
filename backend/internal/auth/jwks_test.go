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

func TestJWKSParserHandlesRSA(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding
	body := map[string]any{
		"keys": []map[string]any{{
			"kid": "k1",
			"kty": "RSA",
			"alg": "RS256",
			"use": "sig",
			"n":   enc.EncodeToString(priv.N.Bytes()),
			"e":   enc.EncodeToString([]byte{0x01, 0x00, 0x01}), // 65537
		}},
	}
	raw, _ := json.Marshal(body)
	keys, err := parseJWKS(raw)
	if err != nil {
		t.Fatalf("parseJWKS: %v", err)
	}
	if _, ok := keys["k1"].(*rsa.PublicKey); !ok {
		t.Errorf("expected *rsa.PublicKey for k1, got %T", keys["k1"])
	}
}

func TestJWKSSkipsEncryptionKeys(t *testing.T) {
	body := map[string]any{
		"keys": []map[string]any{
			{"kid": "enc", "kty": "RSA", "alg": "RS256", "use": "enc", "n": "AA", "e": "AQAB"},
			{"kid": "sig", "kty": "RSA", "alg": "RS256", "use": "sig", "n": "AA", "e": "AQAB"},
		},
	}
	raw, _ := json.Marshal(body)
	keys, err := parseJWKS(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := keys["enc"]; ok {
		t.Error("encryption key should be filtered out")
	}
	if _, ok := keys["sig"]; !ok {
		t.Error("signing key should be present")
	}
}

func TestJWKSEmptyResponseRejected(t *testing.T) {
	_, err := parseJWKS([]byte(`{"keys":[]}`))
	if err == nil {
		t.Error("empty key set should error")
	}
}

func TestJWKSVerifierUnknownKidForcesRefresh(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	enc := base64.RawURLEncoding
	keyBody := map[string]any{
		"keys": []map[string]any{{
			"kid": "k-fresh",
			"kty": "RSA",
			"use": "sig",
			"n":   enc.EncodeToString(priv.N.Bytes()),
			"e":   enc.EncodeToString([]byte{0x01, 0x00, 0x01}),
		}},
	}
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_ = json.NewEncoder(w).Encode(keyBody)
	}))
	defer srv.Close()

	v := NewJWKSVerifier(srv.URL)
	v.HTTPClient = srv.Client()
	if _, err := v.keyFor(t.Context(), "k-fresh"); err != nil {
		t.Fatalf("keyFor: %v", err)
	}
	if hits != 1 {
		t.Errorf("expected one fetch on miss, got %d", hits)
	}
}
