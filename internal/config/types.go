package config

import "time"

type Config struct {
    Logger   Logger   `yaml:"logger"`
    Server   Server   `yaml:"server"`
    Postgres Postgres `yaml:"postgres"`
    S3       S3       `yaml:"s3"`
    Policy   Policy   `yaml:"policy"`
    OTel     OTel     `yaml:"otel"`

    PodName string `yaml:"-"`
    Env     string `yaml:"-"`
}

type Logger struct {
    Level  string `yaml:"level"`
    Format string `yaml:"format"`
}

type Server struct {
    Mode            string        `yaml:"mode"`
    Name            string        `yaml:"name"`
    HTTP            HTTPServer    `yaml:"http"`
    GRPC            GRPCServer    `yaml:"grpc"`
    ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
}

type HTTPServer struct {
    Addr string `yaml:"addr"`
}

type GRPCServer struct {
    Addr string `yaml:"addr"`
}

type Postgres struct {
	DSN             string        `yaml:"dsn"`
	MaxConns        int32         `yaml:"max_conns"`
	MinConns        int32         `yaml:"min_conns"`
	MaxConnLifetime time.Duration `yaml:"max_conn_lifetime"`
	MaxConnIdleTime time.Duration `yaml:"max_conn_idle_time"`
}

type S3 struct {
    Bucket         string        `yaml:"bucket"`
    Region         string        `yaml:"region"`
    Endpoint       string        `yaml:"endpoint"`
    ForcePathStyle bool          `yaml:"force_path_style"`
    AccessKey      string        `yaml:"access_key"`
    SecretKey      string        `yaml:"secret_key"`
    PresignTTL     time.Duration `yaml:"presign_ttl"`
    PartSizeRaw    string        `yaml:"part_size"`
    PartSizeBytes  int64         `yaml:"-"`
}

type Policy struct {
    MaxObjectSizeRaw     string   `yaml:"max_object_size"`
    MaxObjectSizeBytes   int64    `yaml:"-"`
    AllowedContentTypes  []string `yaml:"allowed_content_types"`
}

type OTel struct {
    Enabled      bool   `yaml:"enabled"`
    ServiceName  string `yaml:"service_name"`
    Environment  string `yaml:"environment"`
    OTLPEndpoint string `yaml:"otlp_endpoint"`
    Insecure     bool   `yaml:"insecure"`
}
