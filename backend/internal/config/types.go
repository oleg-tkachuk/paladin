package config

import (
	"encoding/json"
	"time"

	yaml "github.com/oasdiff/yaml3"
)

type Config struct {
	App    App    `yaml:"app" json:"app"`
	Logger Logger `yaml:"logger" json:"logger"`
	// Runtime holds process-wide HTTP-server flags shared by every
	// role (mode / shutdown_timeout / log_probes). Per-listener
	// addresses + timeouts + TLS live under api.server / admin.server
	// / worker.ops. Renamed from `server` to avoid collision with
	// the per-role server blocks.
	Runtime    Runtime    `yaml:"runtime" json:"runtime"`
	Datastores Datastores `yaml:"datastores" json:"datastores"`
	Limits     Limits     `yaml:"limits" json:"limits"`
	Auth       Auth       `yaml:"auth" json:"auth"`
	Security   Security   `yaml:"security" json:"security"`
	Bootstrap  Bootstrap  `yaml:"bootstrap" json:"bootstrap"`
	Middleware Middleware `yaml:"middleware" json:"middleware"`
	Worker     Worker     `yaml:"worker" json:"worker"`
	OTel       OTel       `yaml:"otel" json:"otel"`
	Storage    Storage    `yaml:"storage" json:"storage"`
	Cedar      Cedar      `yaml:"cedar" json:"cedar"`
	MCP        MCP        `yaml:"mcp" json:"mcp"`
	Ingest     Ingest     `yaml:"ingest" json:"ingest"`

	// Per-role blocks — own role-exclusive knobs (per-listener
	// addresses for now; future per-role middleware / limits hang
	// here too). Cross-cutting config (auth, security, datastores,
	// middleware defaults) stays at root.
	API        API        `yaml:"api" json:"api"`
	Admin      Admin      `yaml:"admin" json:"admin"`
	Capability Capability `yaml:"capability" json:"capability"`
	APIToken   APIToken   `yaml:"api_token" json:"api_token"`
	Dispatcher Dispatcher `yaml:"dispatcher" json:"dispatcher"`

	PodName string `yaml:"-"`
	Env     string `yaml:"-"`
}

// SecretRef represents a reference to a Kubernetes secret.
type SecretRef struct {
	Name      string `yaml:"name" json:"name"`
	Key       string `yaml:"key" json:"key"` // Defaults to "password" if empty
	Namespace string `yaml:"namespace" json:"namespace"`
}

// UnmarshalJSON parses either a string (secret name) or an object (full ref).
func (s *SecretRef) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		s.Name = str
		s.Key = DefaultSecretKey

		return nil
	}

	type rawSecretRef SecretRef
	if err := json.Unmarshal(data, (*rawSecretRef)(s)); err != nil {
		return err
	}
	if s.Key == "" {
		s.Key = DefaultSecretKey
	}

	return nil
}

// UnmarshalYAML parses either a string (secret name) or an object (full ref).
func (s *SecretRef) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		s.Name = value.Value
		s.Key = DefaultSecretKey

		return nil
	}

	type rawSecretRef SecretRef
	if err := value.Decode((*rawSecretRef)(s)); err != nil {
		return err
	}
	if s.Key == "" {
		s.Key = DefaultSecretKey
	}

	return nil
}

type App struct {
	Name string `yaml:"name" json:"name"`
	Env  string `yaml:"env" json:"env"`
}

type Logger struct {
	Level             string       `yaml:"level" json:"level"`
	Format            string       `yaml:"format" json:"format"`
	Development       bool         `yaml:"development" json:"development"`
	DisableCaller     bool         `yaml:"disable_caller" json:"disable_caller"`
	DisableStacktrace bool         `yaml:"disable_stacktrace" json:"disable_stacktrace"`
	Sampling          LogSampling  `yaml:"sampling" json:"sampling"`
	Fields            LoggerFields `yaml:"fields" json:"fields"`
}

type LogSampling struct {
	Enabled    bool `yaml:"enabled" json:"enabled"`
	Initial    int  `yaml:"initial" json:"initial"`
	Thereafter int  `yaml:"thereafter" json:"thereafter"`
}

type LoggerFields struct {
	Service string `yaml:"service" json:"service"`
	Env     string `yaml:"env" json:"env"`
}

// Runtime holds process-wide HTTP-server settings shared across every
// role's listener. Per-listener address / timeouts / TLS live under
// the per-service blocks:
//
//	api.server.data   — was server.data_http
//	api.server.iam    — was server.iam_http
//	admin.server      — was server.admin_http
//	worker.ops        — separate ops listener for the worker role
//
// Renamed from `Server` to disambiguate from `api.Server`,
// `admin.Server`, etc. — operators reading the YAML used to see
// two `server:` blocks at different nesting levels and miss the
// connection.
type Runtime struct {
	Mode            string        `yaml:"mode" json:"mode"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout" json:"shutdown_timeout"`
	LogProbes       bool          `yaml:"log_probes" json:"log_probes"`
	// HealthSnapshotToken gates the `/system/health.json` snapshot endpoint
	// (component tree + subsystem + Postgres reachability). Empty (default)
	// leaves it open — fine for dev. In prod, set a shared secret here and
	// on the frontend BFF aggregator (PALADIN_HEALTH_SNAPSHOT_TOKEN) so the
	// detail is only reachable with the token. The kubelet probe endpoints
	// (/livez /readyz /startupz) are NEVER gated.
	HealthSnapshotToken string `yaml:"health_snapshot_token" json:"health_snapshot_token"`
}

type HTTPServer struct {
	Addr               string        `yaml:"addr" json:"addr"`
	ReadHeaderTimeout  time.Duration `yaml:"read_header_timeout" json:"read_header_timeout"`
	ReadTimeout        time.Duration `yaml:"read_timeout" json:"read_timeout"`
	WriteTimeout       time.Duration `yaml:"write_timeout" json:"write_timeout"`
	IdleTimeout        time.Duration `yaml:"idle_timeout" json:"idle_timeout"`
	MaxHeaderBytes     int           `yaml:"max_header_bytes" json:"max_header_bytes"`
	MaxBodyBytes       int64         `yaml:"max_body_bytes" json:"max_body_bytes"`
	RequestIDHeader    string        `yaml:"request_id_header" json:"request_id_header"`
	RealIPHeader       string        `yaml:"real_ip_header" json:"real_ip_header"`
	TrustedProxies     []string      `yaml:"trusted_proxies" json:"trusted_proxies"`
	CORSAllowedOrigins []string      `yaml:"cors_allowed_origins" json:"cors_allowed_origins"`
	TLS                TLS           `yaml:"tls" json:"tls"`
}

type TLS struct {
	Enabled            bool   `yaml:"enabled" json:"enabled"`
	CertPath           string `yaml:"cert_path" json:"cert_path"`
	KeyPath            string `yaml:"key_path" json:"key_path"`
	CaPath             string `yaml:"ca_path" json:"ca_path"`
	ServerName         string `yaml:"server_name" json:"server_name"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify" json:"insecure_skip_verify"`
	// ClientAuth controls how server-side TLS listeners verify
	// inbound client certificates — the mTLS termination knob.
	// Only meaningful on server listeners; ignored when this TLS
	// struct is consumed as a client config.
	//
	// Values: "none" (default — one-way TLS, ignore client cert),
	// "request", "require", "permissive" (verify-if-given —
	// useful during rollout cutover so plaintext + mTLS callers
	// both work), "strict" (require-and-verify — full mTLS,
	// the destination posture).
	// See utils.ParseClientAuth for the mapping onto tls.ClientAuthType.
	ClientAuth string `yaml:"client_auth" json:"client_auth"`
}

type Datastores struct {
	Postgres Postgres `yaml:"postgres" json:"postgres"`
}

