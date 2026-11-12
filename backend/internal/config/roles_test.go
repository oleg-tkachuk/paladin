package config

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// readerOf is a role that reads the binding at path, the first by name.
func readerOf(t *testing.T, path string) Role {
	t.Helper()
	for _, r := range slices.Sorted(maps.Keys(roleSecrets)) {
		if r.reads(path) {
			return r
		}
	}
	t.Fatalf("no role reads %s", path)
	return ""
}

// Every field a Secret can fill is read by some role: a field none reads
// would be configured and never resolved anywhere.
func TestEverySecretFieldHasAReader(t *testing.T) {
	for _, f := range secretFields() {
		readerOf(t, f.path)
	}
}

// What each role reads, written out here rather than derived, from the code
// that consumes each field: a change to the table is a change to this list.
var wantRoleSecrets = map[Role][]string{
	RoleAPI: {"api-token-hmac", "backend-access", "backend-secret", "dpop-nonce", "health-token",
		"pg-app", "pg-replica", "valkey"},
	RoleIAM: {"api-token-hmac", "backend-access", "backend-secret", "dpop-nonce", "health-token",
		"oauth-client", "pg-app", "pg-replica", "valkey"},
	RoleAdmin: {"api-token-hmac", "backend-access", "backend-secret", "dpop-nonce", "health-token",
		"pg-app", "pg-replica", "valkey"},
	RoleWorker: {"api-token-hmac", "backend-access", "backend-secret", "dpop-nonce", "health-token",
		"pg-app", "pg-migrate", "pg-reaper", "pg-replica"},
	RoleDispatcher: {"api-token-hmac", "backend-access", "backend-secret", "dpop-nonce", "health-token",
		"pg-app", "pg-migrate", "pg-reaper", "pg-replica"},
	RoleIngest: {"api-token-hmac", "backend-access", "backend-secret", "dpop-nonce", "health-token",
		"ingest-nats", "ingest-rabbitmq", "ingest-webhook", "pg-app", "pg-migrate", "pg-reaper", "pg-replica"},
	RoleMCP:     {"mcp-dpop", "mcp-exchange"},
	RoleSession: {"health-token"},
	RoleMCPEmbedded: {"api-token-hmac", "backend-access", "backend-secret", "dpop-nonce", "health-token",
		"mcp-dpop", "mcp-exchange", "oauth-client", "pg-app", "pg-replica", "valkey"},
	RoleMigrate:   {"pg-app", "pg-migrate"},
	RoleBootstrap: {"backend-access", "backend-secret", "bootstrap-admin", "pg-app"},
}

// everySecretConfig names a distinct Secret in every field a Secret fills.
func everySecretConfig() *Config {
	ref := func(name string) *SecretRef { return &SecretRef{Name: name, Key: fieldSecretKey} }
	named := func(name string) SecretRef { return SecretRef{Name: name, Key: fieldSecretKey} }
	cfg := &Config{}
	pg := &cfg.Datastores.Postgres
	pg.PasswordSecret, pg.MigratePasswordSecret, pg.ReaperPasswordSecret = ref("pg-app"), ref("pg-migrate"), ref("pg-reaper")
	pg.Replica.Enabled, pg.Replica.PasswordSecret = true, ref("pg-replica")
	cfg.Middleware.RateLimit.Backend = RateLimitBackendValkey
	cfg.Datastores.Valkey.PasswordSecret = ref("valkey")
	cfg.Bootstrap.Admin.Enabled, cfg.Bootstrap.Admin.PasswordSecret = true, ref("bootstrap-admin")
	cfg.APIToken.HMACKeySecret = ref("api-token-hmac")
	cfg.Capability.DPoPNonce.KeySecret = ref("dpop-nonce")
	cfg.MCP.DPoPKeySecret = ref("mcp-dpop")
	cfg.Runtime.HealthSnapshotTokenSecret = ref("health-token")
	cfg.Ingest.Webhook.SharedSecretRef = named("ingest-webhook")
	cfg.Ingest.NATS.TokenRef = named("ingest-nats")
	cfg.Ingest.RabbitMQ.URLRef = named("ingest-rabbitmq")
	cfg.MCP.OAuth.Exchange.ClientSecretRef = ref("mcp-exchange")
	cfg.Auth.OAuth.SeedClients = []OAuthSeedClient{{ClientID: "c", ClientSecretRef: ref("oauth-client")}}
	cfg.Storage.Backends = map[string]StorageBackend{fieldBackend: {Auth: StorageBackendAuth{
		AccessKeySecret: ref("backend-access"), SecretKeySecret: ref("backend-secret"),
	}}}
	return cfg
}

