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
	Middleware Middleware `yaml:"middleware" json:"middleware"`
	Workers    Workers    `yaml:"workers" json:"workers"`
	OTel       OTel       `yaml:"otel" json:"otel"`
	Storage    Storage    `yaml:"storage" json:"storage"`
	Cedar      Cedar      `yaml:"cedar" json:"cedar"`
	MCP        MCP        `yaml:"mcp" json:"mcp"`

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
	DSN               string           `yaml:"dsn" json:"dsn"`
	Password          string           `yaml:"password" json:"password"`
	PasswordSecret    *SecretRef       `yaml:"password_secret" json:"password_secret"`
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
// objectKey references one by name via `object_keys.storage_backend`; if that column
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

// Workers groups every background-loop subsystem under a single section
// so an operator looking for "what runs in the background?" finds them
// in one place.
type Workers struct {
	Reconciler   Reconciler   `yaml:"reconciler" json:"reconciler"`
	Housekeeping Housekeeping `yaml:"housekeeping" json:"housekeeping"`
}

type Housekeeping struct {
	PendingTTL          time.Duration `yaml:"pending_ttl" json:"pending_ttl"`
	MultipartTTL        time.Duration `yaml:"multipart_ttl" json:"multipart_ttl"`
	AuditLogTTL         time.Duration `yaml:"audit_log_ttl" json:"audit_log_ttl"`
	GCInterval          time.Duration `yaml:"gc_interval" json:"gc_interval"`
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
	PollInterval    time.Duration `yaml:"poll_interval" json:"poll_interval"`
	PendingGraceTTL time.Duration `yaml:"pending_grace_ttl" json:"pending_grace_ttl"`
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