type Postgres struct {
	// DSN is the runtime connection string. The user portion of this DSN
	// should resolve to a minimum-privilege role (`paladin_app` by convention,
	// see migrations/011_app_role.sql). It must NOT carry DDL rights.
	DSN            string     `yaml:"dsn" json:"dsn"`
	Password       string     `yaml:"password" json:"password"`
	PasswordSecret *SecretRef `yaml:"password_secret" json:"password_secret"`

	// MigrateDSN is the connection used to apply schema migrations. When
	// empty, migrations run as the runtime DSN's user — fine for dev, but
	// production deploys MUST set this to a separate DDL-capable role
	// (`paladin_migrate` by convention) so the runtime role can be locked
	// down to DML only. See docs/db-roles.md.
	MigrateDSN            string     `yaml:"migrate_dsn" json:"migrate_dsn"`
	MigratePassword       string     `yaml:"migrate_password" json:"migrate_password"`
	MigratePasswordSecret *SecretRef `yaml:"migrate_password_secret" json:"migrate_password_secret"`

	// ReaperDSN is the connection the worker's cross-tenant background DML
	// jobs (purgers, lifecycle reapers, dispatcher outbox) use. Its user
	// should resolve to a dedicated least-privilege role (`paladin_reaper` by
	// convention, migration 058): BYPASSRLS — so the jobs see every tenant's
	// rows with no per-request GUC — but DML-only, no DDL/ownership. When
	// empty the worker falls back to MigrateDSN (dev parity). The one DDL
	// background job (PartitionMaintainer) always runs on the migrate role,
	// never this one. See docs/db-roles.md.
	ReaperDSN            string     `yaml:"reaper_dsn" json:"reaper_dsn"`
	ReaperPassword       string     `yaml:"reaper_password" json:"reaper_password"`
	ReaperPasswordSecret *SecretRef `yaml:"reaper_password_secret" json:"reaper_password_secret"`

	Pool              PostgresPool     `yaml:"pool" json:"pool"`
	Timeouts          PostgresTimeouts `yaml:"timeouts" json:"timeouts"`
	HealthcheckPeriod time.Duration    `yaml:"healthcheck_period" json:"healthcheck_period"`
}

type PostgresPool struct {
	MaxConns        int32         `yaml:"max_conns" json:"max_conns"`
	MinConns        int32         `yaml:"min_conns" json:"min_conns"`
	MaxConnLifetime time.Duration `yaml:"max_conn_lifetime" json:"max_conn_lifetime"`
	MaxConnIdleTime time.Duration `yaml:"max_conn_idle_time" json:"max_conn_idle_time"`
}

type PostgresTimeouts struct {
	Connect   time.Duration `yaml:"connect" json:"connect"`
	Statement time.Duration `yaml:"statement" json:"statement"`
}

// Storage is the registry of physical object-storage backends. Each logical
// objectKey references one by name via `object_keys.backend_id`, and every
// write path (bucket / objectKey / dedicated-tenant creation) MUST name a
// backend explicitly — there is no implicit default. Silently defaulting where
// a tenant's bytes land is a footgun in a multi-backend world, so an omitted
// backend is an error, not a fallback.
type Storage struct {
	Backends map[string]StorageBackend `yaml:"backends" json:"backends"`
}

// Limits collects all request-shape constraints the data plane enforces:
// object/part size, allowed MIME types, metadata-cardinality caps, and the
// presign-URL ttls + size cap. Lives under one top-level section so an
// operator has a single place to look for "what does the service refuse?".
type Limits struct {
	MaxObjectSizeRaw      string   `yaml:"max_object_size" json:"max_object_size"`
	MaxObjectSizeBytes    int64    `yaml:"-" json:"-"`
	MaxMultipartSizeRaw   string   `yaml:"max_multipart_size" json:"max_multipart_size"`
	MaxMultipartSizeBytes int64    `yaml:"-" json:"-"`
	MinPartSizeRaw        string   `yaml:"min_part_size" json:"min_part_size"`
	MinPartSizeBytes      int64    `yaml:"-" json:"-"`
	MaxPartSizeRaw        string   `yaml:"max_part_size" json:"max_part_size"`
	MaxPartSizeBytes      int64    `yaml:"-" json:"-"`
	MaxParts              int      `yaml:"max_parts" json:"max_parts"`
	AllowedContentTypes   []string `yaml:"allowed_content_types" json:"allowed_content_types"`
	Presign               Presign  `yaml:"presign" json:"presign"`
}

// Auth configures JWT verification + minting. v2 issues tokens itself via
// AuthService.Login / RefreshToken; the same SigningKey is used for both
// signing (issuer) and verification (interceptor on each plane).
//
// JWKSURL is reserved for federated-IdP scenarios (slice 6+); v1 uses HMAC
// only and rejects unset SigningKey at startup.
type Auth struct {
	Issuer            string        `yaml:"issuer" json:"issuer"`
	JWKSURL           string        `yaml:"jwks_url" json:"jwks_url"`
	SigningKey        string        `yaml:"signing_key" json:"signing_key"`
	SigningKeySecret  *SecretRef    `yaml:"signing_key_secret" json:"signing_key_secret"`
	Leeway            time.Duration `yaml:"leeway" json:"leeway"`
	AccessTokenTTL    time.Duration `yaml:"access_token_ttl" json:"access_token_ttl"`
	RefreshTokenTTL   time.Duration `yaml:"refresh_token_ttl" json:"refresh_token_ttl"`
	ScopedTokenMaxTTL time.Duration `yaml:"scoped_token_max_ttl" json:"scoped_token_max_ttl"`

	// LoginRateLimitPerSubjectPerMinute caps credential-bearing IAM RPCs
	// (Login + RefreshToken) per (subject, IP) per minute — the credential-
	// stuffing defence. <= 0 keeps the built-in default (10); set a high value
	// to effectively disable (e.g. an e2e stack that logs in repeatedly as one
	// account). Mirrors auth.oauth.token_rate_limit_per_minute.
	LoginRateLimitPerSubjectPerMinute int `yaml:"login_rate_limit_per_subject_per_minute" json:"login_rate_limit_per_subject_per_minute"`
	// LoginRateLimitPerIPPerMinute caps the same RPCs per source IP per minute
	// — the subject-enumeration defence. <= 0 keeps the built-in default (60).
	LoginRateLimitPerIPPerMinute int `yaml:"login_rate_limit_per_ip_per_minute" json:"login_rate_limit_per_ip_per_minute"`

	// OAuth turns IAM into an OAuth 2.1 Authorization Server (ADR-0009):
	// the /oauth/authorize + /oauth/token + /oauth/register endpoints that
	// mint the bearers the MCP Resource Server (ADR-0008) validates.
	// Disabled by default.
	OAuth OAuthAS `yaml:"oauth" json:"oauth"`
}

// OAuthAS configures the IAM-hosted OAuth 2.1 Authorization Server (ADR-0009).
// Tokens are minted by the same issuer.Issuer the login path uses, so the
// SigningKey / AccessTokenTTL / RefreshTokenTTL on the parent Auth block
// apply; this block adds only the OAuth-specific knobs.
type OAuthAS struct {
	// Enabled mounts the /oauth/* endpoints. Off → IAM serves only the
	// existing Connect AuthService.
	Enabled bool `yaml:"enabled" json:"enabled"`
	// DynamicRegistration gates POST /oauth/register (RFC 7591). When false,
	// only pre-seeded clients (SeedClients) can be used.
	DynamicRegistration bool `yaml:"dynamic_registration" json:"dynamic_registration"`
	// AuthorizationCodeTTL bounds how long an issued code is valid before
	// the token exchange must happen. Spec recommends ≤10m; we default 60s.
	AuthorizationCodeTTL time.Duration `yaml:"authorization_code_ttl" json:"authorization_code_ttl"`
	// AllowedRedirectSchemes restricts registered redirect_uri schemes
	// (e.g. https, claude-desktop, cursor). Empty → https only.
	AllowedRedirectSchemes []string `yaml:"allowed_redirect_schemes" json:"allowed_redirect_schemes"`
	// ConsentURL, when set, is the front-end consent page the /authorize GET
	// redirects to (after validating the request) instead of server-rendering
	// its built-in HTML form. The page collects credentials + the Allow/Deny
	// decision and POSTs them back to /oauth/authorize. Empty → use the
	// built-in server-rendered consent (works standalone, no front-end).
	ConsentURL string `yaml:"consent_url" json:"consent_url"`
	// TokenRateLimitPerMinute caps /oauth/token requests per client_id
	// (in-memory token bucket, per pod) to blunt code/secret brute-forcing.
	// <= 0 falls back to a built-in default (60/min); set a high value to
	// effectively disable.
	TokenRateLimitPerMinute int `yaml:"token_rate_limit_per_minute" json:"token_rate_limit_per_minute"`
	// TokenEndpointAllowedOrigins enables CORS on /oauth/token for
	// browser-based public clients: an Origin in this list (or "*") gets the
	// Access-Control-Allow-Origin reply and OPTIONS preflight handling. Empty
	// → no CORS headers (server-to-server / native-form clients need none).
	TokenEndpointAllowedOrigins []string `yaml:"token_endpoint_allowed_origins" json:"token_endpoint_allowed_origins"`
	// SeedClients are first-party clients registered at boot so a fresh
	// deployment works without DCR. Keyed by client_id.
	SeedClients []OAuthSeedClient `yaml:"seed_clients" json:"seed_clients"`
}

