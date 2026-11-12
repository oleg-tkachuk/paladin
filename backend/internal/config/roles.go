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
	RoleAPI        Role = "api"
	RoleAdmin      Role = "admin"
	RoleWorker     Role = "worker"
	RoleDispatcher Role = "dispatcher"
	RoleIngest     Role = "ingest"
	// RoleMCP is the standalone bridge: no database, only its own credentials.
	RoleMCP Role = "mcp"
	// RoleMCPEmbedded co-hosts the api and admin planes beside the bridge
	// (`serve mcp --embedded`), and reads what all three do.
	RoleMCPEmbedded Role = "mcp-embedded"
	RoleMigrate     Role = "migrate"
	RoleBootstrap   Role = "bootstrap"
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

// sharedDepsSecrets are what every role building the shared dependencies
// reads (app.BuildSharedDeps): its database pool and replica, every storage
// backend's credentials (registry warm-up), the API-token pepper and the DPoP
// nonce key.
var sharedDepsSecrets = []string{
	secretPostgresPassword, secretPostgresReplicaPassword, secretStorageBackends,
	secretAPITokenHMACKey, secretDPoPNonceKey,
}

// roleSecrets is the binding paths each role reads, from the code that
// consumes them. A role absent here is refused rather than given nothing.
var roleSecrets = map[Role][]string{
	// The planes: shared deps, the rate limits in Valkey, the health
	// snapshot; api also seeds the OAuth clients (AssembleAPIMuxes).
	RoleAPI:   concat(sharedDepsSecrets, secretValkeyPassword, secretHealthSnapshotToken, secretOAuthSeedClients),
	RoleAdmin: concat(sharedDepsSecrets, secretValkeyPassword, secretHealthSnapshotToken),
	// The partition pool is the migrate role's; the reaper pool its own.
	RoleWorker: concat(sharedDepsSecrets, secretHealthSnapshotToken,
		secretPostgresMigratePassword, secretPostgresReaperPassword),
	// The bypass-RLS pool: reaper credentials, else migrate ones.
	RoleDispatcher: concat(sharedDepsSecrets, secretHealthSnapshotToken,
		secretPostgresMigratePassword, secretPostgresReaperPassword),
	RoleIngest: concat(sharedDepsSecrets, secretHealthSnapshotToken,
		secretPostgresMigratePassword, secretPostgresReaperPassword,
		secretIngestWebhook, secretIngestNATSToken, secretIngestRabbitMQURL),
	// No database: the bridge's DPoP key and its token-exchange client.
	RoleMCP: {secretMCPDPoPKey, secretMCPExchangeClient},
	// The runtime pool (boot) and the migration role's own.
	RoleMigrate: {secretPostgresPassword, secretPostgresMigratePassword},
	// The runtime pool, the admin it creates, and the backends it probes.
	RoleBootstrap: {secretPostgresPassword, secretBootstrapAdminPassword, secretStorageBackends},
}

func init() {
	roleSecrets[RoleMCPEmbedded] = concat(roleSecrets[RoleAPI],
		append(slices.Clone(roleSecrets[RoleAdmin]), roleSecrets[RoleMCP]...)...)
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
