package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// newTestResolver builds a K8sSecretResolver whose token/namespace paths
// live under a tmp dir we control, so tests can decide whether the SA
// token "exists" or not. The Kubernetes API is a client-go fake holding
// `secrets`; nil leaves the in-cluster clientset to be built, which only the
// pre-flight paths reach.
func newTestResolver(t *testing.T, tokenPath, nsPath string, secrets ...*corev1.Secret) *K8sSecretResolver {
	t.Helper()
	r := NewK8sSecretResolver(zap.NewNop())
	r.tokenPath = tokenPath
	r.nsPath = nsPath
	if secrets != nil {
		objs := make([]runtime.Object, len(secrets))
		for i, sec := range secrets {
			objs[i] = sec
		}
		r.client = fake.NewClientset(objs...)
	}
	return r
}

// secret builds a Secret as the API returns it: values in Data, decoded.
func secret(namespace, name, key, value string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Data:       map[string][]byte{key: []byte(value)},
	}
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

	r := newTestResolver(t, tokenPath, nsPath, secret("paladin", "pg", "password", "resolved-value"))

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

	r := newTestResolver(t, tokenPath, nsPath,
		secret("paladin", "paladin-signing-key", "key", "resolved-signing-key"),
		secret("paladin", "paladin-ingest-hmac", "secret", "resolved-hmac"),
	)

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

// inCluster writes the projected token and namespace files and marks the
// process as in-cluster, returning their paths.
func inCluster(t *testing.T) (tokenPath, nsPath string) {
	t.Helper()
	t.Setenv(DefaultK8sServiceHostEnvKey, "10.0.0.1")
	dir := t.TempDir()
	tokenPath = filepath.Join(dir, "token")
	nsPath = filepath.Join(dir, "namespace")
	if err := os.WriteFile(tokenPath, []byte("fake-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nsPath, []byte("paladin"), 0o600); err != nil {
		t.Fatal(err)
	}
	return tokenPath, nsPath
}

// What the API refuses is reported in terms an operator can act on.
func TestResolveSecret_Refusals(t *testing.T) {
	tokenPath, nsPath := inCluster(t)
	forbidden := apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "pg", errors.New("rbac"))

	cases := []struct {
		name    string
		secrets []*corev1.Secret
		react   error
		want    string
	}{
		{name: "missing secret", secrets: []*corev1.Secret{}, want: "secret pg not found in namespace paladin"},
		{name: "rbac", secrets: []*corev1.Secret{}, react: forbidden, want: "access denied reading secret pg"},
		{name: "missing key", secrets: []*corev1.Secret{secret("paladin", "pg", "other", "v")}, want: `key "password" not found in secret`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestResolver(t, tokenPath, nsPath, tc.secrets...)
			if tc.react != nil {
				r.client.(*fake.Clientset).PrependReactor("get", "secrets",
					func(k8stesting.Action) (bool, runtime.Object, error) { return true, nil, tc.react })
			}
			_, err := r.ResolveSecret(context.Background(), &SecretRef{Name: "pg", Key: "password"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}
