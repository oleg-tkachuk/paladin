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

// Server holds runtime-wide HTTP-server settings shared across every
// role's listener. Per-listener address / timeouts / TLS live under
// the per-service blocks (api / admin / worker).
server: {
  mode:             "debug" | "test" | *"release"
  shutdown_timeout: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"20s"
  log_probes:       bool | *false
}

// API role — the binary started by `serve api`. Hosts the data and
// iam Connect listeners.
api: {
  server: {
    data: #HTTPServer & {addr: string | *"0.0.0.0:8080"}
    iam:  #HTTPServer & {addr: string | *"0.0.0.0:8085"}
  }
}

// Admin role — `serve admin`. Single listener.
admin: {
  server: #HTTPServer & {addr: string | *"0.0.0.0:8090"}
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
    // Optional separate DSN for schema migrations. Production deploys
    // SHOULD set this to a DDL-capable role (`paladin_migrate`) distinct from
    // the runtime role (`paladin_app`) used by `dsn`. Empty → migrations run
    // as the runtime user (fine for dev, unsafe for prod).
    migrate_dsn:               string | *""
    migrate_password:          string | *""
    migrate_password_secret?:  #SecretRef
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
      // Server-side statement_timeout. 0 disables (do not ship to prod).
      // Default 30s bounds worst-case query time without breaking the
      // worker loops that occasionally chew through ~1s page-scans.
      statement: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"30s"
    }
    healthcheck_period: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"30s"
  }
}

limits: {
  max_object_size:    =~"^[0-9]+(B|KB|MB|GB|TB)$" | *"100MB"
  max_multipart_size: =~"^[0-9]+(B|KB|MB|GB|TB)$" | *"1TB"
  min_part_size:      =~"^[0-9]+(B|KB|MB|GB)$" | *"5MB"
  max_part_size:      =~"^[0-9]+(B|KB|MB|GB)$" | *"5GB"
  max_parts:          int | *10000
  // Empty → accept anything. Listing common types here gives the upload
  // path an early reject for typos / drive-bys.
  allowed_content_types: [...string] | *[]
  // Presign-URL ttls + body-size cap. Per-method ttls override default_ttl
  // when non-zero; max_ttl bounds caller-supplied TTLs.
  presign: {
    put_ttl:          =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"15m"
    get_ttl:          =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"15m"
    part_ttl:         =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"15m"
    default_ttl:      =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"15m"
    max_ttl:          =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"168h"
    default_max_size: int & >= 1 | *5368709120 // 5 GiB
  }
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
  log_sensitive:                bool | *false
}

// Bootstrap groups one-shot startup steps. Each step is opt-in (default
// disabled) and idempotent — restarting the server does not duplicate
// state. See internal/bootstrap/admin.go for the runtime implementation.
bootstrap: {
  // ArgoCD-style platform-admin bootstrap. Reads the password from an
  // environment variable (mounted from a Kubernetes Secret via the chart),
  // creates the dedicated tenant if missing, then either creates the user
  // or — when force_reset=true — rotates its password_hash. Audit entries
  // are written under "iam.bootstrap_admin.create" / ".reset".
  admin: {
    enabled:             bool   | *false
    subject:             string | *"admin"
    tenant_slug:         =~"^[a-z][a-z0-9-]{1,62}[a-z0-9]$" | *"platform"
    tenant_display_name: string | *"Platform"
    display_name:        string | *""
    roles:               [...string] | *["platform.admin"]
    // Either `password` (debug-only, inline) or `password_secret` (k8s
    // Secret coordinates) populates the resolved password. The chart
    // creates the Secret itself — operators don't pre-create it. In-cluster
    // boot resolves password_secret → password and zeroes the ref before
    // any consumer reads cfg.
    password:            string | *""
    password_secret?:    #SecretRef
    // 0 = use the per-mode default (16 in release, 8 in debug/test).
    min_password_length: int & >= 0 | *0
    force_reset:         bool | *false
  }
}