// Each role reads its own Secrets from the API and no other: what its
// service account must be allowed, and all it is.
func TestARoleReadsOnlyItsOwnSecrets(t *testing.T) {
	var names []string
	for _, want := range wantRoleSecrets {
		names = append(names, want...)
	}
	for role, want := range wantRoleSecrets {
		t.Run(string(role), func(t *testing.T) {
			got := fetchedBy(t, role, names)
			if !slices.Equal(got, want) {
				t.Errorf("%s reads %v, want %v", role, got, want)
			}
		})
	}
	if len(wantRoleSecrets) != len(roleSecrets) {
		t.Errorf("the table names %d roles, the test %d", len(roleSecrets), len(wantRoleSecrets))
	}
}

// fetchedBy resolves everySecretConfig for role against an API holding every
// Secret in names, and returns the names it read, sorted.
func fetchedBy(t *testing.T, role Role, names []string) []string {
	t.Helper()
	t.Setenv(DefaultK8sServiceHostEnvKey, "10.0.0.1")
	dir := t.TempDir()
	tokenPath, nsPath := filepath.Join(dir, "token"), filepath.Join(dir, "namespace")
	for path, body := range map[string]string{tokenPath: "token", nsPath: "paladin"} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	objs := make([]runtime.Object, 0, len(names))
	for _, n := range slices.Compact(slices.Sorted(slices.Values(names))) {
		objs = append(objs, secret("paladin", n, fieldSecretKey, "v"))
	}
	client := fake.NewClientset(objs...)
	read := map[string]bool{}
	client.PrependReactor("get", "secrets", func(a k8stesting.Action) (bool, runtime.Object, error) {
		read[a.(k8stesting.GetAction).GetName()] = true
		return false, nil, nil
	})
	r := newTestResolver(t, tokenPath, nsPath)
	r.client = client
	if err := r.ResolveConfig(context.Background(), everySecretConfig(), role); err != nil {
		t.Fatalf("ResolveConfig(%s): %v", role, err)
	}
	return slices.Sorted(maps.Keys(read))
}

// A field another role reads keeps its reference and no value, so nothing in
// this role mistakes it for a configured empty one.
func TestAFieldAnotherRoleReadsKeepsItsReference(t *testing.T) {
	names := []string{"pg-app", "pg-migrate", "pg-reaper", "pg-replica", "valkey", "bootstrap-admin",
		"api-token-hmac", "dpop-nonce", "mcp-dpop", "health-token", "ingest-webhook", "ingest-nats",
		"ingest-rabbitmq", "mcp-exchange", "oauth-client", "backend-access", "backend-secret"}
	t.Setenv(DefaultK8sServiceHostEnvKey, "10.0.0.1")
	dir := t.TempDir()
	tokenPath, nsPath := filepath.Join(dir, "token"), filepath.Join(dir, "namespace")
	for path, body := range map[string]string{tokenPath: "token", nsPath: "paladin"} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	secrets := make([]*corev1.Secret, 0, len(names))
	for _, n := range names {
		secrets = append(secrets, secret("paladin", n, fieldSecretKey, "v"))
	}
	cfg := everySecretConfig()
	if err := newTestResolver(t, tokenPath, nsPath, secrets...).ResolveConfig(context.Background(), cfg, RoleMCP); err != nil {
		t.Fatal(err)
	}
	if cfg.APIToken.HMACKey != "" || cfg.APIToken.HMACKeySecret == nil {
		t.Errorf("the bridge resolved the API-token pepper: value %q, reference %v", cfg.APIToken.HMACKey, cfg.APIToken.HMACKeySecret)
	}
	if cfg.MCP.DPoPKey == "" || cfg.MCP.DPoPKeySecret != nil {
		t.Errorf("the bridge did not resolve its own DPoP key")
	}
}

// A role the binary does not know is refused, never resolved as one that
// reads nothing.
func TestAnUnknownRoleIsRefused(t *testing.T) {
	if _, err := ParseRole("scheduler"); err == nil {
		t.Error("ParseRole(scheduler) = nil error")
	}
	for _, r := range slices.Sorted(maps.Keys(roleSecrets)) {
		if got, err := ParseRole(string(r)); err != nil || got != r {
			t.Errorf("ParseRole(%s) = %q, %v", r, got, err)
		}
	}
	t.Setenv(DefaultK8sServiceHostEnvKey, "10.0.0.1")
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenPath, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := newTestResolver(t, tokenPath, filepath.Join(dir, "ns")).ResolveConfig(context.Background(), &Config{}, Role("scheduler"))
	if err == nil || !strings.Contains(err.Error(), "unknown role") {
		t.Errorf("ResolveConfig(unknown role) = %v, want an error naming the unknown role", err)
	}
}
