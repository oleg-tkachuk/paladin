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

// A background refresh that succeeds says nothing: only a failure is worth a
// line.
func TestJWKSRefreshSuccessIsNotLogged(t *testing.T) {
	const refreshesToWatch = 3
	priv, err := rsa.GenerateKey(rand.Reader, rsaTestBits)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]any{"keys": []map[string]any{rsaJWK(priv, "k1", "sig")}}
	var served atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served.Add(1)
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
	for served.Load() < refreshesToWatch {
		if time.Now().After(deadline) {
			t.Fatal("the set was never fetched again")
		}
		time.Sleep(jwksTestRefresh)
	}
	if n := logs.Len(); n != 0 {
		t.Errorf("%d warnings for refreshes that succeeded", n)
	}
}

// An interval of none is the default, not a ticker of zero.
func TestJWKSRefreshEvery(t *testing.T) {
	for in, want := range map[time.Duration]time.Duration{
		0:                      defaultRefreshInterval,
		-time.Second:           defaultRefreshInterval,
		time.Nanosecond:        time.Nanosecond,
		jwksTestRefresh:        jwksTestRefresh,
		defaultRefreshInterval: defaultRefreshInterval,
	} {
		if got := refreshEvery(in); got != want {
			t.Errorf("refreshEvery(%s) = %s, want %s", in, got, want)
		}
	}
}

// An unknown kid fetches the set again at most once per MinRefreshInterval,
// by default too: a spray of invented kids costs the identity provider one
// fetch, and the window's end lets the next one through.
func TestJWKSUnknownKidIsDebounced(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, rsaTestBits)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]any{"keys": []map[string]any{rsaJWK(priv, "k1", "sig")}}
	var served atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served.Add(1)
		_ = json.NewEncoder(w).Encode(keys)
	}))
	defer srv.Close()

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := start
	v := NewJWKSVerifier(srv.URL)
	v.HTTPClient = srv.Client()
	v.Now = func() time.Time { return now }
	if err := v.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for _, step := range []struct {
		at      time.Duration
		fetches int32
	}{
		{time.Second, 1}, // inside the window: refused, not fetched
		{defaultMinRefreshInterval - time.Nanosecond, 1}, // still inside
		{defaultMinRefreshInterval, 2},                   // the window's end: fetched again
	} {
		now = start.Add(step.at)
		if _, err := v.keySetWith(t.Context(), "invented"); err == nil {
			t.Fatalf("at %s: an unknown kid was found", step.at)
		}
		if got := served.Load(); got != step.fetches {
			t.Errorf("at %s: %d fetches, want %d", step.at, got, step.fetches)
		}
	}

	// A window the operator set is kept, however short.
	v.MinRefreshInterval = time.Nanosecond
	now = now.Add(time.Second)
	_, _ = v.keySetWith(t.Context(), "invented")
	if got := served.Load(); got != 3 {
		t.Errorf("past a one-nanosecond window: %d fetches, want 3", got)
	}
}