// OAuthSeedClient is a pre-registered (usually public / PKCE) OAuth client.
type OAuthSeedClient struct {
	ClientID         string   `yaml:"client_id" json:"client_id"`
	RedirectURIs     []string `yaml:"redirect_uris" json:"redirect_uris"`
	AllowedScopes    []string `yaml:"allowed_scopes" json:"allowed_scopes"`
	AllowedAudiences []string `yaml:"allowed_audiences" json:"allowed_audiences"`
	Public           bool     `yaml:"public" json:"public"`
	// SkipConsent pre-authorizes this client: the operator trusts it (a
	// first-party app like claude-desktop), so /authorize goes straight to
	// login without the per-user consent screen. Only configurable here, never
	// via dynamic registration — a self-registered client can never skip
	// consent.
	SkipConsent bool `yaml:"skip_consent" json:"skip_consent"`
}

type Security struct {
	// trust_tenant_id_from_request used to live here but was dead code —
	// the field was declared but never read anywhere in the Go tree, and
	// the only consumer of the X-Tenant-Id header (AuthService.Login)
	// uses it unconditionally as a tenant-disambiguation hint while
	// still requiring a valid password. Removed in the post-2026-05
	// security audit. Operators relying on it for trust-bypass were
	// never actually getting that behaviour — Login was always
	// password-gated.
	RejectTenantMismatch bool `yaml:"reject_tenant_mismatch" json:"reject_tenant_mismatch"`
	LogSensitive         bool `yaml:"log_sensitive" json:"log_sensitive"`

	// RLS is intentionally not configurable here. Migration 023
	// enables per-table policies unconditionally; the runtime always
	// installs the PrepareConn hook that stamps paladin.tenant_id GUC
	// (cmd/server/common.go). Operator-visible knob would only
	// surface a footgun (every "off" position breaks writes since
	// paladin_app is NOBYPASSRLS by design). See migrations/023_rls.sql
	// for the full role + policy matrix.
}

// Bootstrap groups one-shot startup steps that prepare the cluster for
// first-time use. Each step is opt-in (default disabled) and idempotent —
// the server can restart freely without re-creating the same state.
type Bootstrap struct {
	Admin BootstrapAdmin `yaml:"admin" json:"admin"`
}

// BootstrapAdmin provisions a platform-admin user from a Kubernetes Secret
// on first startup, ArgoCD-style. The Secret is created by the Helm chart
// (templates/secret-bootstrap-admin.yaml) — operators don't pre-create it
// and never put the password in YAML. The dedicated tenant is created if
// missing so the admin has a home; `platform.admin` role makes it
// cross-tenant via Cedar.
//
// Flow:
//
//  1. enabled=false (default): no-op.
//  2. enabled=true, user missing: create tenant if missing, hash the
//     password, INSERT into iam.users, write an audit entry.
//  3. enabled=true, user exists, force_reset=false: log + skip.
//  4. enabled=true, user exists, force_reset=true: rotate password_hash,
//     write an audit entry, log a WARN.
//
// `force_reset=true` is meant for one-off rotations via Helm upgrade. Flip
// it back to false in the next deploy or the password keeps getting reset
// on every restart (logged but otherwise harmless — bcrypt cost dominates).
type BootstrapAdmin struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Subject is the login identifier (`subject` claim / Login.subject).
	Subject string `yaml:"subject" json:"subject"`
	// TenantSlug + TenantDisplayName describe the dedicated tenant the
	// admin lives in. The tenant is created on first run if absent.
	TenantSlug        string `yaml:"tenant_slug" json:"tenant_slug"`
	TenantDisplayName string `yaml:"tenant_display_name" json:"tenant_display_name"`
	// DisplayName is the user's display_name (cosmetic, distinct from the
	// tenant name). Empty falls back to Subject.
	DisplayName string `yaml:"display_name" json:"display_name"`
	// Roles attached to the admin user. Default ["platform.admin"]. Cedar
	// uses the dot form; do not use hyphens.
	Roles []string `yaml:"roles" json:"roles"`
	// Password is the bootstrap password. In a Kubernetes deployment it is
	// resolved at boot from PasswordSecret; locally an operator may set it
	// inline (debug mode only). Never logged.
	Password string `yaml:"password" json:"password"`
	// PasswordSecret references the Kubernetes Secret holding the password.
	// Resolved at boot by K8sSecretResolver — same pattern as the postgres
	// password_secret.
	PasswordSecret *SecretRef `yaml:"password_secret" json:"password_secret"`
	// MinPasswordLength rejects shorter passwords on startup. Default 16
	// (server.mode=release) / 8 (debug, test).
	MinPasswordLength int `yaml:"min_password_length" json:"min_password_length"`
	// ForceReset rotates the existing user's password_hash on every boot
	// while true. Flip back to false after the rotation has propagated.
	ForceReset bool `yaml:"force_reset" json:"force_reset"`
}

// Workers groups every background-loop subsystem under a single section
// so an operator looking for "what runs in the background?" finds them
// in one place.
// API is the per-role config block for the api binary (`serve api`).
// Owns the data + iam listener configs that used to live as
// server.data_http / server.iam_http; future role-exclusive limits
// and per-listener middleware hang here too.
type API struct {
	Server APIServer `yaml:"server" json:"server"`
}

// APIServer holds the api role's two listeners. Same HTTPServer shape
// as the rest; just two named entries because the api binary opens
// data + iam on different ports under one process.
type APIServer struct {
	Data HTTPServer `yaml:"data" json:"data"`
	IAM  HTTPServer `yaml:"iam" json:"iam"`
}

// Admin is the per-role config block for the admin binary
// (`serve admin`). Currently a single listener; the block exists so
// future admin-only knobs (audit-log rate limits, stricter timeouts,
// dedicated trusted-proxy list) hang here without further nesting.
type Admin struct {
	Server HTTPServer `yaml:"server" json:"server"`
}

// Worker is the per-role config block for the worker binary. Carries
// the ops listener (probes / observability) and the background-job
// catalog under `jobs:`.
type Worker struct {
	// Ops is the worker's HTTP listener for /healthz + /readyz +
	// future ops endpoints. Defaults to :8090 to match the chart's
	// containerPort. Was server.admin_http re-read in the legacy
	// flat config.
	Ops HTTPServer `yaml:"ops" json:"ops"`
	// OpsURL is where OTHER pods reach that ops listener — the admin
	// plane's SystemService.GetPlatformStats proxies
	// <ops_url>/system/rls-census.json from it, because objects, quotas,
	// capability_records, api_tokens and event_subscriptions are all RLS'd
	// and only the worker holds a BYPASSRLS pool. Cluster-internal Service
	// URL (e.g. "http://paladin-core-worker:8099"); empty disables the proxy
	// and the RPC reports rls.available=false. Same shape and contract as
	// Dispatcher.OpsURL.
	OpsURL string     `yaml:"ops_url" json:"ops_url"`
	Jobs   WorkerJobs `yaml:"jobs" json:"jobs"`
}

// WorkerJobs is the catalog of background-job configs the worker
// binary consumes. Renamed from `Workers` (top-level) when the config
// migrated to per-service blocks; struct field-set is unchanged.
type WorkerJobs struct {
	Reconciler       Reconciler       `yaml:"reconciler" json:"reconciler"`
	Housekeeping     Housekeeping     `yaml:"housekeeping" json:"housekeeping"`
	RefreshTokenReap RefreshTokenReap `yaml:"refresh_token_reap" json:"refresh_token_reap"`
	Lifecycle        Lifecycle        `yaml:"lifecycle" json:"lifecycle"`
	Replication      Replication      `yaml:"replication" json:"replication"`
	Capability       CapabilityWorker `yaml:"capability" json:"capability"`
	APIToken         APITokenWorker   `yaml:"api_token" json:"api_token"`
	Operations       OperationsWorker `yaml:"operations" json:"operations"`
	QuotaReconcile   QuotaReconcile   `yaml:"quota_reconcile" json:"quota_reconcile"`
	PurgeDrain       PurgeDrain       `yaml:"purge_drain" json:"purge_drain"`
}

