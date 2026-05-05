// CUE schema for the PALADIN YAML config. Defaults defined here are applied at
// load time before validation, so any field omitted from the YAML lands at
// the value declared as `*<default>` below.
//
// Keep this file in sync with internal/config/types.go — every Go field
// must have a matching schema entry, otherwise the koanf merge silently
// drops unknown values.

app: {
  name: string
  env:  "local" | "staging" | "prod" | *"local"
}

logger: {
  level:              "debug" | "info" | "warn" | "warning" | "error" | "dpanic" | "panic" | "fatal" | *"info"
  format:             "console" | *"json"
  development:        bool | *false
  disable_caller:     bool | *false
  disable_stacktrace: bool | *false
  sampling: {
    enabled:    bool | *false
    initial:    int  | *100
    thereafter: int  | *100
  }
  // fields default to app.name / app.env so the operator only overrides them
  // when the structured-log dimensions must differ from the service identity
  // (rarely true).
  fields: {
    service: string | *app.name
    env:     string | *app.env
  }
}

otel: {
  enabled:  bool | *false
  endpoint: string | *""
  protocol: "grpc" | "http" | *"http"
  insecure: bool | *true
  resource: {
    "service.name":           string | *app.name
    "deployment.environment": string | *app.env
  }
}

server: {
  mode: "debug" | "test" | *"release"
  name: string | *app.name
  // Three-plane HTTP listeners. v2 splits the API into paladin-data, paladin-admin
  // and paladin-iam audiences, each on its own port so the operator can expose
  // them on different network profiles.
  data_http:        #HTTPServer
  admin_http:       #HTTPServer
  iam_http:         #HTTPServer
  shutdown_timeout: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"20s"
  log_probes:       bool | *false
}

#HTTPServer: {
  addr:                 string
  read_header_timeout:  =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"5s"
  read_timeout:         =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"30s"
  write_timeout:        =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"30s"
  idle_timeout:         =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"90s"
  max_header_bytes:     int    | *1048576  // 1 MiB
  max_body_bytes:       int    | *10485760 // 10 MiB
  request_id_header:    string | *"X-Request-Id"
  real_ip_header:       string | *"X-Forwarded-For"
  trusted_proxies:      [...string] | *[]
  cors_allowed_origins: [...string] | *[]
  tls: {
    enabled:              bool   | *false
    cert_path:            string | *""
    key_path:             string | *""
    ca_path:              string | *""
    server_name:          string | *""
    insecure_skip_verify: bool   | *false
  }
}

datastores: {
  postgres: {
    dsn:               string
    password:          string | *""
    password_secret?:  #SecretRef
    // Optional separate DSN for reaper / housekeeping connections so they
    // run with a lower-privilege role and don't compete with hot-path
    // queries on the main pool. Empty → reuse `dsn`.
    reaper_dsn: string | *""
    pool: {
      max_conns:          int & >= 1 | *20
      min_conns:          int & >= 0 | *2
      max_conn_lifetime:  =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"30m"
      max_conn_idle_time: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"5m"
    }
    timeouts: {
      // 0s = disabled. Enforce a non-zero value on hot tenants to bound
      // worst-case query time.
      connect:   =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"5s"
      statement: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"0s"
    }
    healthcheck_period: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"30s"
  }
}

policy: {
  max_object_size:    =~"^[0-9]+(B|KB|MB|GB|TB)$" | *"100MB"
  max_multipart_size: =~"^[0-9]+(B|KB|MB|GB|TB)$" | *"1TB"
  min_part_size:      =~"^[0-9]+(B|KB|MB|GB)$" | *"5MB"
  max_part_size:      =~"^[0-9]+(B|KB|MB|GB)$" | *"5GB"
  max_parts:          int | *10000
  presign_put_ttl:    =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"15m"
  presign_get_ttl:    =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"15m"
  presign_part_ttl:   =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"15m"
  // Empty → accept anything. Listing common types here gives the upload
  // path an early reject for typos / drive-bys.
  allowed_content_types: [...string] | *[]
  labels_max_bytes:      int | *4096
  labels_max_keys:       int | *10
  external_ref_max_len:  int | *256
  object_tag_max_len:    int | *1024
}

