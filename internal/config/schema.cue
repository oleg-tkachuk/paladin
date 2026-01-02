package config


logger: {
  level:  "debug" | "warn" | "warning" | "error" | *"info"
  format: "console" | *"json"
}

server: {
  mode: "debug" | "test" | *"release"
  name: string
  http: { addr: string }
  grpc: { addr: string }
  shutdown_timeout: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"20s"
}

postgres: {
  dsn: string
  max_conns: int & >= 1 | *20
  min_conns: int & >= 0 | *2
  max_conn_lifetime: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"30m"
  max_conn_idle_time: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"5m"
}

s3: {
  bucket: string
  region: string
  endpoint: string
  force_path_style: bool | *true
  access_key: string
  secret_key: string
  presign_ttl: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"15m"
  part_size: =~"^[0-9]+(B|KB|MB|GB)$" | *"8MB"
}

policy: {
  max_object_size: =~"^[0-9]+(B|KB|MB|GB)$" | *"100MB"
  allowed_content_types: [...string]
}

otel: {
  enabled: bool | *false
  service_name: string
  environment: string
  otlp_endpoint: string
  insecure: bool | *true
}
