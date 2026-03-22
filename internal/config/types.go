package config

import (
	"encoding/json"
	"time"

	yaml "github.com/oasdiff/yaml3"
)

type Config struct {
	App          App          `yaml:"app" json:"app"`
	Logger       Logger       `yaml:"logger" json:"logger"`
	Server       Server       `yaml:"server" json:"server"`
	Datastores   Datastores   `yaml:"datastores" json:"datastores"`
	Policy       Policy       `yaml:"policy" json:"policy"`
	Auth         Auth         `yaml:"auth" json:"auth"`
	Security     Security     `yaml:"security" json:"security"`
	Housekeeping Housekeeping `yaml:"housekeeping" json:"housekeeping"`
	RateLimit    RateLimit    `yaml:"rate_limit" json:"rate_limit"`
	Cache        Cache        `yaml:"cache" json:"cache"`
	Timeouts     Timeouts     `yaml:"timeouts" json:"timeouts"`
	Idempotency  Idempotency  `yaml:"idempotency" json:"idempotency"`
	OTel         OTel         `yaml:"otel" json:"otel"`

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
	Mode            string        `yaml:"mode" json:"mode"`
	Name            string        `yaml:"name" json:"name"`
	HTTP            HTTPServer    `yaml:"http" json:"http"`
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
	S3       S3       `yaml:"s3" json:"s3"`
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

type S3 struct {
	Bucket          string        `yaml:"bucket" json:"bucket"`
	Region          string        `yaml:"region" json:"region"`
	Endpoint        string        `yaml:"endpoint" json:"endpoint"`
	PublicEndpoint  string        `yaml:"public_endpoint" json:"public_endpoint"`
	ForcePathStyle  bool          `yaml:"force_path_style" json:"force_path_style"`
	AccessKey       string        `yaml:"access_key" json:"access_key"`
	AccessKeySecret *SecretRef    `yaml:"access_key_secret" json:"access_key_secret"`
	SecretKey       string        `yaml:"secret_key" json:"secret_key"`
	SecretKeySecret *SecretRef    `yaml:"secret_key_secret" json:"secret_key_secret"`
	PresignTTL      time.Duration `yaml:"presign_ttl" json:"presign_ttl"`
	PartSizeRaw     string        `yaml:"part_size" json:"part_size"`
	PartSizeBytes   int64         `yaml:"-"`
	SSEType         string        `yaml:"sse_type" json:"sse_type"`     // e.g. "AES256" or "aws:kms"
	SSEKeyID        string        `yaml:"sse_key_id" json:"sse_key_id"` // Optional KMS Key ID
}

type Policy struct {
	MaxObjectSizeRaw      string        `yaml:"max_object_size" json:"max_object_size"`
	MaxObjectSizeBytes    int64         `yaml:"-" json:"-"`
	MaxMultipartSizeRaw   string        `yaml:"max_multipart_size" json:"max_multipart_size"`
	MaxMultipartSizeBytes int64         `yaml:"-" json:"-"`
	MinPartSizeRaw        string        `yaml:"min_part_size" json:"min_part_size"`
	MinPartSizeBytes      int64         `yaml:"-" json:"-"`
	MaxPartSizeRaw        string        `yaml:"max_part_size" json:"max_part_size"`
	MaxPartSizeBytes      int64         `yaml:"-" json:"-"`
	MaxParts              int           `yaml:"max_parts" json:"max_parts"`
	PresignPutTTL         time.Duration `yaml:"presign_put_ttl" json:"presign_put_ttl"`
	PresignGetTTL         time.Duration `yaml:"presign_get_ttl" json:"presign_get_ttl"`
	PresignPartTTL        time.Duration `yaml:"presign_part_ttl" json:"presign_part_ttl"`
	AllowedContentTypes   []string      `yaml:"allowed_content_types" json:"allowed_content_types"`
	LabelsMaxBytes        int           `yaml:"labels_max_bytes" json:"labels_max_bytes"`
	LabelsMaxKeys         int           `yaml:"labels_max_keys" json:"labels_max_keys"`
	ExternalRefMaxLen     int           `yaml:"external_ref_max_len" json:"external_ref_max_len"`
	ObjectKeyMaxLen       int           `yaml:"object_key_max_len" json:"object_key_max_len"`
}

type Auth struct {
	Enabled        bool       `yaml:"enabled" json:"enabled"`
	AdminKey       string     `yaml:"admin_key" json:"admin_key"`
	AdminKeySecret *SecretRef `yaml:"admin_key_secret" json:"admin_key_secret"`
}

type Security struct {
	TrustTenantIDFromRequest bool `yaml:"trust_tenant_id_from_request" json:"trust_tenant_id_from_request"`
	RejectTenantMismatch     bool `yaml:"reject_tenant_mismatch" json:"reject_tenant_mismatch"`
	EnableRLS                bool `yaml:"enable_rls" json:"enable_rls"`
	LogSensitive             bool `yaml:"log_sensitive" json:"log_sensitive"`
}

type Housekeeping struct {
	EnableReaper        bool          `yaml:"enable_reaper" json:"enable_reaper"`
	PendingTTL          time.Duration `yaml:"pending_ttl" json:"pending_ttl"`
	MultipartTTL        time.Duration `yaml:"multipart_ttl" json:"multipart_ttl"`
	AuditLogTTL         time.Duration `yaml:"audit_log_ttl" json:"audit_log_ttl"`
	GCInterval          time.Duration `yaml:"gc_interval" json:"gc_interval"`
	DeleteOrphanedParts bool          `yaml:"delete_orphaned_parts" json:"delete_orphaned_parts"`
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
