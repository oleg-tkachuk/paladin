package config

import (
	"encoding/json"
	"time"

	yaml "github.com/oasdiff/yaml3"
)

type Config struct {
	App        App        `yaml:"app" json:"app"`
	Logger     Logger     `yaml:"logger" json:"logger"`
	Server     Server     `yaml:"server" json:"server"`
	Datastores Datastores `yaml:"datastores" json:"datastores"`
	Limits     Limits     `yaml:"limits" json:"limits"`
	Auth       Auth       `yaml:"auth" json:"auth"`
	Security   Security   `yaml:"security" json:"security"`
	Bootstrap  Bootstrap  `yaml:"bootstrap" json:"bootstrap"`
	Middleware Middleware `yaml:"middleware" json:"middleware"`
	Workers    Workers    `yaml:"workers" json:"workers"`
	OTel       OTel       `yaml:"otel" json:"otel"`
	Storage    Storage    `yaml:"storage" json:"storage"`
	Cedar      Cedar      `yaml:"cedar" json:"cedar"`
	MCP        MCP        `yaml:"mcp" json:"mcp"`
	LLM        LLM        `yaml:"llm" json:"llm"`
	Vector     Vector     `yaml:"vector" json:"vector"`
	Capability Capability `yaml:"capability" json:"capability"`
	APIToken   APIToken   `yaml:"api_token" json:"api_token"`

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

type Server struct {
	Mode string `yaml:"mode" json:"mode"`
	// DataHTTP, AdminHTTP, IAMHTTP — v2 three-plane listener configuration.
	// All three must be set; each plane gets its own audience and interceptor
	// stack.
	DataHTTP        HTTPServer    `yaml:"data_http" json:"data_http"`
	AdminHTTP       HTTPServer    `yaml:"admin_http" json:"admin_http"`
	IAMHTTP         HTTPServer    `yaml:"iam_http" json:"iam_http"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout" json:"shutdown_timeout"`
	LogProbes       bool          `yaml:"log_probes" json:"log_probes"`
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

	ReaperDSN         string           `yaml:"reaper_dsn" json:"reaper_dsn"`
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
// objectKey references one by name via `object_keys.backend_id`; if that column
// is empty the service falls back to DefaultBackend.
type Storage struct {
	DefaultBackend string                    `yaml:"default_backend" json:"default_backend"`
	Backends       map[string]StorageBackend `yaml:"backends" json:"backends"`
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
}

type Security struct {
	TrustTenantIDFromRequest bool `yaml:"trust_tenant_id_from_request" json:"trust_tenant_id_from_request"`
	RejectTenantMismatch     bool `yaml:"reject_tenant_mismatch" json:"reject_tenant_mismatch"`
	LogSensitive             bool `yaml:"log_sensitive" json:"log_sensitive"`
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
type Workers struct {
	Reconciler       Reconciler       `yaml:"reconciler" json:"reconciler"`
	Housekeeping     Housekeeping     `yaml:"housekeeping" json:"housekeeping"`
	RefreshTokenReap RefreshTokenReap `yaml:"refresh_token_reap" json:"refresh_token_reap"`
	ApiKeyReap       ApiKeyReap       `yaml:"api_key_reap" json:"api_key_reap"`
	Lifecycle        Lifecycle        `yaml:"lifecycle" json:"lifecycle"`
	Replication      Replication      `yaml:"replication" json:"replication"`
	Capability       CapabilityWorker `yaml:"capability" json:"capability"`
	APIToken         APITokenWorker   `yaml:"api_token" json:"api_token"`
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

// ApiKeyReap flips revoked=true on api-keys past their expires_at.
// Always on; tune `interval` based on api-key volume.
type ApiKeyReap struct {
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
	OperationsTTL       time.Duration `yaml:"operations_ttl" json:"operations_ttl"`
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
	// Bucket is the physical S3 bucket name. PALADIN "ObjectKey" entries
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

// StorageBackendAuth selects how the PALADIN control plane authenticates to a
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
}

// MCPUpstreams holds the PALADIN plane URLs the MCP bridge dispatches to. They
// are not secrets; the per-request bearer token is what gates access.
type MCPUpstreams struct {
	AdminURL string `yaml:"admin_url" json:"admin_url"`
	DataURL  string `yaml:"data_url" json:"data_url"`
	IAMURL   string `yaml:"iam_url" json:"iam_url"`
}

// MCPStdio configures the local stdio bridge (Claude Desktop / Cursor /
// IDE plugins). When AllowWrite=false the binary registers only read-only
// tools — the safe default for ad-hoc LLM exploration on a developer
// workstation.
type MCPStdio struct {
	Enabled    bool `yaml:"enabled" json:"enabled"`
	AllowWrite bool `yaml:"allow_write" json:"allow_write"`
}

// MCPHTTP configures the streamable-HTTP bridge (remote LLM platforms).
// AllowWrite carries higher blast radius here than for the stdio binary;
// keep it false unless the deployment is behind mTLS and short-lived
// service-account tokens.
type MCPHTTP struct {
	Enabled        bool          `yaml:"enabled" json:"enabled"`
	Addr           string        `yaml:"addr" json:"addr"`
	AllowWrite     bool          `yaml:"allow_write" json:"allow_write"`
	SessionTimeout time.Duration `yaml:"session_timeout" json:"session_timeout"`
}

// LLM configures the server-side model providers PALADIN uses for out-of-band
// flows (on-store summarization, embeddings, classifiers, rerank). MCP
// `sampling` covers in-loop calls and goes through the host — this
// section is what PALADIN needs when no host is connected.
//
// Two providers are supported:
//
//   - LiteLLM: an OpenAI-compatible proxy fronting every major vendor.
//     Multi-provider routing, retries, fallbacks, per-virtual-key
//     budgets and cost dashboards live in the proxy. PALADIN holds only the
//     proxy bearer key.
//
//   - Ollama: local CPU/GPU inference. Speaks OpenAI-compat at /v1/*.
//     No auth; cost is always 0. Useful for air-gapped deploys, embedding
//     workloads where vendor-API costs are prohibitive, and dev mode.
//
// Bindings map a logical Role ("embeddings.default", "summarize.cheap")
// to a (provider, model) pair so PALADIN code stays decoupled from vendor
// identity. Operators flip a deployment from LiteLLM-Anthropic to
// Ollama-llama3 with a YAML edit, no code change.
type LLM struct {
	// Enabled gates the whole subsystem. When false, callers see
	// llm.ErrProviderUnavailable and fall back to heuristics.
	Enabled bool `yaml:"enabled" json:"enabled"`

	// LiteLLM is the proxy-fronted multi-vendor provider. Optional;
	// when BaseURL is empty, LiteLLM bindings won't resolve and will
	// surface ErrProviderUnavailable to callers.
	LiteLLM LLMLiteLLM `yaml:"litellm" json:"litellm"`

	// Ollama is the local-inference provider. Optional; when BaseURL
	// is empty, Ollama bindings won't resolve.
	Ollama LLMOllama `yaml:"ollama" json:"ollama"`

	// Bindings maps Role → (provider, model). Key is the Role string
	// from internal/llm/provider.go.
	Bindings map[string]LLMBinding `yaml:"bindings" json:"bindings"`
}

// LLMLiteLLM is the LiteLLM-proxy provider config.
type LLMLiteLLM struct {
	// BaseURL of the LiteLLM proxy (e.g. http://litellm:4000/v1). The
	// `/v1` suffix is the OpenAI-compat API root the client appends
	// `/chat/completions`/`/embeddings` to.
	BaseURL string `yaml:"base_url" json:"base_url"`
	// APIKey / APIKeySecret resolve the proxy bearer key. In dev the
	// inline APIKey is convenient; production deploys mount via Secret
	// (cross-namespace SecretRefs are honoured by K8sSecretResolver).
	APIKey       string    `yaml:"api_key" json:"api_key"`
	APIKeySecret SecretRef `yaml:"api_key_secret" json:"api_key_secret"`
	// Timeout caps each request. Embedding batches above this size
	// must be chunked by the caller.
	Timeout time.Duration `yaml:"timeout" json:"timeout"`
}

// LLMOllama is the local-Ollama provider config.
type LLMOllama struct {
	// BaseURL of the Ollama daemon (e.g. http://ollama:11434). The
	// client appends `/v1/chat/completions` / `/v1/embeddings`.
	BaseURL string `yaml:"base_url" json:"base_url"`
	// Timeout caps each request. Defaults to 120s — local inference
	// is slower than cloud APIs, especially on cold starts.
	Timeout time.Duration `yaml:"timeout" json:"timeout"`
}

// LLMBinding pins one role to a (provider, model) pair.
type LLMBinding struct {
	// Provider selects which configured provider to use. One of
	// "litellm" / "ollama".
	Provider string `yaml:"provider" json:"provider"`
	// Model is the provider-specific model identifier. For LiteLLM
	// it's the alias in the proxy's `model_list` (e.g.
	// "openai/gpt-4o-mini"); for Ollama it's the model tag
	// ("llama3.1:8b").
	Model string `yaml:"model" json:"model"`
}

// Vector configures the embeddings index. Day-1 backend is pgvector; the
// Qdrant block is reserved for later (placeholder adapter returns
// ErrNotImplemented). Backend selection is single — multi-backend lands
// when the migration trigger fires.
type Vector struct {
	// Enabled gates the whole subsystem. When false, semantic search and
	// memory recall return empty results — non-fatal degradation.
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Backend selects the implementation: "pgvector" (default) or "qdrant".
	Backend string `yaml:"backend" json:"backend"`
	// Pgvector holds the pgvector-specific knobs. Reuses the main
	// Postgres pool; no separate DSN.
	Pgvector VectorPgvector `yaml:"pgvector" json:"pgvector"`
	// Qdrant holds the Qdrant-specific knobs. Honoured only when
	// Backend == "qdrant" and the real client lands.
	Qdrant VectorQdrant `yaml:"qdrant" json:"qdrant"`
}

// VectorPgvector mirrors internal/vector/pgvector.Config.
type VectorPgvector struct {
	// Dimension must match migrations/014_pgvector.sql vector(N).
	Dimension int `yaml:"dimension" json:"dimension"`
	// DefaultModel is used when SearchRequest.EmbeddingModel is empty
	// and the tenant has only one model registered.
	DefaultModel string `yaml:"default_model" json:"default_model"`
}

// VectorQdrant mirrors internal/vector/qdrant.Config. Default deploy
// pattern: Qdrant lives in its own namespace (typically `qdrant`); PALADIN
// reaches it over a cluster-internal Service URL. The api_key Secret
// can either be copied into the PALADIN namespace (Helm pre-install hook)
// or read cross-namespace via the secret-reader ClusterRole — both
// flows are supported.
type VectorQdrant struct {
	// URL of the Qdrant REST endpoint, e.g.
	// http://qdrant.qdrant.svc.cluster.local:6333
	URL string `yaml:"url" json:"url"`
	// APIKey is the bearer key Qdrant expects on every request.
	// In production resolve via APIKeySecret; APIKey-inline is for
	// dev mode only.
	APIKey       string    `yaml:"api_key" json:"api_key"`
	APIKeySecret SecretRef `yaml:"api_key_secret" json:"api_key_secret"`
	// Collection is the Qdrant collection name. One collection per
	// PALADIN deployment is the supported topology — multi-tenancy is
	// enforced via the `tenant_id` payload filter on every query,
	// not via separate collections.
	Collection string `yaml:"collection" json:"collection"`
	// Dimension MUST match the embedding model's output size.
	// Mismatch shows up as a Qdrant 4xx at upsert time.
	Dimension int `yaml:"dimension" json:"dimension"`
	// Timeout caps each HTTP call. Default 10s — search is fast but
	// large upserts / collection-stat calls can spike on cold cache.
	Timeout time.Duration `yaml:"timeout" json:"timeout"`
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
	// Conventional value: the PALADIN deployment's external URL or a
	// stable label like "paladin-prod-eu".
	IssuerName string `yaml:"issuer_name" json:"issuer_name"`

	// TrustedIssuers is the set of `iss` values the verifier accepts.
	// Always include IssuerName; add others when federating across
	// PALADIN instances.
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
}