// Auth is the PALADIN IAM-plane JWT issuer + verifier. The same signing_key is
// used to mint tokens (Login / RefreshToken) and to verify them on each
// plane interceptor; three audiences are recognised: paladin-data, paladin-admin,
// paladin-iam. JWKSURL is reserved for federated IdP integration and unused
// in the current release — leave it empty.
auth: {
  issuer:               string | *"paladin"
  signing_key:          string | *""
  signing_key_secret?:  #SecretRef
  jwks_url:             string | *""
  leeway:               =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"30s"
  access_token_ttl:     =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"15m"
  refresh_token_ttl:    =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"168h"
  scoped_token_max_ttl: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"24h"
}

security: {
  trust_tenant_id_from_request: bool | *true
  reject_tenant_mismatch:       bool | *true
  // enable_rls requires the row-level-security migration to be applied.
  // Flipping it without the migration is a silent foot-gun.
  enable_rls:    bool | *false
  log_sensitive: bool | *false
}

rate_limit: {
  enabled:             bool   | *true
  requests_per_second: number | *300
  burst:               int    | *500
  max_tenants:         int    | *10000
  cleanup_ttl:         =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"10m"
  cleanup_interval:    =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"5m"
}

cache: {
  enabled:  bool | *true
  max_size: int & >= 1 | *1000
  ttl:      =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"5m"
}

timeouts: {
  fast_operation:    =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"5s"
  default_operation: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"30s"
  s3_operation:      =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"60s"
  long_operation:    =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"2m"
}

idempotency: {
  enabled: bool | *true
  ttl:     =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"24h"
}

housekeeping: {
  enable_reaper:         bool | *true
  pending_ttl:           =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"24h"
  multipart_ttl:         =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"72h"
  audit_log_ttl:         =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"8760h" // 365d
  gc_interval:           =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"1h"
  delete_orphaned_parts: bool | *false
}

// Storage is the registry of physical object-storage backends. Each
// logical object_key picks one by name; when its `storage_backend`
// column is empty the service falls back to `default_backend`.
storage: {
  default_backend: string | *"primary"
  backends: [string]: {
    kind:             "aws-s3" | "s3-compatible" | "gcs"
    bucket:           string | *""
    region:           string | *""
    endpoint:         string | *""
    public_endpoint:  string | *""
    force_path_style: bool   | *true
    auth: {
      mode:                    "static_keys" | "default_chain" | "assume_role" | "web_identity"
      access_key:              string | *""
      access_key_secret?:      #SecretRef
      secret_key:              string | *""
      secret_key_secret?:      #SecretRef
      session_token:           string | *""
      session_token_secret?:   #SecretRef
      role_arn:                string | *""
      session_name:            string | *""
      external_id:             string | *""
      duration_seconds:        int    | *0
      web_identity_token_file: string | *""
    }
    presign_ttl: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"15m"
    part_size:   =~"^[0-9]+(B|KB|MB|GB)$"     | *"8MB"
    sse: {
      type:   "" | "AES256" | "aws:kms" | *""
      key_id: string | *""
    }
    events: {
      enabled:       bool | *false
      target:        "sqs" | "redis" | "none" | *"none"
      queue_url:     string | *""
      poll_interval: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"10s"
    }
  }
}

presign: {
  default_ttl:      =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"15m"
  max_ttl:          =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"168h"
  default_max_size: int & >= 1 | *5368709120 // 5 GiB
}

reconciler: {
  poll_interval:     =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"30s"
  pending_grace_ttl: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"2h"
  batch_size:        int & >= 1 | *100
}

cedar: {
  policy_cache_ttl: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"30s"
}

mcp: {
  upstreams: {
    admin_url: string | *"http://localhost:8090"
    data_url:  string | *"http://localhost:8080"
    iam_url:   string | *"http://localhost:8085"
  }
  stdio: {
    enabled:     bool | *true
    allow_write: bool | *false
  }
  http: {
    enabled:         bool   | *true
    addr:            string | *":8095"
    allow_write:     bool   | *false
    session_timeout: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"10m"
  }
}

#SecretRef: string | {
  name:      string
  key:       string | *"password"
  namespace: string | *""
}
