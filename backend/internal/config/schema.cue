// CUE schema for the Paladin YAML config. Defaults defined here are applied at
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
  // How metrics leave the process. "otlp" pushes to endpoint; "prometheus"
  // exposes /metrics for a scraper to pull; "none" is traces only. Defaults
  // to otlp so an existing config keeps its behaviour.
  metrics_exporter: "otlp" | "prometheus" | "none" | *"otlp"
  // Where the Prometheus endpoint listens when metrics_exporter is
  // "prometheus". Plain HTTP on a port of its own — see the field comment in
  // config/types.go for why it is not a path on the API planes.
  metrics_addr: string | *"0.0.0.0:9095"
  resource: {
    "service.name":           string | *app.name
    "deployment.environment": string | *app.env
  }
}

// Runtime holds process-wide HTTP-server settings shared across every
// role's listener. Per-listener address / timeouts / TLS live under
// the per-service blocks (api / admin / worker). Renamed from
// `server` to disambiguate from `api.server` / `admin.server`.
runtime: {
  mode:                  "debug" | "test" | *"release"
  shutdown_timeout:      =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"20s"
  log_probes:            bool | *false
  // Shared secret gating /system/health.json. Empty → open (dev).
  health_snapshot_token: string | *""
}

// #LocalDataPort is the data plane's port when the binary runs on the host
// with these defaults. 8083, not 8080: another-service's core-api owns 8080 on the
// same machine. The data listener and mcp.upstreams.data_url both derive
// from it so the two cannot drift. Containers are unaffected — the chart
// (deploy/chart/values.yaml) and configs/compose.yaml pin 8080 explicitly.
#LocalDataPort: 8083

// API role — the binary started by `serve api`. Hosts the data and
// iam Connect listeners.
api: {
  server: {
    data: #HTTPServer & {addr: string | *"0.0.0.0:\(#LocalDataPort)"}
    iam:  #HTTPServer & {addr: string | *"0.0.0.0:8085"}
  }
}

// Admin role — `serve admin`. Single listener.
admin: {
  server: #HTTPServer & {addr: string | *"0.0.0.0:8090"}
}

#HTTPServer: {
  addr:                 string
  // Doubles as the TLS handshake deadline — Go derives that from
  // ReadHeaderTimeout when it is set — so this bounds a slow handshake as
  // well as a slow header. See the chart values for why 5s was not enough.
  read_header_timeout:  =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"15s"
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
    // Server-side mTLS termination knob (#25a). Only meaningful on
    // inbound TLS listeners. See internal/utils/tls.go ParseClientAuth
    // for the mapping onto tls.ClientAuthType.
    client_auth:          *"" | "none" | "request" | "require" | "permissive" | "strict"
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
    // Optional separate DSN for the worker's cross-tenant background DML
    // jobs (purgers, lifecycle reapers, dispatcher outbox). Its user should
    // resolve to a dedicated least-privilege BYPASSRLS role (`paladin_reaper`,
    // migration 058) — DML-only, no DDL. Empty → fall back to `migrate_dsn`.
    reaper_dsn:               string | *""
    reaper_password:          string | *""
    reaper_password_secret?:  #SecretRef
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

// Auth is the Paladin IAM-plane JWT issuer + verifier. The same signing_key is
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
  // OAuth 2.1 Authorization Server (ADR-0009). Disabled by default; tokens
  // reuse the signing_key + *_token_ttl above.
  oauth: {
    enabled:                  bool | *false
    dynamic_registration:     bool | *false
    authorization_code_ttl:   =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"60s"
    allowed_redirect_schemes: [...string] | *["https"]
    consent_url:              string | *""
    token_rate_limit_per_minute:    int | *60
    token_endpoint_allowed_origins: [...string] | *[]
    seed_clients: [...{
      client_id:         string
      redirect_uris:     [...string] | *[]
      allowed_scopes:    [...string] | *[]
      allowed_audiences: [...string] | *[]
      public:            bool | *true
      skip_consent:      bool | *false
    }] | *[]
  }
}

