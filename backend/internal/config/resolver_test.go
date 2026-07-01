package config

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// newTestResolver builds a K8sSecretResolver whose token/namespace paths
// live under a tmp dir we control, so tests can decide whether the SA
// token "exists" or not. The HTTP client is left untouched — only the
// pre-flight token-existence path is exercised here unless apiBaseURL is
// overridden.
func newTestResolver(t *testing.T, tokenPath, nsPath string) *K8sSecretResolver {
	t.Helper()
	r := NewK8sSecretResolver(zap.NewNop())
	r.tokenPath = tokenPath
	r.nsPath = nsPath
	return r
}

func configWithSecretRef() *Config {
	return &Config{
		Datastores: Datastores{
			Postgres: Postgres{
				DSN:            "postgres://x@y/z",
				PasswordSecret: &SecretRef{Name: "pg", Key: "password"},
			},
		},
	}
}

// In-cluster + token present → pre-flight passes and the walk proceeds.
// We stand up a fake K8s API to verify the resolver actually resolves
// the SecretRef rather than no-op'ing.
func TestResolveConfig_InCluster_TokenPresent_Resolves(t *testing.T) {
	t.Setenv(DefaultK8sServiceHostEnvKey, "10.0.0.1")

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token")
	nsPath := filepath.Join(dir, "namespace")
	if err := os.WriteFile(tokenPath, []byte("fake-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nsPath, []byte("paladin"), 0o600); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if got, want := req.URL.Path, "/api/v1/namespaces/paladin/secrets/pg"; got != want {
			t.Errorf("unexpected secret URL: got %q want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"stringData":{"password":"resolved-value"}}`))
	}))
	defer srv.Close()

	r := newTestResolver(t, tokenPath, nsPath)
	r.apiBaseURL = srv.URL
	r.client = srv.Client()

	cfg := configWithSecretRef()
	if err := r.ResolveConfig(context.Background(), cfg); err != nil {
		t.Fatalf("ResolveConfig() error = %v, want nil", err)
	}
	if got := cfg.Datastores.Postgres.Password; got != "resolved-value" {
		t.Errorf("Password = %q, want %q", got, "resolved-value")
	}
	if cfg.Datastores.Postgres.PasswordSecret != nil {
		t.Errorf("PasswordSecret = %+v, want cleared", cfg.Datastores.Postgres.PasswordSecret)
	}
}

// In-cluster + no token file → actionable error mentioning the chart toggle.
func TestResolveConfig_InCluster_TokenMissing_Errors(t *testing.T) {
	t.Setenv(DefaultK8sServiceHostEnvKey, "10.0.0.1")

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token") // intentionally not created
	nsPath := filepath.Join(dir, "namespace")

	r := newTestResolver(t, tokenPath, nsPath)

	err := r.ResolveConfig(context.Background(), configWithSecretRef())
	if err == nil {
		t.Fatal("ResolveConfig() error = nil, want actionable error")
	}
	msg := err.Error()
	for _, want := range []string{
		"SA token missing",
		"automountServiceAccountToken: false",
		"serviceAccount.automount: true",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing substring %q", msg, want)
		}
	}
}

// The auth signing key and the ingest-webhook HMAC secret both carry a
// SecretRef that Validate() accepts but the resolver historically never
// walked — so a SealedSecret-backed prod deploy booted with an empty key.
// This pins that both refs resolve in-cluster and are cleared after.
func TestResolveConfig_InCluster_SigningKeyAndWebhookSecret(t *testing.T) {
	t.Setenv(DefaultK8sServiceHostEnvKey, "10.0.0.1")

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token")
	nsPath := filepath.Join(dir, "namespace")
	if err := os.WriteFile(tokenPath, []byte("fake-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nsPath, []byte("paladin"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Fake K8s API serving both secrets by path.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/api/v1/namespaces/paladin/secrets/paladin-signing-key":
			_, _ = w.Write([]byte(`{"stringData":{"key":"resolved-signing-key"}}`))
		case "/api/v1/namespaces/paladin/secrets/paladin-ingest-hmac":
			_, _ = w.Write([]byte(`{"stringData":{"secret":"resolved-hmac"}}`))
		default:
			t.Errorf("unexpected secret URL: %q", req.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	r := newTestResolver(t, tokenPath, nsPath)
	r.apiBaseURL = srv.URL
	r.client = srv.Client()

	cfg := &Config{
		Auth: Auth{
			SigningKeySecret: &SecretRef{Name: "paladin-signing-key", Key: "key"},
		},
	}
	cfg.Ingest.Webhook.SharedSecretRef = SecretRef{Name: "paladin-ingest-hmac", Key: "secret"}

	if err := r.ResolveConfig(context.Background(), cfg); err != nil {
		t.Fatalf("ResolveConfig() error = %v, want nil", err)
	}
	if got := cfg.Auth.SigningKey; got != "resolved-signing-key" {
		t.Errorf("Auth.SigningKey = %q, want resolved-signing-key", got)
	}
	if cfg.Auth.SigningKeySecret != nil {
		t.Errorf("Auth.SigningKeySecret = %+v, want cleared", cfg.Auth.SigningKeySecret)
	}
	if got := cfg.Ingest.Webhook.SharedSecret; got != "resolved-hmac" {
		t.Errorf("Ingest.Webhook.SharedSecret = %q, want resolved-hmac", got)
	}
	if cfg.Ingest.Webhook.SharedSecretRef.Name != "" {
		t.Errorf("Ingest.Webhook.SharedSecretRef = %+v, want cleared", cfg.Ingest.Webhook.SharedSecretRef)
	}
}

// Out-of-cluster (no KUBERNETES_SERVICE_HOST) + no token file →
// silently no-op; inline values left intact.
func TestResolveConfig_OutOfCluster_NoToken_NoOp(t *testing.T) {
	// Ensure env is unset even if the test runner inherited it.
	t.Setenv(DefaultK8sServiceHostEnvKey, "")
	if err := os.Unsetenv(DefaultK8sServiceHostEnvKey); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token") // intentionally not created
	nsPath := filepath.Join(dir, "namespace")

	r := newTestResolver(t, tokenPath, nsPath)

	cfg := &Config{
		Datastores: Datastores{
			Postgres: Postgres{
				DSN:      "postgres://x@y/z",
				Password: "inline-pw",
			},
		},
	}
	if err := r.ResolveConfig(context.Background(), cfg); err != nil {
		t.Fatalf("ResolveConfig() error = %v, want nil", err)
	}
	if got := cfg.Datastores.Postgres.Password; got != "inline-pw" {
		t.Errorf("Password = %q, want %q", got, "inline-pw")
	}
}