// PurgeDrain reclaims bytes owed by permanent deletes whose synchronous
// storage delete failed (pending_purges, migration 069).
//
// Effectively mandatory wherever permanent delete is reachable: the objects
// row is gone by the time the debt exists, so this loop is the only remaining
// path to reclaiming those bytes and the only emitter of paladin.object.purged
// for that path. interval=0 disables it and reinstates the leak.
type PurgeDrain struct {
	Interval time.Duration `yaml:"interval" json:"interval"`
	// MaxBackoff caps the per-row retry curve (doubling from 1m). Debt is
	// never discarded — a permanently failing row keeps retrying at this
	// cadence so the backlog stays visible instead of being dropped.
	MaxBackoff time.Duration `yaml:"max_backoff" json:"max_backoff"`
	BatchSize  int           `yaml:"batch_size" json:"batch_size"`
}

// QuotaReconcile recomputes the `quotas` usage columns from live objects
// and rolls the per-day admission counters at the UTC day boundary.
//
// Not optional in spirit: middleware.QuotaSoftCheck rejects uploads
// against usage_total_bytes / usage_object_count, and the upload path only
// ever increments them (best-effort, never decremented on delete). Without
// this job a tenant that deletes what it uploaded stays counted and
// eventually cannot write. interval=0 disables it — only appropriate for a
// deployment that sets no quotas at all.
type QuotaReconcile struct {
	Interval time.Duration `yaml:"interval" json:"interval"`
}

// Dispatcher is the per-role config block for the `serve dispatcher`
// binary — the durable webhook fan-out loop introduced by migration
// 028 (event_deliveries outbox). Producer (admin pod) writes rows;
// this loop consumes them.
//
// Per-knob notes:
//
//   - PollInterval — idle-loop sleep when no rows are ready. Hot
//     spikes burn down without polling pressure (the loop reschedules
//     immediately when a batch returns rows); a 1s tick is the cost of
//     a cold queue.
//   - BatchSize — rows pulled per FOR UPDATE SKIP LOCKED scan. Larger
//     batches amortise the tx round-trip but hold row locks longer
//     while the loop processes them serially. 50 is a safe default
//     for the HTTP-bound delivery profile.
//   - BaseBackoff / MaxBackoff — per-row retry curve. Doubles per
//     attempt up to MaxBackoff. Lifted from the prior in-process
//     defaults; tuned at runtime if a noisy customer dominates.
//   - DefaultMaxAttempts — retry budget when the sub's
//     HttpSink.MaxAttempts is unset. Beyond this, the row flips to
//     status='failed' and the queue stops touching it.
type Dispatcher struct {
	// Ops is the dispatcher pod's HTTP listener for /healthz +
	// /readyz + /system/health.json. Defaults to :8099 — same shape
	// as the worker's ops listener but a different role tag.
	Ops HTTPServer `yaml:"ops" json:"ops"`
	// OpsURL is where OTHER pods reach that ops listener — the admin
	// plane's SystemService.GetDispatcherStats proxies
	// <ops_url>/system/dispatcher-stats.json from it. Cluster-internal
	// Service URL (e.g. "http://paladin-dispatcher:8099");
	// empty disables the proxy (the RPC reports available=false).
	OpsURL             string        `yaml:"ops_url" json:"ops_url"`
	PollInterval       time.Duration `yaml:"poll_interval" json:"poll_interval"`
	BatchSize          int           `yaml:"batch_size" json:"batch_size"`
	BaseBackoff        time.Duration `yaml:"base_backoff" json:"base_backoff"`
	MaxBackoff         time.Duration `yaml:"max_backoff" json:"max_backoff"`
	DefaultMaxAttempts int           `yaml:"default_max_attempts" json:"default_max_attempts"`

	// ChargeEventsEnabled fans out one paladin.capability.charged event
	// per successful capability.UsageStore[pgx.Tx].Charge. Default OFF —
	// every chargeable RPC fires, so the cardinality multiplies the
	// outbox volume by the per-tenant request rate. Subscribers MUST
	// set a CEL filter pinning `event.kind == 'paladin.capability.charged'`
	// (or just dropping events on the floor at the broker) before
	// flipping this on for a noisy tenant.
	ChargeEventsEnabled bool `yaml:"charge_events_enabled" json:"charge_events_enabled"`

	// AuditMirrorEnabled mirrors every audit_log row to
	// paladin.audit.<action>. Default OFF — designed for SIEM /
	// compliance pipelines that already accept high-volume event
	// streams. Even noisier than ChargeEventsEnabled because every
	// mutation is logged; only flip on with a downstream consumer
	// already in place.
	AuditMirrorEnabled bool `yaml:"audit_mirror_enabled" json:"audit_mirror_enabled"`
}

// OperationsWorker drives the long-running operation queue
// (BatchDelete / BatchCopy / BatchUpdateTags / BatchRestoreObjects).
// Handlers in internal/api/v1/batch enqueue rows; this worker
// dequeues and runs them. Disable by setting interval to 0 — but
// note that calling BatchXxx RPCs without a runner stages
// PENDING operations that nothing will ever complete.
type OperationsWorker struct {
	// Interval is the tick rate. The runner drains aggressively per
	// tick (up to 100 ops), so this can be a few seconds — backlog
	// burns down quickly without polling pressure.
	Interval time.Duration `yaml:"interval" json:"interval"`
}

// CapabilityWorker drops capability_revocations rows for tokens whose
// underlying capability has been expired for at least `expired_for`.
// Keeps the denylist bounded; the verifier doesn't notice (an expired
// row can never match a verifying token by definition). Disable by
// setting interval to 0.
type CapabilityWorker struct {
	Interval   time.Duration `yaml:"interval" json:"interval"`
	ExpiredFor time.Duration `yaml:"expired_for" json:"expired_for"`
}

// APITokenWorker drops api_tokens rows whose expires_at is past
// expired_for. Bounded table over time; verifier correctness is
// unchanged (an expired token can never satisfy the time gate).
// Disable by setting interval to 0.
type APITokenWorker struct {
	Interval   time.Duration `yaml:"interval" json:"interval"`
	ExpiredFor time.Duration `yaml:"expired_for" json:"expired_for"`
}

// RefreshTokenReap drops expired refresh-token rows. Always on; tune
// `interval` based on token issuance volume.
type RefreshTokenReap struct {
	Interval time.Duration `yaml:"interval" json:"interval"`
}

// Lifecycle runs CEL-based expiration rules per bucket. Optional —
// disable when no buckets carry lifecycle rules.
type Lifecycle struct {
	Enabled  bool          `yaml:"enabled" json:"enabled"`
	Interval time.Duration `yaml:"interval" json:"interval"`
}

// Replication copies objects between backends per BucketReplication.
// Optional and currently dry-run unless a real StorageReplicator is wired
// in — `enabled: false` skips even the planning loop.
type Replication struct {
	Enabled        bool          `yaml:"enabled" json:"enabled"`
	Interval       time.Duration `yaml:"interval" json:"interval"`
	LookbackWindow time.Duration `yaml:"lookback_window" json:"lookback_window"`
}

type Housekeeping struct {
	PendingTTL   time.Duration `yaml:"pending_ttl" json:"pending_ttl"`
	MultipartTTL time.Duration `yaml:"multipart_ttl" json:"multipart_ttl"`
	AuditLogTTL  time.Duration `yaml:"audit_log_ttl" json:"audit_log_ttl"`
	// OperationsTTL bounds how long a terminal-state operation row is
	// retained. 0 disables the reaper. See [worker.OperationsReaper].
	OperationsTTL time.Duration `yaml:"operations_ttl" json:"operations_ttl"`

	// HardDeleteAfter is the cooling-off window between soft-delete
	// (row state='DELETED') and hard-delete (S3 DELETE + DB row
	// removal). 0 disables the LifecycleHardDeleter — the row stays
	// DELETED forever, useful for an audit-only deployment.
	//
	// Default 0 (off); recommended 7d-30d in prod so support has a
	// restore window. The worker's race-with-PUT mitigation is OCC-
	// gated; see [worker.LifecycleHardDeleter] for the failure-mode
	// matrix.
	HardDeleteAfter time.Duration `yaml:"hard_delete_after" json:"hard_delete_after"`
	// HardDeleteBatchSize caps rows per sweep. 0 → 100.
	HardDeleteBatchSize int32 `yaml:"hard_delete_batch_size" json:"hard_delete_batch_size"`

	Interval            time.Duration `yaml:"interval" json:"interval"`
	DeleteOrphanedParts bool          `yaml:"delete_orphaned_parts" json:"delete_orphaned_parts"`
}