security: {
  // trust_tenant_id_from_request removed in the post-2026-05 audit —
  // was declared but never read; the X-Tenant-Id Login hint was
  // always password-gated regardless of the flag. See types.go.Security
  // for the longer rationale.
  reject_tenant_mismatch: bool | *true
  log_sensitive:          bool | *false
  // RLS is not configurable — see types.go.Security. The runtime
  // always installs the BeforeAcquire hook because migration 023
  // makes RLS unavoidable at the DB layer.
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
  // Per-tenant request ceiling. Counters live in Postgres, so the limit is
  // shared across replicas rather than granted afresh to each pod.
  //
  // burst / max_tenants / cleanup_* are kept so existing values files keep
  // loading (the loader rejects unknown fields) but are no longer read — they
  // configured the in-memory bucket map that Postgres replaced.
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
    operations_ttl:        =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"336h"  // 14d — decided, see docs/ops-housekeeping.md
    interval:              =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"1h"
    delete_orphaned_parts: bool | *false
  }
  // RefreshTokenReap: drops expired refresh tokens.
  refresh_token_reap: {
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
    // How long a RUNNING operation may go without a heartbeat before it is
    // declared lost and marked FAILED (WORKER_LOST). The runner heartbeats
    // every 30s, so this only catches operations whose worker stopped —
    // between claiming one and writing its result, nothing else in the
    // system would ever look at the row again.
    stale_after: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"15m"
  }
  // Quota reconciler: recomputes quotas.usage_total_bytes /
  // usage_object_count from live objects and rolls the per-day
  // counters at the UTC day boundary. The upload path only
  // increments those columns and QuotaSoftCheck rejects uploads
  // against them, so without this a tenant that deletes its
  // objects stays counted and eventually cannot write.
  // interval=0 disables — only safe with no quotas configured. The
  // per-day caps reject against these counters, so skipping the roll
  // turns a daily budget into a lifetime one.
  quota_reconcile: {
    interval: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"15m"
  }
  // Purge drainer: retries byte-reclaim for permanent deletes whose
  // synchronous storage delete failed, and emits paladin.object.purged.
  // The objects row is already gone when the debt is written, so this
  // is the only remaining path to those bytes. interval=0 disables
  // and reinstates the leak.
  purge_drain: {
    interval:    =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"1m"
    max_backoff: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"1h"
    batch_size:  int & >= 1 | *100
  }
  } // close worker.jobs
} // close worker

// Dispatcher role — `serve dispatcher`. The durable webhook fan-out
// loop. Producer (admin pod) writes event_deliveries rows; this pod
// consumes them via FOR UPDATE SKIP LOCKED. Multiple replicas safe.
// Default port 8099 matches the chart's dispatcher containerPort.
dispatcher: {
  ops: #HTTPServer & {addr: string | *"0.0.0.0:8099"}
  // PollInterval — idle-loop sleep when no rows are ready.
  poll_interval:        =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"1s"
  // BatchSize — rows pulled per FOR UPDATE SKIP LOCKED scan.
  batch_size:           int & >= 1 | *50
  // BaseBackoff / MaxBackoff — per-row retry curve, doubles per
  // attempt up to MaxBackoff.
  base_backoff:         =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"5s"
  max_backoff:          =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"1h"
  // DefaultMaxAttempts — retry budget when the sub's
  // HttpSink.MaxAttempts is unset. Beyond this, the row flips to
  // status='failed' and the queue stops touching it.
  default_max_attempts: int & >= 1 | *5
  // ChargeEventsEnabled fans out one paladin.capability.charged event
  // per successful capability.UsageStore.Charge. Default OFF; high
  // cardinality.
  charge_events_enabled: bool | *false
  // AuditMirrorEnabled mirrors every audit_log row to paladin.audit.<action>.
  // Default OFF; even higher cardinality than charges.
  audit_mirror_enabled: bool | *false
}