// Middleware bundles cross-cutting interceptor knobs (timeouts, rate
// limiting, resource cache, idempotency).
middleware: {
  timeouts: {
    fast_operation:    =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"5s"
    default_operation: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"30s"
    s3_operation:      =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"60s"
    long_operation:    =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"2m"
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
  idempotency: {
    enabled: bool | *true
    ttl:     =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"24h"
  }
}

// Worker role — `serve worker`. Hosts a single ops HTTP listener
// (probes / metrics) and the background-job catalog. The ops port
// defaults to :8090 so the chart's containerPort can match a single
// hard-coded value across the admin and worker roles.
worker: {
  ops: #HTTPServer & {addr: string | *"0.0.0.0:8090"}
  jobs: {
  // Reconciler: closes gaps when S3 events are unavailable / lost.
  reconciler: {
    interval:       =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"30s"
    min_object_age: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"2h"
    batch_size:     int & >= 1 | *100
  }
  // Housekeeping: deletes audit_log rows older than audit_log_ttl.
  housekeeping: {
    pending_ttl:           =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"24h"
    multipart_ttl:         =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"72h"
    audit_log_ttl:         =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"8760h" // 365d
    operations_ttl:        =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"720h"  // 30d
    interval:              =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"1h"
    delete_orphaned_parts: bool | *false
  }
  // RefreshTokenReap: drops expired refresh tokens.
  refresh_token_reap: {
    interval: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"1h"
  }
  // ApiKeyReap: flips revoked=true on api-keys past their expires_at.
  api_key_reap: {
    interval: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"1h"
  }
  // Lifecycle: CEL-based per-bucket expiration. Disable when no buckets
  // carry lifecycle rules to save the per-tick scan.
  lifecycle: {
    enabled:  bool | *true
    interval: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"30m"
  }
  // Replication: per-bucket cross-backend copies. Currently dry-run
  // unless a real StorageReplicator is wired in.
  replication: {
    enabled:         bool | *false
    interval:        =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"5m"
    lookback_window: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"1h"
  }
  // Capability: drop expired revocation rows so the denylist stays
  // bounded. interval=0 disables; expired_for is the grace window
  // beyond a capability's natural expiry before its revocation row
  // can be dropped.
  capability: {
    interval:    =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"1h"
    expired_for: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"24h"
  }
  // APIToken: drop expired api_tokens rows. Same shape as Capability
  // — interval=0 disables.
  api_token: {
    interval:    =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"1h"
    expired_for: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"168h"   // 7 days
  }
  // Operations runner: dequeues PENDING rows from the operations
  // table and runs the registered Executor. Without it, BatchXxx
  // RPCs enqueue work that nothing ever completes.
  operations: {
    interval: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"5s"
  }
  } // close worker.jobs
} // close worker

// Storage is the registry of physical object-storage backends. Each
// logical object_key picks one by name; when its `storage_backend`
// column is empty the service falls back to `default_backend`.
storage: {
  default_backend: string | *"primary"
  backends: [string]: {
    kind:             "aws-s3" | "s3-compatible" | "gcs" | *"aws-s3"
    bucket:           string | *""
    region:           string | *""
    endpoint:         string | *""
    public_endpoint:  string | *""
    force_path_style: bool   | *true
    auth: {
      mode:                    "static_keys" | "default_chain" | "assume_role" | "web_identity" | *"default_chain"
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

llm: {
  enabled: bool | *false
  litellm: {
    base_url:        string | *""
    api_key:         string | *""
    api_key_secret?: #SecretRef
    timeout:         =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"30s"
  }
  ollama: {
    base_url: string | *""
    // Local inference is slower than cloud — wider default timeout.
    timeout:  =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"120s"
  }
  bindings: [string]: {
    provider: "litellm" | "ollama"
    model:    string
  }
}

vector: {
  enabled: bool | *false
  backend: "pgvector" | "qdrant" | *"pgvector"
  pgvector: {
    dimension:     int | *1536
    default_model: string | *""
  }
  qdrant: {
    url:             string | *""
    api_key:         string | *""
    api_key_secret?: #SecretRef
    collection:      string | *"paladin"
    dimension:       int | *1536
    timeout:         =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"10s"
  }
}

capability: {
  enabled:               bool   | *false
  issuer_name:           string | *""
  trusted_issuers:       [...string] | *[]
  signing_key_path:      string | *""
  signing_key_kid:       string | *""
  default_ttl:           =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"15m"
  verifier_leeway:       =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"30s"
  revocation_cache_ttl:  =~"^-?[0-9]+(ns|us|ms|s|m|h)$" | *"2s"
}

api_token: {
  enabled:          bool | *false
  max_ttl:          =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"8760h"   // 1 year
  verifier_leeway:  =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"30s"
  touch_last_used:  bool | *true
}

#SecretRef: string | {
  name:      string
  key:       string | *"password"
  namespace: string | *""
}
