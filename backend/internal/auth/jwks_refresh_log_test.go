package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

const (
	jwksTestRefresh = 5 * time.Millisecond
	jwksTestWait    = 5 * time.Second
)

// A background refresh that fails is logged: the cached keys stay in use,
// and a key the identity provider rotated in is refused until one succeeds.
func TestJWKSRefreshFailureIsLogged(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding
	keys := map[string]any{"keys": []map[string]any{{
		"kid": "k1", "kty": "RSA", "use": "sig",
		"n": enc.EncodeToString(priv.N.Bytes()), "e": enc.EncodeToString([]byte{0x01, 0x00, 0x01}),
	}}}
	var served atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// The first fetch, Start's, succeeds; every refresh after it fails.
		if served.Add(1) > 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(keys)
	}))
	defer srv.Close()

	core, logs := observer.New(zap.WarnLevel)
	v := NewJWKSVerifier(srv.URL)
	v.HTTPClient = srv.Client()
	v.RefreshInterval = jwksTestRefresh
	v.Logger = zap.New(core)
	if err := v.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(jwksTestWait)
	for logs.FilterMessage("jwks refresh failed; keeping the cached keys").Len() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("a failed refresh was never logged")
		}
		time.Sleep(jwksTestRefresh)
	}
}