// Storage is the registry of physical object-storage backends. Each
// logical object_key picks one by name; every write path must name a
// backend explicitly — there is no implicit default.
storage: {
  backends: [string]: {
    kind:             "aws-s3" | "s3-compatible" | "gcs" | *"aws-s3"
    provider:         string | *""
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
  // ADR-0010 Phase 1: canonical A-shape ObjectKey entity UID. Default false so
  // an env that omits it keeps the legacy UID; the shipped configs set true
  // (behaviourally inert — no policy matches the UID literal).
  canonical_collection_euid: bool | *false
}

// ingest — the storage-event receiver role (`serve ingest`).
//
// This block was missing entirely, and its absence was not neutral. With no
// CUE declaration and nothing in configs/config.yaml, every knob fell back to
// its Go zero value — including dedup_ttl and reaper_interval, which
// serve_ingest passes straight through with no fallback and which
// worker.RunTicker reads as "disabled" when <= 0. So an ingest deployment
// that did not spell both out never reaped `ingested_events` and the table
// grew without bound, while the struct's doc comments promised "Default 24h"
// and "Default 1h" — true of nothing.
//
// The defaults below are those comments, finally made real. Declaring them IS
// a behaviour change for such a deployment: the reaper starts running. That
// is the intended fix, and it is why this landed on its own rather than with
// the drift tests that found it.
ingest: {
  // Gates the whole subsystem; `serve ingest` fails fast when false.
  enabled: bool | *false
  // Which transport adapter starts. Required when enabled.
  driver:  "webhook" | "nats" | "rabbitmq" | "sqs" | *"webhook"

  // DedupTTL — how long ingested_events rows are retained. Must exceed the
  // longest broker re-delivery window expected.
  dedup_ttl:       =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"24h"
  // ReaperInterval — cadence of the reaper that drops rows older than
  // dedup_ttl. Zero would disable it, which is what the gap amounted to.
  reaper_interval: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"1h"

  // Honoured only when driver=="webhook".
  webhook: {
    addr: string | *"0.0.0.0:8100"
    // HMAC key the source signs the body with. Empty in dev (the publisher
    // signs with "" and the check trivially passes); production sets it,
    // normally through shared_secret_ref.
    shared_secret:      string | *""
    shared_secret_ref?: #SecretRef
    signature_header:   string | *"X-Paladin-Signature"
    max_body_bytes:     int & >= 1 | *1048576
    // Slowloris hardening, mirroring the data plane's HTTP server.
    read_header_timeout: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"5s"
    read_timeout:        =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"30s"
    write_timeout:       =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"30s"
    idle_timeout:        =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"60s"
  }

  // Honoured only when driver=="nats".
  nats: {
    url:           string | *""
    subject:       string | *""
    queue_group:   string | *""
    jetstream:     bool | *false
    durable_name:  string | *""
    source_format: string | *""
    token:         string | *""
    token_ref?:    #SecretRef
    // Spelled out rather than shared: schema.cue has no #TLS definition, and
    // inventing one here would change how every other block validates.
    tls: {
      enabled:              bool   | *false
      cert_path:            string | *""
      key_path:             string | *""
      ca_path:              string | *""
      server_name:          string | *""
      insecure_skip_verify: bool   | *false
    }
  }

  // Honoured only when driver=="rabbitmq".
  rabbitmq: {
    url:            string | *""
    url_ref?:       #SecretRef
    queue:          string | *""
    prefetch_count: int & >= 0 | *0
    source_format:  string | *""
  }

  // Honoured only when driver=="sqs". The canonical AWS S3 → SQS path.
  sqs: {
    queue_url:          string | *""
    region:             string | *""
    role_arn:           string | *""
    endpoint:           string | *""
    max_messages:       int & >= 1 & <= 10 | *10
    wait_time_seconds:  int & >= 0 & <= 20 | *20
    visibility_timeout: int & >= 0 | *30
    // SNS-wrapped notifications arrive as an envelope around the S3 event.
    unwrap_sns:    bool | *false
    source_format: string | *""
  }
}

mcp: {
  upstreams: {
    admin_url: string | *"http://localhost:8090"
    data_url:  string | *"http://localhost:\(#LocalDataPort)"
    iam_url:   string | *"http://localhost:8085"
    // Trust material for whichever URLs above are https://. There is no
    // "enabled" switch on purpose — the URL scheme decides whether a dial is
    // encrypted, so the two cannot disagree. A cluster deployment uses
    // https:// URLs plus ca_path pointing at the mounted internal-mTLS CA
    // bundle; config.MCPUpstreams.Validate rejects https:// without it.
    tls: {
      cert_path:            string | *""
      key_path:             string | *""
      ca_path:              string | *""
      server_name:          string | *""
      insecure_skip_verify: bool   | *false
    }
  }
  stdio: {
    enabled: bool | *true
  }
  http: {
    enabled:         bool   | *true
    addr:            string | *":8095"
    session_timeout: =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"10m"
  }
  // OAuth 2.1 Resource-Server posture for the streamable-HTTP MCP server
  // (ADR-0008). Disabled by default — the X-Paladin-Token header path is
  // unchanged. When enabled, set resource_url + authorization_servers so
  // standard MCP clients can discover where to authenticate.
  oauth: {
    enabled:               bool | *false
    resource_url:          string | *""
    authorization_servers: [...string] | *[]
    scopes_supported:      [...string] | *[]
    authorization_server: {
      issuer:                 string | *""
      authorization_endpoint: string | *""
      token_endpoint:         string | *""
      registration_endpoint:  string | *""
      jwks_uri:               string | *""
    }
  }
}

capability: {
  enabled:                  bool   | *false
  issuer_name:              string | *""
  trusted_issuers:          [...string] | *[]
  signing_key_path:         string | *""
  signing_key_kid:          string | *""
  default_ttl:              =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"15m"
  verifier_leeway:          =~"^[0-9]+(ns|us|ms|s|m|h)$" | *"30s"
  revocation_cache_ttl:     =~"^-?[0-9]+(ns|us|ms|s|m|h)$" | *"2s"
  // Auto-charge knob: amount + unit consumed by the capability
  // interceptor on each billable handler call. Unit values are
  // ISO 4217 fiat (USD/EUR/UAH/GBP) or the abstract sentinel UNIT
  // for non-currency metering; empty defers to the capability's own
  // declared unit (which defaults to USD).
  charge_per_request_amount: number | *0
  charge_per_request_unit:   "USD" | "EUR" | "UAH" | "GBP" | "UNIT" | *""
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
