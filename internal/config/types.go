package config

import "time"

type Config struct {
	Logger       Logger       `yaml:"logger" json:"logger"`
	Server       Server       `yaml:"server" json:"server"`
	Postgres     Postgres     `yaml:"postgres" json:"postgres"`
	S3           S3           `yaml:"s3" json:"s3"`
	Policy       Policy       `yaml:"policy" json:"policy"`
	Security     Security     `yaml:"security" json:"security"`
	Housekeeping Housekeeping `yaml:"housekeeping" json:"housekeeping"`
	RateLimit    RateLimit    `yaml:"rate_limit" json:"rate_limit"`
	OTel         OTel         `yaml:"otel" json:"otel"`

	PodName string `yaml:"-"`
	Env     string `yaml:"-"`
}

type Logger struct {
	Level             string `yaml:"level" json:"level"`
	Format            string `yaml:"format" json:"format"`
	Development       bool   `yaml:"development" json:"development"`
	DisableCaller     bool   `yaml:"disable_caller" json:"disable_caller"`
	DisableStacktrace bool   `yaml:"disable_stacktrace" json:"disable_stacktrace"`
}

type Server struct {
	Mode            string        `yaml:"mode" json:"mode"`
	Name            string        `yaml:"name" json:"name"`
	HTTP            HTTPServer    `yaml:"http" json:"http"`
	GRPC            GRPCServer    `yaml:"grpc" json:"grpc"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout" json:"shutdown_timeout"`
	LogProbes       bool          `yaml:"log_probes" json:"log_probes"`
}

type HTTPServer struct {
	Addr               string        `yaml:"addr" json:"addr"`
	ReadHeaderTimeout  time.Duration `yaml:"read_header_timeout" json:"read_header_timeout"`
	ReadTimeout        time.Duration `yaml:"read_timeout" json:"read_timeout"`
	WriteTimeout       time.Duration `yaml:"write_timeout" json:"write_timeout"`
	IdleTimeout        time.Duration `yaml:"idle_timeout" json:"idle_timeout"`
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

type GRPCServer struct {
	Addr string `yaml:"addr" json:"addr"`
}

type Postgres struct {
	DSN             string        `yaml:"dsn" json:"dsn"`
	MaxConns        int32         `yaml:"max_conns" json:"max_conns"`
	MinConns        int32         `yaml:"min_conns" json:"min_conns"`
	MaxConnLifetime time.Duration `yaml:"max_conn_lifetime" json:"max_conn_lifetime"`
	MaxConnIdleTime time.Duration `yaml:"max_conn_idle_time" json:"max_conn_idle_time"`
}

type S3 struct {
	Bucket         string        `yaml:"bucket" json:"bucket"`
	Region         string        `yaml:"region" json:"region"`
	Endpoint       string        `yaml:"endpoint" json:"endpoint"`
	PublicEndpoint string        `yaml:"public_endpoint" json:"public_endpoint"`
	ForcePathStyle bool          `yaml:"force_path_style" json:"force_path_style"`
	AccessKey      string        `yaml:"access_key" json:"access_key"`
	SecretKey      string        `yaml:"secret_key" json:"secret_key"`
	PresignTTL     time.Duration `yaml:"presign_ttl" json:"presign_ttl"`
	PartSizeRaw    string        `yaml:"part_size" json:"part_size"`
	PartSizeBytes  int64         `yaml:"-"`
	SSEType        string        `yaml:"sse_type" json:"sse_type"`     // e.g. "AES256" or "aws:kms"
	SSEKeyID       string        `yaml:"sse_key_id" json:"sse_key_id"` // Optional KMS Key ID
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
	GCInterval          time.Duration `yaml:"gc_interval" json:"gc_interval"`
	DeleteOrphanedParts bool          `yaml:"delete_orphaned_parts" json:"delete_orphaned_parts"`
}

type RateLimit struct {
	RequestsPerSecond float64 `yaml:"requests_per_second" json:"requests_per_second"`
	Burst             int     `yaml:"burst" json:"burst"`
}

type OTel struct {
	Enabled      bool   `yaml:"enabled" json:"enabled"`
	ServiceName  string `yaml:"service_name" json:"service_name"`
	Environment  string `yaml:"environment" json:"environment"`
	OTLPEndpoint string `yaml:"otlp_endpoint" json:"otlp_endpoint"`
	Insecure     bool   `yaml:"insecure" json:"insecure"`
}
