




app: {
  name: string
  env:  "local" | "staging" | "prod" | *"local"
}

logger: {
  level:  "debug" | "info" | "warn" | "warning" | "error" | "dpanic" | "panic" | "fatal" | *"info"
  format: "console" | *"json"
  development: bool | *false
  disable_caller: bool | *false
  disable_stacktrace: bool | *false
  sampling: {
    enabled: bool | *false
    initial: int | *100
    thereafter: int | *100
  }
  fields: {
    service: string | *app.name
    env:     string | *app.env
  }
}

server: {
  mode: "debug" | "test" | *"release"
  name: string
  http: { 
  	addr: string 
    read_header_timeout: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"5s"
    read_timeout: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"30s"
    write_timeout: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"30s"
    idle_timeout: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"90s"
    max_header_bytes: int | *1048576
    max_body_bytes: int | *10485760
    request_id_header: string | *"X-Request-Id"
    real_ip_header: string | *"X-Forwarded-For"
//    trusted_proxies: [...string] | *[]
//    cors_allowed_origins: [...string] | *["*"]
    tls: {
       enabled:              bool | *false
       cert_path:            string | *""
       key_path:             string | *""
       ca_path:              string | *""
       server_name:          string | *""
       insecure_skip_verify: bool | *false
    }
  }
  grpc: { 
  	addr: string 
    reflection_enabled: bool | *false
    max_recv_msg_size: int | *4194304
    max_send_msg_size: int | *4194304
  }
  shutdown_timeout: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"20s"
  log_probes: bool | *true
}

datastores: {
	postgres: {
	  dsn: string
	  pool: {
      max_conns: int & >= 1 | *20
      min_conns: int & >= 0 | *2
      max_conn_lifetime: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"30m"
      max_conn_idle_time: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"5m"
    }
    timeouts: {
      connect: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"5s"
      statement: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"0s"
    }
    healthcheck_period: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"30s"
	}

	s3: {
	  bucket: string
	  region: string
	  endpoint: string
	  public_endpoint: string | *""
	  force_path_style: bool | *true
	  access_key: string
	  secret_key: string
	  presign_ttl: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"15m"
	  part_size: =~"^[0-9]+(B|KB|MB|GB)$" | *"8MB"
	  sse_type: string | *"AES256"
	  sse_key_id: string | *""
	}
}


policy: {
  max_object_size: =~"^[0-9]+(B|KB|MB|GB)$" | *"100MB"
  max_multipart_size: =~"^[0-9]+(B|KB|MB|GB)$" | *"1TB"
  min_part_size: =~"^[0-9]+(B|KB|MB|GB)$" | *"5MB"
  max_part_size: =~"^[0-9]+(B|KB|MB|GB)$" | *"5GB"
  max_parts: int | *10000
  presign_put_ttl: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"15m"
  presign_get_ttl: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"15m"
  presign_part_ttl: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"15m"
//  allowed_content_types: [...string] | *[]
  labels_max_bytes: int | *4096
  labels_max_keys: int | *10
  external_ref_max_len: int | *256
  object_key_max_len: int | *1024
}

security: {
  trust_tenant_id_from_request: bool | *false
  reject_tenant_mismatch: bool | *true
  enable_rls: bool | *false
  log_sensitive: bool | *false
}

rate_limit: {
  requests_per_second: number | *100
  burst: int | *100
}

housekeeping: {
  enable_reaper: bool | *true
  pending_ttl: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"24h"
  multipart_ttl: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"72h"
  gc_interval: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"1h"
  delete_orphaned_parts: bool | *false
}

otel: {
  enabled: bool | *false
  endpoint: string
  protocol: "grpc" | "http" | *"grpc"
  insecure: bool | *true
  resource: {
    "service.name": string
    "deployment.environment" : string
  }
}