// Middleware bundles the four cross-cutting interceptor knobs so an
// operator has a single section to scan when tuning request-shape
// behaviour: per-handler timeouts, sliding-window rate limits, the
// resource-cache TTL, and idempotency-key retention.
type Middleware struct {
	Timeouts    Timeouts    `yaml:"timeouts" json:"timeouts"`
	RateLimit   RateLimit   `yaml:"rate_limit" json:"rate_limit"`
	Cache       Cache       `yaml:"cache" json:"cache"`
	Idempotency Idempotency `yaml:"idempotency" json:"idempotency"`
}

type RateLimit struct {
	Enabled           bool          `yaml:"enabled" json:"enabled"`
	RequestsPerSecond float64       `yaml:"requests_per_second" json:"requests_per_second"`
	Burst             int           `yaml:"burst" json:"burst"`
	MaxTenants        int           `yaml:"max_tenants" json:"max_tenants"`
	CleanupTTL        time.Duration `yaml:"cleanup_ttl" json:"cleanup_ttl"`
	CleanupInterval   time.Duration `yaml:"cleanup_interval" json:"cleanup_interval"`
}

type OTel struct {
	Enabled  bool         `yaml:"enabled" json:"enabled"`
	Endpoint string       `yaml:"endpoint" json:"endpoint"`
	Protocol string       `yaml:"protocol" json:"protocol"`
	Insecure bool         `yaml:"insecure" json:"insecure"`
	Resource OTelResource `yaml:"resource" json:"resource"`
}

type OTelResource struct {
	ServiceName           string `yaml:"service.name" json:"service.name"`
	DeploymentEnvironment string `yaml:"deployment.environment" json:"deployment.environment"`
}

type Cache struct {
	Enabled bool          `yaml:"enabled" json:"enabled"`
	MaxSize int           `yaml:"max_size" json:"max_size"`
	TTL     time.Duration `yaml:"ttl" json:"ttl"`
}

type Timeouts struct {
	FastOperation    time.Duration `yaml:"fast_operation" json:"fast_operation"`
	DefaultOperation time.Duration `yaml:"default_operation" json:"default_operation"`
	S3Operation      time.Duration `yaml:"s3_operation" json:"s3_operation"`
	LongOperation    time.Duration `yaml:"long_operation" json:"long_operation"`
}

type Idempotency struct {
	Enabled bool          `yaml:"enabled" json:"enabled"`
	TTL     time.Duration `yaml:"ttl" json:"ttl"`
}

// StorageBackend describes one physical object-storage backend: connection
// params, credentials, SSE policy, upload-part sizing, and the event pipeline
// that drives CompletionMode (IMPLICIT when events.enabled, EXPLICIT otherwise).
type StorageBackend struct {
	Kind string `yaml:"kind" json:"kind"` // aws-s3 | s3-compatible | gcs
	// Provider is the vendor/implementation behind Kind — a free-form slug
	// ("garage" | "seaweedfs" | "minio" | "aws" | "gcp" | "digitalocean" | …).
	// Optional: Kind is too coarse (every self-hosted S3 is "s3-compatible"),
	// so Provider records which one for UI display. Mirrored into
	// storage_backends.provider by the bootstrap reconciler.
	Provider string `yaml:"provider" json:"provider"`
	// Bucket is the physical S3 bucket name. Paladin "ObjectKey" entries
	// become a tenant-scoped prefix within this bucket; the full S3 key
	// for any object is "<tenant_id>/<object_key>/<key>".
	Bucket         string               `yaml:"bucket" json:"bucket"`
	Region         string               `yaml:"region" json:"region"`
	Endpoint       string               `yaml:"endpoint" json:"endpoint"`
	PublicEndpoint string               `yaml:"public_endpoint" json:"public_endpoint"`
	ForcePathStyle bool                 `yaml:"force_path_style" json:"force_path_style"`
	Auth           StorageBackendAuth   `yaml:"auth" json:"auth"`
	PartSizeRaw    string               `yaml:"part_size" json:"part_size"`
	PartSizeBytes  int64                `yaml:"-" json:"-"`
	SSE            StorageBackendSSE    `yaml:"sse" json:"sse"`
	Events         StorageBackendEvents `yaml:"events" json:"events"`
}

// StorageBackendAuth selects how the Paladin control plane authenticates to a
// physical S3 backend. Only one mode is active per backend.
//
// Modes:
//   - "static_keys"   — long-lived IAM user access key + secret. Required
//     fields: AccessKey, SecretKey (or *Secret variants). Optional
//     SessionToken for short-lived creds minted out-of-band (e.g. by an
//     external broker).
//   - "default_chain" — defer to the AWS SDK default credential chain.
//     Picks up env vars (AWS_ACCESS_KEY_ID, ...), ECS task role
//     (AWS_CONTAINER_CREDENTIALS_*), EC2 IMDSv2 instance role, and the
//     AWS_PROFILE / shared-credentials file. The right choice for
//     EC2 / ECS / Fargate / on-prem with env-injected creds.
//   - "assume_role"   — assume an IAM role via STS. Bootstrap credentials
//     come from the default chain (so this layers on top of an EC2 role
//     or env-keys). Required: RoleARN. Optional: SessionName,
//     ExternalID, DurationSeconds.
//   - "web_identity"  — assume a role via STS AssumeRoleWithWebIdentity,
//     which is the canonical EKS IRSA path. Required: RoleARN. The token
//     file is read from WebIdentityTokenFile (or the
//     AWS_WEB_IDENTITY_TOKEN_FILE env var the EKS pod identity webhook
//     injects). The right choice for pods running in EKS with IRSA
//     annotations.
type StorageBackendAuth struct {
	Mode string `yaml:"mode" json:"mode"`

	// Static-keys credentials (mode=static_keys).
	AccessKey       string     `yaml:"access_key" json:"access_key"`
	AccessKeySecret *SecretRef `yaml:"access_key_secret" json:"access_key_secret"`
	SecretKey       string     `yaml:"secret_key" json:"secret_key"`
	SecretKeySecret *SecretRef `yaml:"secret_key_secret" json:"secret_key_secret"`

	// SessionToken is optional for static_keys (e.g. short-lived
	// credentials handed in by an external rotator).
	SessionToken       string     `yaml:"session_token" json:"session_token"`
	SessionTokenSecret *SecretRef `yaml:"session_token_secret" json:"session_token_secret"`

	// Role-assumption fields (assume_role + web_identity).
	RoleARN         string `yaml:"role_arn" json:"role_arn"`
	SessionName     string `yaml:"session_name" json:"session_name"`
	ExternalID      string `yaml:"external_id" json:"external_id"`
	DurationSeconds int    `yaml:"duration_seconds" json:"duration_seconds"`

	// Web-identity-only: path to the OIDC token file. Defaults to
	// AWS_WEB_IDENTITY_TOKEN_FILE env when empty (EKS IRSA convention).
	WebIdentityTokenFile string `yaml:"web_identity_token_file" json:"web_identity_token_file"`
}

const (
	AuthModeStaticKeys   = "static_keys"
	AuthModeDefaultChain = "default_chain"
	AuthModeAssumeRole   = "assume_role"
	AuthModeWebIdentity  = "web_identity"
)

type StorageBackendSSE struct {
	Type  string `yaml:"type" json:"type"`     // "" | AES256 | aws:kms
	KeyID string `yaml:"key_id" json:"key_id"` // required when type=aws:kms
}

type StorageBackendEvents struct {
	Enabled      bool          `yaml:"enabled" json:"enabled"`
	Target       string        `yaml:"target" json:"target"` // sqs | redis | none
	QueueURL     string        `yaml:"queue_url" json:"queue_url"`
	PollInterval time.Duration `yaml:"poll_interval" json:"poll_interval"`
}

// Presign holds presign-URL knobs. Nested under Limits since the URL TTLs
// and the body-size cap are both request-shape constraints.
type Presign struct {
	PutTTL         time.Duration `yaml:"put_ttl" json:"put_ttl"`
	GetTTL         time.Duration `yaml:"get_ttl" json:"get_ttl"`
	PartTTL        time.Duration `yaml:"part_ttl" json:"part_ttl"`
	DefaultTTL     time.Duration `yaml:"default_ttl" json:"default_ttl"`
	MaxTTL         time.Duration `yaml:"max_ttl" json:"max_ttl"`
	DefaultMaxSize int64         `yaml:"default_max_size" json:"default_max_size"`
}

