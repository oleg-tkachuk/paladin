package config

import (
	"fmt"
	"slices"
	"strings"
)

// Role is the process a configuration is loaded for: a `serve` role or a
// one-shot command. It decides which Secrets the resolver reads (ADR-0047):
// each role reads the ones its own code consumes and no other, so a role's
// service account needs nothing beyond them.
type Role string

// The roles, as the binary's commands name them.
const (
	// RoleAPI serves the data plane; RoleIAM the IAM plane, the OAuth
	// authorization server and the session JWKS, and alone holds the session
	// key (ADR-0047).
	RoleAPI        Role = "api"
	RoleIAM        Role = "iam"
	RoleAdmin      Role = "admin"
	RoleWorker     Role = "worker"
	RoleDispatcher Role = "dispatcher"
	RoleIngest     Role = "ingest"
	// RoleMCP is the standalone bridge: no database, only its own credentials.
	RoleMCP Role = "mcp"
	// RoleMCPEmbedded co-hosts the api and admin planes beside the bridge
	// (`serve mcp --embedded`), and reads what all three do (iam's set
	// includes api's).
	RoleMCPEmbedded Role = "mcp-embedded"
	// RoleSession holds the console's session (ADR-0048): no database, no
	// key; it mints its tokens at the iam plane.
	RoleSession   Role = "session"
	RoleMigrate   Role = "migrate"
	RoleBootstrap Role = "bootstrap"
)

// The resolver's binding paths: one per Secret-filled field, or the prefix of
// a family of them. secretBindings names each binding by one of these.
const (
	secretPostgresPassword        = "postgres.password_secret"
	secretPostgresMigratePassword = "postgres.migrate_password_secret" // #nosec G101 -- a config path, not a credential
	secretPostgresReaperPassword  = "postgres.reaper_password_secret"  // #nosec G101 -- a config path, not a credential
	secretPostgresReplicaPassword = "postgres.replica.password_secret"
	secretValkeyPassword          = "valkey.password_secret"
	secretBootstrapAdminPassword  = "bootstrap.admin.password_secret"
	secretAPITokenHMACKey         = "api_token.hmac_key_secret" // #nosec G101 -- a config path, not a credential
	secretDPoPNonceKey            = "capability.dpop_nonce.key_secret"
	secretMCPDPoPKey              = "mcp.dpop_key_secret" // #nosec G101 -- a config path, not a credential
	secretHealthSnapshotToken     = "runtime.health_snapshot_token_secret"
	secretIngestWebhook           = "ingest.webhook.shared_secret_ref" // #nosec G101 -- a config path, not a credential
	secretIngestNATSToken         = "ingest.nats.token_ref"            // #nosec G101 -- a config path, not a credential
	secretIngestRabbitMQURL       = "ingest.rabbitmq.url_ref"
	secretMCPExchangeClient       = "mcp.oauth.exchange.client_secret_ref" // #nosec G101 -- a config path, not a credential
	// secretOAuthSeedClients prefixes auth.oauth.seed_clients[<id>].client_secret_ref.
	secretOAuthSeedClients = "auth.oauth.seed_clients[" // #nosec G101 -- a config path, not a credential
	// secretStorageBackends prefixes storage.backends.<name>.auth.<field>.
	secretStorageBackends = "storage.backends."
)

// Builds is what a role's shared dependencies (app.BuildSharedDeps) build
// beyond the database, and so which of their Secrets the role reads: a role
// reads a Secret only for a part it builds (ADR-0047).
type Builds struct {
	// Storage is a client for every storage backend, from its credentials.
	Storage bool
	// TokenAuth is what verifies and issues API tokens and capabilities:
	// the API-token hasher, from its pepper, and the capability verifier and
	// issuers, with the DPoP nonces from their key. Without it a role holds
	// the token and capability stores alone.
	TokenAuth bool
}

// roleBuilds is what each role that builds the shared dependencies builds,
// from the code that consumes each part. A role absent here builds none.
var roleBuilds = map[Role]Builds{
	RoleAPI:   {Storage: true, TokenAuth: true},
	RoleIAM:   {TokenAuth: true},
	RoleAdmin: {Storage: true, TokenAuth: true},
	// Storage for the reapers and the object jobs; the token and capability
	// purgers need the stores alone.
	RoleWorker:     {Storage: true},
	RoleDispatcher: {},
	// Storage for the reconciler's probe of an object a delete event names.
	RoleIngest: {Storage: true},
	// The api, iam and admin planes beside the bridge.
	RoleMCPEmbedded: {Storage: true, TokenAuth: true},
}

