package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// runtimeResolver is a RuntimeSecretResolver held to namespace, in a pod of
// podNamespace, over an API holding secrets.
func runtimeResolver(t *testing.T, namespace, podNamespace string, secrets ...string) *RuntimeSecretResolver {
	t.Helper()
	t.Setenv(DefaultK8sServiceHostEnvKey, "10.0.0.1")
	dir := t.TempDir()
	tokenPath, nsPath := filepath.Join(dir, "token"), filepath.Join(dir, "namespace")
	for path, body := range map[string]string{tokenPath: "token", nsPath: podNamespace} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stored := make([]*corev1.Secret, 0, len(secrets))
	for _, s := range secrets {
		ns, name, _ := strings.Cut(s, "/")
		stored = append(stored, secret(ns, name, fieldSecretKey, "v"))
	}
	return &RuntimeSecretResolver{k8s: newTestResolver(t, tokenPath, nsPath, stored...), namespace: namespace}
}

// Data names the Secrets a running process reads — a registered backend's
// credentials, a sink's — so they are confined to one namespace (ADR-0047),
// the only one where the chart lets admin and the dispatcher read Secrets.
func TestARuntimeSecretIsReadFromItsNamespaceOnly(t *testing.T) {
	const runtimeNS, podNS = "paladin-credentials", "paladin"
	r := runtimeResolver(t, runtimeNS, podNS, runtimeNS+"/creds", podNS+"/paladin-session-key")

	if got, err := r.ResolveSecret(context.Background(), &SecretRef{Name: "creds", Key: fieldSecretKey}); err != nil || got == "" {
		t.Fatalf("a ref naming no namespace = %q, %v; want the runtime namespace's Secret", got, err)
	}
	if _, err := r.ResolveSecret(context.Background(), &SecretRef{Namespace: runtimeNS, Name: "creds", Key: fieldSecretKey}); err != nil {
		t.Fatalf("a ref naming the runtime namespace: %v", err)
	}
	_, err := r.ResolveSecret(context.Background(), &SecretRef{Namespace: podNS, Name: "paladin-session-key", Key: fieldSecretKey})
	if err == nil || !strings.Contains(err.Error(), runtimeNS) {
		t.Fatalf("a ref into the pod's own namespace = %v, want a refusal naming %s", err, runtimeNS)
	}
}

// With no runtime namespace configured a ref resolves where it always did:
// the namespace it names, else the pod's.
func TestWithoutARuntimeNamespaceTheRefDecides(t *testing.T) {
	r := runtimeResolver(t, "", "paladin", "paladin/creds", "other/creds")
	for _, ref := range []*SecretRef{
		{Name: "creds", Key: fieldSecretKey},
		{Namespace: "other", Name: "creds", Key: fieldSecretKey},
	} {
		if _, err := r.ResolveSecret(context.Background(), ref); err != nil {
			t.Errorf("ResolveSecret(%+v): %v", ref, err)
		}
	}
}