type Reconciler struct {
	// Interval between reconcile ticks.
	Interval time.Duration `yaml:"interval" json:"interval"`
	// MinObjectAge: an object must be at least this old to be eligible for
	// reconciliation — protects against racing the upload path before the
	// client has called Complete.
	MinObjectAge time.Duration `yaml:"min_object_age" json:"min_object_age"`
	// BatchSize caps how many stuck-PENDING rows are reconciled per tick.
	BatchSize int `yaml:"batch_size" json:"batch_size"`
}

type Cedar struct {
	PolicyCacheTTL time.Duration `yaml:"policy_cache_ttl" json:"policy_cache_ttl"`
	// CanonicalObjectKeyEUID switches the Cedar ObjectKey entity UID from the
	// legacy `{tenant_uuid}/{object_key}` form to the canonical A-shape name
	// (ADR-0010, Phase 1). Default false. Only applies where (backend, bucket)
	// are in scope on the authz request; attribute/parent-based policies are
	// unaffected by the UID string. Flip per-environment only after confirming
	// no policy hardcodes a `resource == ObjectKey::"…"` literal.
	CanonicalObjectKeyEUID bool `yaml:"canonical_object_key_euid" json:"canonical_object_key_euid"`
}

// MCP gates the Model Context Protocol bridges (the LLM-facing entry points).
// Each transport flavour (stdio for local IDE plugins, streamable HTTP for
// remote LLM platforms) is opt-out via `enabled: false` so an operator can
// e.g. ship a stdio-only build to laptops while disabling the HTTP server
// entirely in production.
type MCP struct {
	Upstreams MCPUpstreams `yaml:"upstreams" json:"upstreams"`
	Stdio     MCPStdio     `yaml:"stdio" json:"stdio"`
	HTTP      MCPHTTP      `yaml:"http" json:"http"`

	// Profiles defines named tool allow-lists. Each transport (stdio/http)
	// picks one by name. Empty map → built-in defaults are used (see
	// internal/mcp/profile.go: DefaultProfiles). User-supplied entries
	// override built-ins of the same name. Tool-name patterns support
	// trailing-* wildcard, e.g. "paladin_list_*".
	Profiles map[string]MCPProfile `yaml:"profiles" json:"profiles"`

	// AlwaysDeny is a global blacklist applied AFTER profile expansion
	// regardless of which profile a transport selects. It encodes the
	// "antithesis to capability model" set: tool names an agentic
	// runtime must never see in its catalog (Issue/Revoke its own
	// capability, mint API tokens, manage users, rewrite policies).
	// Tool-name patterns support trailing-* wildcard. Default list
	// (when empty) is the built-in DefaultAlwaysDeny.
	AlwaysDeny []string `yaml:"always_deny" json:"always_deny"`

	// OAuth turns the streamable-HTTP MCP server into a spec-compliant
	// OAuth 2.1 Resource Server (ADR-0008): it advertises where to
	// authenticate (RFC 9728 / RFC 8414 metadata) and challenges
	// unauthenticated requests with 401 + WWW-Authenticate. Disabled by
	// default — the legacy X-Paladin-Token header keeps working untouched.
	OAuth MCPOAuth `yaml:"oauth" json:"oauth"`
}

// MCPOAuth configures the MCP server's OAuth 2.1 Resource-Server posture
// (ADR-0008). The Authorization Server itself (the /authorize + /token +
// registration endpoints) is a separate, deferred phase; this block only
// makes the MCP server discoverable + enforce bearer auth at its edge.
type MCPOAuth struct {
	// Enabled gates the whole RS behaviour: the 401 challenge and the
	// metadata endpoints. When false the transport behaves exactly as
	// before (X-Paladin-Token, missing token → 400).
	Enabled bool `yaml:"enabled" json:"enabled"`
	// ResourceURL is this MCP server's OAuth resource identifier — the
	// canonical URL clients bind their token to (RFC 8707), e.g.
	// "https://paladin.example.com/mcp". Published as `resource` in the
	// protected-resource metadata and echoed in the WWW-Authenticate
	// challenge.
	ResourceURL string `yaml:"resource_url" json:"resource_url"`
	// AuthorizationServers lists the issuer URLs of the OAuth Authorization
	// Servers that can mint tokens for this resource (RFC 9728). A standard
	// MCP client fetches each AS's own metadata from these URLs. Typically
	// one entry: Paladin IAM, or a federated IdP.
	AuthorizationServers []string `yaml:"authorization_servers" json:"authorization_servers"`
	// ScopesSupported is advertised in the protected-resource metadata so
	// clients know which scopes to request. Optional.
	ScopesSupported []string `yaml:"scopes_supported" json:"scopes_supported"`
	// AuthorizationServer, when its Issuer is set, makes this process ALSO
	// serve RFC 8414 Authorization-Server metadata at
	// /.well-known/oauth-authorization-server — the Paladin-IAM-is-the-AS case
	// (same origin). Leave Issuer empty when delegating to an external IdP
	// that serves its own metadata. The endpoint paths it advertises are
	// the contract the deferred AS phase fulfils.
	AuthorizationServer MCPOAuthAS `yaml:"authorization_server" json:"authorization_server"`
}

// MCPOAuthAS is the RFC 8414 Authorization-Server metadata this process
// advertises when it is itself the AS. All fields are URLs; empty endpoint
// fields are omitted from the document.
type MCPOAuthAS struct {
	Issuer                string `yaml:"issuer" json:"issuer"`
	AuthorizationEndpoint string `yaml:"authorization_endpoint" json:"authorization_endpoint"`
	TokenEndpoint         string `yaml:"token_endpoint" json:"token_endpoint"`
	RegistrationEndpoint  string `yaml:"registration_endpoint" json:"registration_endpoint"`
	JWKSURI               string `yaml:"jwks_uri" json:"jwks_uri"`
}

// MCPProfile is a named tool allow-list. Tool-name patterns support a
// trailing `*` wildcard (e.g. "paladin_list_*"), and the literal "*" matches
// every registered tool. Deny entries take precedence over Allow.
type MCPProfile struct {
	Tools []string `yaml:"tools" json:"tools"`
	Deny  []string `yaml:"deny,omitempty" json:"deny,omitempty"`
}

// MCPUpstreams holds the Paladin plane URLs the MCP bridge dispatches to. They
// are not secrets; the per-request bearer token is what gates access.
type MCPUpstreams struct {
	AdminURL string `yaml:"admin_url" json:"admin_url"`
	DataURL  string `yaml:"data_url" json:"data_url"`
	IAMURL   string `yaml:"iam_url" json:"iam_url"`
}

// MCPStdio configures the local stdio bridge (Claude Desktop / Cursor /
// IDE plugins). Profile selects which tool catalog the binary registers
// — the safe default is "read_only" (read-only inspection tools only),
// "agent_safe" adds presign + tag mutations, "admin" exposes everything
// not in MCP.AlwaysDeny.
type MCPStdio struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Profile string `yaml:"profile" json:"profile"`
}

// MCPHTTP configures the streamable-HTTP bridge (remote LLM platforms).
// Higher blast radius than stdio; pin Profile conservatively (read_only
// or agent_safe) unless the deployment is behind mTLS and short-lived
// service-account tokens.
type MCPHTTP struct {
	Enabled        bool          `yaml:"enabled" json:"enabled"`
	Addr           string        `yaml:"addr" json:"addr"`
	Profile        string        `yaml:"profile" json:"profile"`
	SessionTimeout time.Duration `yaml:"session_timeout" json:"session_timeout"`
	// SessionsURL is the admin plane's view of the MCP server's /sessions
	// endpoint (e.g. "http://paladin-mcp:8095/sessions"). The admin
	// MCPInspectService.ListSessions proxies there, forwarding the caller's
	// admin JWT. Empty disables the proxy — ListSessions returns an empty
	// list. Admin→MCP is the reverse of the bridge's MCP→plane direction, so
	// this is a distinct URL from Upstreams.
	SessionsURL string `yaml:"sessions_url" json:"sessions_url"`
}