// Builds is what r's shared dependencies build.
func (r Role) Builds() Builds { return roleBuilds[r] }

// sharedDepsSecrets are what a role building the shared dependencies reads
// for them: the database pool and replica always, every storage backend's
// credentials for Storage, the API-token pepper and the DPoP nonce key for
// TokenAuth.
func sharedDepsSecrets(r Role) []string {
	b := r.Builds()
	paths := []string{secretPostgresPassword, secretPostgresReplicaPassword}
	if b.Storage {
		paths = append(paths, secretStorageBackends)
	}
	if b.TokenAuth {
		paths = append(paths, secretAPITokenHMACKey, secretDPoPNonceKey)
	}
	return paths
}

// roleSecrets is the binding paths each role reads, from the code that
// consumes them. A role absent here is refused rather than given nothing.
var roleSecrets = map[Role][]string{
	// The planes: shared deps, the rate limits in Valkey, the health
	// snapshot; iam also seeds the OAuth clients (AssembleIAMMux).
	RoleAPI:   concat(sharedDepsSecrets(RoleAPI), secretValkeyPassword, secretHealthSnapshotToken),
	RoleIAM:   concat(sharedDepsSecrets(RoleIAM), secretValkeyPassword, secretHealthSnapshotToken, secretOAuthSeedClients),
	RoleAdmin: concat(sharedDepsSecrets(RoleAdmin), secretValkeyPassword, secretHealthSnapshotToken),
	// The partition pool is the migrate role's; the reaper pool its own.
	RoleWorker: concat(sharedDepsSecrets(RoleWorker), secretHealthSnapshotToken,
		secretPostgresMigratePassword, secretPostgresReaperPassword),
	// The bypass-RLS pool: reaper credentials, else migrate ones.
	RoleDispatcher: concat(sharedDepsSecrets(RoleDispatcher), secretHealthSnapshotToken,
		secretPostgresMigratePassword, secretPostgresReaperPassword),
	RoleIngest: concat(sharedDepsSecrets(RoleIngest), secretHealthSnapshotToken,
		secretPostgresMigratePassword, secretPostgresReaperPassword,
		secretIngestWebhook, secretIngestNATSToken, secretIngestRabbitMQURL),
	// No database: the bridge's DPoP key and its token-exchange client.
	RoleMCP: {secretMCPDPoPKey, secretMCPExchangeClient},
	// Only the token the admin role reads its health snapshot with.
	RoleSession: {secretHealthSnapshotToken},
	// The runtime pool (boot) and the migration role's own.
	RoleMigrate: {secretPostgresPassword, secretPostgresMigratePassword},
	// The runtime pool, the admin it creates, and the backends it probes.
	RoleBootstrap: {secretPostgresPassword, secretBootstrapAdminPassword, secretStorageBackends},
}

// runtimeSecretReaders read Secrets a name in data gives — a registered
// backend's credentials, an event sink's — from runtime_secrets.namespace:
// admin probes backends and tests sinks, the dispatcher delivers to sinks,
// bootstrap probes the backends it seeds.
var runtimeSecretReaders = []Role{RoleAdmin, RoleBootstrap, RoleDispatcher, RoleMCPEmbedded}

// The embedded bridge reads what the api, iam and admin planes and the
// bridge each read, every path once.
func init() {
	var union []string
	for _, r := range []Role{RoleAPI, RoleIAM, RoleAdmin, RoleMCP} {
		union = append(union, roleSecrets[r]...)
	}
	slices.Sort(union)
	roleSecrets[RoleMCPEmbedded] = slices.Compact(union)
}

// ParseRole is role when the binary knows it, else an error.
func ParseRole(role string) (Role, error) {
	r := Role(role)
	if _, ok := roleSecrets[r]; !ok {
		return "", fmt.Errorf("config: unknown role %q", role)
	}
	return r, nil
}

// reads reports whether r reads the binding at path: one of its paths, or a
// path under one of its families.
func (r Role) reads(path string) bool {
	for _, p := range roleSecrets[r] {
		if path == p || (strings.HasSuffix(p, ".") || strings.HasSuffix(p, "[")) && strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func concat(base []string, more ...string) []string {
	return append(slices.Clone(base), more...)
}