// Capability configures the agent-runtime authorisation primitive. See
// internal/capability for the package and migrations/016_capabilities.sql
// for the schema.
//
// In dev (`enabled: true`, no `signing_key_path` set) the boot path
// generates an ephemeral Ed25519 keypair so smoke tests work without
// pre-provisioned material. Production deploys mount the private key
// via Secret + readOnly volume; key rotation is graceful (multiple
// kids in the JWKS document while clients catch up).
type Capability struct {
	// Enabled gates the whole subsystem. When false, the issuer +
	// verifier are not built and the interceptor short-circuits to
	// "no capability supplied" (callers fall through to JWT auth).
	Enabled bool `yaml:"enabled" json:"enabled"`

	// IssuerName is placed in the `iss` claim of every minted token
	// and required to be in TrustedIssuers on the verifier side.
	// Conventional value: the Paladin deployment's external URL or a
	// stable label like "paladin-prod-eu".
	IssuerName string `yaml:"issuer_name" json:"issuer_name"`

	// TrustedIssuers is the set of `iss` values the verifier accepts.
	// Always include IssuerName; add others when federating across
	// Paladin instances.
	TrustedIssuers []string `yaml:"trusted_issuers" json:"trusted_issuers"`

	// SigningKeyPath is a filesystem path to the Ed25519 private key
	// (PEM-encoded PKCS#8). Empty in dev → ephemeral keypair generated
	// at boot. Production deploys mount the key via a Secret + readOnly
	// volume.
	SigningKeyPath string `yaml:"signing_key_path" json:"signing_key_path"`

	// SigningKeyKID is the key ID emitted in the JWT header. When
	// empty, the boot path derives one from the public key bytes.
	SigningKeyKID string `yaml:"signing_key_kid" json:"signing_key_kid"`

	// DefaultTTL caps Issue.TTL when the request omits it. Default 15
	// minutes — short enough that revocation propagation rarely matters.
	DefaultTTL time.Duration `yaml:"default_ttl" json:"default_ttl"`

	// VerifierLeeway is the clock-skew window applied to nbf / exp.
	// Default 30s; matches existing internal/auth.Auth.Leeway.
	VerifierLeeway time.Duration `yaml:"verifier_leeway" json:"verifier_leeway"`

	// ChargePerRequestAmount is the amount automatically charged
	// against the capability + tenant budgets for each "billable"
	// handler call (presign, complete object, batch op kick-off).
	// 0 = no automatic charge (default) — handlers still emit the
	// request-count bump, but the spend counter never moves.
	//
	// One uniform knob covers the typical "track per-call cost"
	// model. Operators who want per-handler differentiation extend
	// the call sites with explicit ChargeCapability(ctx, amt, unit)
	// invocations; this default is the "bare minimum so caveats
	// matter".
	//
	// Renamed from ChargePerRequest — same semantic, just no longer
	// USD-pinned in name.
	ChargePerRequestAmount float64 `yaml:"charge_per_request_amount" json:"charge_per_request_amount"`

	// ChargePerRequestUnit pins the currency / unit for the auto-
	// charge amount. Empty falls back to the capability's own
	// UnitCode (which itself defaults to "USD"). Set this when the
	// platform wants every billable call denominated in a specific
	// currency regardless of the capability's declared unit — e.g.
	// EUR-denominated metering on a tenant whose capabilities are
	// minted with the empty default.
	ChargePerRequestUnit string `yaml:"charge_per_request_unit" json:"charge_per_request_unit"`

	// RevocationCacheTTL is how long the verifier caches IsRevoked
	// answers. Default 2s; the SLO for revocation propagation. Set <0
	// to disable caching (every check hits the DB).
	RevocationCacheTTL time.Duration `yaml:"revocation_cache_ttl" json:"revocation_cache_ttl"`
}

// APIToken configures the hashed-bearer M2M token subsystem. Distinct
// from Capability (agent-runtime, JWT, short-lived) and from Auth
// (user authn via OIDC / HS256 bootstrap). See internal/auth/api_token
// for the package and migrations/017_api_tokens.sql for the schema.
//
// API tokens are long-lived service-to-service credentials following
// the hashed-bearer pattern (Hatchet / GitHub PATs / Stripe / GitLab
// reference implementations). Disabled by default — enable per-deploy
// once the admin RPC surface is rolled out and at least one issuance
// path (CLI or admin UI) is in place.
type APIToken struct {
	// Enabled gates the subsystem. When false, the issuer + verifier
	// are not built and the interceptor short-circuits — JWT / capability
	// auth still works.
	Enabled bool `yaml:"enabled" json:"enabled"`

	// MaxTTL caps the lifetime of any token Issue mints. 0 → 1 year
	// default (in the issuer). Service tokens shouldn't live forever;
	// if a customer needs a longer-lived secret they're using the
	// wrong primitive (consider OIDC client_credentials).
	MaxTTL time.Duration `yaml:"max_ttl" json:"max_ttl"`

	// VerifierLeeway widens the expires_at gate to absorb clock skew.
	// Default 30s.
	VerifierLeeway time.Duration `yaml:"verifier_leeway" json:"verifier_leeway"`

	// TouchLastUsed controls whether the verifier bumps last_used_at
	// on successful verify. Production should keep this on for stale-
	// token cleanup tooling; high-QPS deploys that can't tolerate the
	// per-request UPDATE turn it off and rely on creation timestamps.
	TouchLastUsed bool `yaml:"touch_last_used" json:"touch_last_used"`

	// HMACKey is the server-side key for the token lookup digest —
	// HMAC-SHA256(HMACKey, token) is stored in api_tokens.token_hmac and
	// matched on verify. Must be ≥32 bytes. It is a shared secret (a
	// "pepper"): rotating it invalidates every issued token, so treat it
	// like the auth signing key. Provide it out-of-band via HMACKeySecret
	// (Kubernetes Secret) in real deployments; an inline value here is for
	// dev only. When BOTH are empty the subsystem derives a key from
	// Auth.SigningKey (domain-separated) so dev works out of the box —
	// acceptable because that key is already a managed secret, but a
	// dedicated HMACKeySecret is preferred in production for key
	// separation (so rotating the JWT signing key doesn't drop tokens).
	HMACKey string `yaml:"hmac_key" json:"hmac_key"`

	// HMACKeySecret resolves HMACKey from a Kubernetes Secret at boot.
	// Takes precedence over an inline HMACKey. Same pattern as
	// Auth.SigningKeySecret.
	HMACKeySecret *SecretRef `yaml:"hmac_key_secret" json:"hmac_key_secret"`
}

// ─── Ingest plane (storage events) ──────────────────────────────────────────
//
// The ingest plane is Paladin as a CONSUMER of storage-backend events. When
// SeaweedFS or MinIO publishes "object uploaded" / "object deleted",
// the ingest worker receives the event and promotes the matching Paladin
// row from PENDING → AVAILABLE (or marks it deleted).
//
// Three transport drivers, mutually exclusive — operator picks one
// per deployment:
//
//	webhook   — receiver: HTTP endpoint the source POSTs to. Default
//	            for dev because no extra infra is needed.
//	nats      — subscriber: ingest worker subscribes to a NATS subject.
//	            Production-grade; pairs with SeaweedFS gocdk_pub_sub.
//	rabbitmq  — consumer: amqp091-go on a queue bound to an exchange
//	            the source publishes to.
//
// Source adapters live alongside the drivers; they parse the wire
// format (SeaweedFS filer event JSON / MinIO event notification JSON
// / raw CloudEvents) into the internal CloudEvent shape and dispatch
// to the handler.
type Ingest struct {
	// Enabled gates the whole subsystem. The `serve ingest` subcommand
	// errors out at boot if this is false to fail fast on misconfig.
	Enabled bool `yaml:"enabled" json:"enabled"`

	// Driver selects which transport adapter starts: "webhook" |
	// "nats" | "rabbitmq" | "sqs". Required when Enabled.
	Driver string `yaml:"driver" json:"driver"`

	// Webhook configures the HTTP receiver. Honoured only when
	// Driver=="webhook".
	Webhook IngestWebhook `yaml:"webhook" json:"webhook"`

	// NATS configures the NATS subscriber. Honoured only when
	// Driver=="nats".
	NATS IngestNATS `yaml:"nats" json:"nats"`

	// RabbitMQ configures the AMQP consumer. Honoured only when
	// Driver=="rabbitmq".
	RabbitMQ IngestRabbitMQ `yaml:"rabbitmq" json:"rabbitmq"`

	// SQS configures the AWS SQS poller. Honoured only when Driver=="sqs".
	// The canonical AWS S3 → SQS path: S3 bucket notifications land in the
	// queue, this driver long-polls and deletes on success. Pair with
	// source_format "s3".
	SQS IngestSQS `yaml:"sqs" json:"sqs"`

	// DedupTTL controls how long ingested_events rows are retained.
	// Must exceed the longest broker re-delivery window we expect.
	// Default 24h.
	DedupTTL time.Duration `yaml:"dedup_ttl" json:"dedup_ttl"`

	// ReaperInterval is the cadence of the IngestEventReaper that
	// drops ingested_events older than DedupTTL. Default 1h.
	ReaperInterval time.Duration `yaml:"reaper_interval" json:"reaper_interval"`
}

// IngestWebhook is the receiver-side HTTP transport.
type IngestWebhook struct {
	// Addr is the listen address for the receiver mux. Default :8100.
	// Endpoints exposed: /webhook/seaweedfs, /webhook/minio,
	// /webhook/cloudevents, /healthz, /readyz.
	Addr string `yaml:"addr" json:"addr"`

	// SharedSecret is the HMAC key the source signs the body with.
	// Empty in dev (publisher signs with literal "" → check trivially
	// passes). Production deploys MUST set this — usually via
	// SharedSecretRef and a K8s Secret.
	SharedSecret    string    `yaml:"shared_secret" json:"shared_secret"`
	SharedSecretRef SecretRef `yaml:"shared_secret_ref" json:"shared_secret_ref"`

	// SignatureHeader names the header carrying the HMAC. Defaults
	// to "X-Paladin-Signature". SeaweedFS webhook uses an empty bearer
	// pattern by default; configure the source to send this header.
	SignatureHeader string `yaml:"signature_header" json:"signature_header"`

	// MaxBodyBytes guards against runaway payloads. 1 MiB default.
	MaxBodyBytes int64 `yaml:"max_body_bytes" json:"max_body_bytes"`

	// ReadHeaderTimeout / ReadTimeout / WriteTimeout / IdleTimeout
	// mirror the data plane HTTPServer config so the receiver stays
	// hardened against slowloris and friends. Sensible defaults if
	// left zero.
	ReadHeaderTimeout time.Duration `yaml:"read_header_timeout" json:"read_header_timeout"`
	ReadTimeout       time.Duration `yaml:"read_timeout" json:"read_timeout"`
	WriteTimeout      time.Duration `yaml:"write_timeout" json:"write_timeout"`
	IdleTimeout       time.Duration `yaml:"idle_timeout" json:"idle_timeout"`
}

// IngestNATS is the subscriber-side NATS config.
type IngestNATS struct {
	// URL is the NATS server URL (nats://host:4222). Multiple URLs
	// can be comma-separated for cluster failover.
	URL string `yaml:"url" json:"url"`

	// Subject is the subject the source publishes to. SeaweedFS
	// gocdk_pub_sub style: "seaweedfs.filer".
	Subject string `yaml:"subject" json:"subject"`

	// QueueGroup, when non-empty, joins a NATS queue subscription so
	// multiple ingest pods load-balance across messages instead of
	// fanning each one out to every replica.
	QueueGroup string `yaml:"queue_group" json:"queue_group"`

	// JetStream toggles JetStream durable consumer mode (preferred
	// for at-least-once). When false the subscriber uses the legacy
	// at-most-once core NATS pubsub.
	JetStream bool `yaml:"jetstream" json:"jetstream"`

	// DurableName is the durable consumer id when JetStream is on.
	// Pin to a stable string so the consumer position survives pod
	// restarts.
	DurableName string `yaml:"durable_name" json:"durable_name"`

	// SourceFormat tells the worker which adapter to use:
	// "seaweedfs" | "seaweedfs_nats" | "s3" | "minio" | "cloudevents".
	// Required. (Garage is deliberately NOT a valid value — it emits no
	// notifications; the factory returns a descriptive error. See
	// docs/storage-ingest.md.)
	//
	// `seaweedfs` is for SF's `[notification.webhook]` driver — JSON
	// payload posted over HTTP. NOT compatible with this NATS driver,
	// kept selectable here only so the schema doesn't reject overlays
	// that mistakenly mix-and-match.
	//
	// `seaweedfs_nats` is for SF's `[notification.gocdk_pub_sub]`
	// driver routed via `topic_url = nats://...` — gob-encoded
	// envelope wrapping a proto-marshalled `filer_pb.EventNotification`.
	// This is what the in-cluster setup uses; see
	// gitops/.../seaweedfs/notification-config.yaml.
	//
	// `s3` / `minio` both parse the AWS S3 event-notification JSON
	// envelope (MinIO mirrors the AWS shape); they differ only in the
	// source label stamped on emitted events. Works with the nats,
	// rabbitmq, or webhook driver depending on where the store delivers.
	SourceFormat string `yaml:"source_format" json:"source_format"`

	// Auth — token / nkey / TLS. NATS-go has many auth flavours;
	// expose the common pair for now (token + TLS).
	Token    string    `yaml:"token" json:"token"`
	TokenRef SecretRef `yaml:"token_ref" json:"token_ref"`
	TLS      TLS       `yaml:"tls" json:"tls"`
}

// IngestRabbitMQ is the consumer-side AMQP config.
type IngestRabbitMQ struct {
	// URL is the AMQP URL (amqp://user:pass@host:5672/). Use TLS
	// with amqps:// in production.
	URL    string    `yaml:"url" json:"url"`
	URLRef SecretRef `yaml:"url_ref" json:"url_ref"`

	// Queue is the queue we consume from. Must be declared on the
	// broker beforehand (the SeaweedFS gocdk publisher publishes to
	// an exchange and binds via the management plugin); this
	// consumer doesn't manage exchange/queue topology.
	Queue string `yaml:"queue" json:"queue"`

	// PrefetchCount caps in-flight unacked messages per consumer.
	// Default 32.
	PrefetchCount int `yaml:"prefetch_count" json:"prefetch_count"`

	// SourceFormat tells the worker which adapter to use:
	// "seaweedfs" | "minio" | "cloudevents". Required.
	SourceFormat string `yaml:"source_format" json:"source_format"`
}

// IngestSQS is the AWS SQS poller config (the native AWS S3 → SQS path).
type IngestSQS struct {
	// QueueURL is the full SQS queue URL
	// (https://sqs.<region>.amazonaws.com/<acct>/<queue>). Required.
	QueueURL string `yaml:"queue_url" json:"queue_url"`

	// Region is the AWS region the queue lives in. Required.
	Region string `yaml:"region" json:"region"`

	// RoleArn, when set, is sts:AssumeRole'd off the ambient credential chain
	// (IRSA / env / instance profile) before polling — for a queue in another
	// account. Empty = ambient credentials.
	RoleArn string `yaml:"role_arn" json:"role_arn"`

	// Endpoint overrides the SQS endpoint (LocalStack / an S3-compatible
	// SQS shim / tests). Empty = real AWS.
	Endpoint string `yaml:"endpoint" json:"endpoint"`

	// MaxMessages caps messages returned per ReceiveMessage (1..10).
	// Default 10.
	MaxMessages int32 `yaml:"max_messages" json:"max_messages"`

	// WaitTimeSeconds is the long-poll wait (0..20). Default 20 — long
	// polling cuts empty receives and API cost. 0 = short poll.
	WaitTimeSeconds int32 `yaml:"wait_time_seconds" json:"wait_time_seconds"`

	// VisibilityTimeout (seconds) hides an in-flight message from other
	// receivers while we process it. 0 = use the queue's configured default.
	// On a delivery error we leave the message; it reappears after this
	// window and the queue's redrive policy dead-letters it after
	// maxReceiveCount.
	VisibilityTimeout int32 `yaml:"visibility_timeout" json:"visibility_timeout"`

	// UnwrapSNS unwraps an SNS `Notification` envelope to reach the S3 event
	// JSON inside `.Message`. Set when the topology is S3 → SNS → SQS (SNS
	// fan-out); leave false for a direct S3 → SQS subscription.
	UnwrapSNS bool `yaml:"unwrap_sns" json:"unwrap_sns"`

	// SourceFormat tells the worker which adapter to use — almost always
	// "s3". Empty defaults to "s3".
	SourceFormat string `yaml:"source_format" json:"source_format"`
}
