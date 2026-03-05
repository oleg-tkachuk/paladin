# Configuration

All configuration is loaded from a YAML file at startup. The default path is `/app/configs/paladin.yaml` and can be overridden with the `--config` CLI flag.

**Source:** `internal/config/types.go`, `internal/config/config.go`, `internal/config/resolver.go`

## Config Sections

### `app`

| Key | Type | Description |
|---|---|---|
| `app.name` | string | Service name (used in logs and OTel resource) |
| `app.env` | string | Environment label: `local`, `staging`, `prod` |

### `logger`

| Key | Type | Default (local) | Description |
|---|---|---|---|
| `logger.level` | string | `error` | Log level: `debug`, `info`, `warn`, `error` |
| `logger.format` | string | `json` | `json` or `console` |
| `logger.development` | bool | `true` | Enables development mode in Zap |
| `logger.disable_caller` | bool | `false` | Omit caller from log entries |
| `logger.disable_stacktrace` | bool | `true` | Omit stack traces |
| `logger.sampling.enabled` | bool | `true` | Enable log sampling |
| `logger.sampling.initial` | int | `100` | Logs per second before sampling |
| `logger.sampling.thereafter` | int | `100` | Log every N after initial |
| `logger.fields.service` | string | `paladin` | Static `service` field in every log |
| `logger.fields.env` | string | `local` | Static `env` field in every log |

### `server`

| Key | Type | Default (local) | Description |
|---|---|---|---|
| `server.mode` | string | `release` | Gin mode: `debug`, `test`, `release` |
| `server.name` | string | `paladin` | Server name |
| `server.shutdown_timeout` | duration | `20s` | Graceful shutdown timeout |
| `server.log_probes` | bool | `false` | Log health probe requests at debug level |
| `server.http.addr` | string | `0.0.0.0:8080` | HTTP listener address |
| `server.http.read_header_timeout` | duration | `5s` | Max time to read request headers |
| `server.http.read_timeout` | duration | `30s` | Max time to read request body |
| `server.http.write_timeout` | duration | `30s` | Max time to write response |
| `server.http.idle_timeout` | duration | `90s` | Keep-alive idle timeout |
| `server.http.max_header_bytes` | int | `1048576` (1 MiB) | Max request header size |
| `server.http.max_body_bytes` | int64 | `10485760` (10 MiB) | Max request body size (hard limit applied by middleware) |
| `server.http.request_id_header` | string | `X-Request-Id` | Header name for request ID propagation |
| `server.http.real_ip_header` | string | `X-Forwarded-For` | Header used to extract real client IP |
| `server.http.trusted_proxies` | []string | `[]` | Trusted proxy IPs for IP extraction |
| `server.http.cors_allowed_origins` | []string | `["*"]` | CORS allowed origins (**restrict in production**) |
| `server.http.tls.enabled` | bool | `false` | Enable HTTPS |
| `server.http.tls.cert_path` | string | — | Path to TLS certificate |
| `server.http.tls.key_path` | string | — | Path to TLS private key |
| `server.grpc.addr` | string | `0.0.0.0:9090` | gRPC listener address |
| `server.grpc.reflection_enabled` | bool | `false` | Enable gRPC server reflection |
| `server.grpc.max_recv_msg_size` | int | `4194304` (4 MiB) | Max gRPC receive message size |
| `server.grpc.max_send_msg_size` | int | `4194304` (4 MiB) | Max gRPC send message size |

### `datastores.postgres`

| Key | Type | Description |
|---|---|---|
| `datastores.postgres.dsn` | string | Full PostgreSQL DSN (password can be injected via secret) |
| `datastores.postgres.password_secret` | SecretRef | Kubernetes Secret reference for DB password |
| `datastores.postgres.reaper_dsn` | string | Optional separate DSN for the reaper role (higher privileges) |
| `datastores.postgres.pool.max_conns` | int32 | Max pool connections (default `20`) |
| `datastores.postgres.pool.min_conns` | int32 | Min pool connections (default `2`) |
| `datastores.postgres.pool.max_conn_lifetime` | duration | Max connection lifetime (default `30m`) |
| `datastores.postgres.pool.max_conn_idle_time` | duration | Max idle time before closing (default `5m`) |
| `datastores.postgres.timeouts.connect` | duration | Connection timeout (default `5s`) |
| `datastores.postgres.timeouts.statement` | duration | Statement timeout (default `0s` = disabled) |
| `datastores.postgres.healthcheck_period` | duration | Pool health check period (default `30s`) |

**`SecretRef` type:** Can be either a plain string (secret name, key defaults to `password`) or an object with `name`, `key`, `namespace`.

### `datastores.s3`

| Key | Type | Description |
|---|---|---|
| `datastores.s3.bucket` | string | Target S3 bucket name |
| `datastores.s3.region` | string | S3 region |
| `datastores.s3.endpoint` | string | Internal S3 endpoint URL |
| `datastores.s3.public_endpoint` | string | Public S3 endpoint for pre-signed URLs |
| `datastores.s3.force_path_style` | bool | Use path-style addressing (required for SeaweedFS) |
| `datastores.s3.access_key` | string | S3 access key (override via `access_key_secret`) |
| `datastores.s3.access_key_secret` | SecretRef | Kubernetes Secret for S3 access key |
| `datastores.s3.secret_key` | string | S3 secret key (override via `secret_key_secret`) |
| `datastores.s3.secret_key_secret` | SecretRef | Kubernetes Secret for S3 secret key |
| `datastores.s3.presign_ttl` | duration | Default pre-signed URL TTL (default `15m`) |
| `datastores.s3.part_size` | string | Multipart upload part size (e.g. `"8MB"`) |
| `datastores.s3.sse_type` | string | Server-side encryption type: `AES256` or `aws:kms` |
| `datastores.s3.sse_key_id` | string | KMS Key ID when `sse_type=aws:kms` |

### `policy`

| Key | Type | Default | Description |
|---|---|---|---|
| `policy.max_object_size` | string | `"100MB"` | Maximum file size for single upload |
| `policy.max_multipart_size` | string | `"1TB"` | Maximum total size for multipart uploads |
| `policy.min_part_size` | string | `"5MB"` | Minimum part size (AWS S3 minimum) |
| `policy.max_part_size` | string | `"5GB"` | Maximum individual part size |
| `policy.max_parts` | int | `10000` | Maximum number of parts per multipart upload |
| `policy.presign_put_ttl` | duration | `15m` | TTL for single-upload pre-signed PUT URLs |
| `policy.presign_get_ttl` | duration | `15m` | TTL for download pre-signed GET URLs |
| `policy.presign_part_ttl` | duration | `15m` | TTL for multipart pre-signed part URLs |
| `policy.allowed_content_types` | []string | `[image/jpeg, image/png, application/pdf, ...]` | Allowed MIME types |
| `policy.labels_max_bytes` | int | `4096` | Max total byte size of labels map |
| `policy.labels_max_keys` | int | `10` | Max number of label keys |
| `policy.external_ref_max_len` | int | `256` | Max length of `external_ref` field |
| `policy.object_key_max_len` | int | `1024` | Max length of object key |

### `auth`

| Key | Type | Default | Description |
|---|---|---|---|
| `auth.enabled` | bool | `false` | Enable authentication enforcement |
| `auth.admin_key` | string | `""` | Admin bypass key (sent as `Bearer <key>`) |

When `auth.enabled: false`, all requests bypass authentication and receive a `default-tenant` fallback if no tenant is set.

### `security`

| Key | Type | Default | Description |
|---|---|---|---|
| `security.trust_tenant_id_from_request` | bool | `true` | Accept `X-Tenant-ID` header as the tenant identity |
| `security.reject_tenant_mismatch` | bool | `true` | Reject requests where path `tenant_id` and header `X-Tenant-ID` differ |
| `security.enable_rls` | bool | `false` | Enable PostgreSQL row-level security (`app.tenant_id` session variable) |
| `security.log_sensitive` | bool | `false` | Log sensitive fields (disabled in production) |

> [!WARNING]
> `enable_rls` requires migration `003_harden_objects.sql` to be applied first. Enabling without the migration causes silent runtime errors.

### `timeouts`

| Key | Type | Default | Description |
|---|---|---|---|
| `timeouts.fast_operation` | duration | `5s` | Get, GetMeta, Delete, PatchMeta, GetMultipart |
| `timeouts.default_operation` | duration | `30s` | CreateSingle, List |
| `timeouts.s3_operation` | duration | `60s` | Pre-sign, InitiateMultipart, SignPart, AbortMultipart |
| `timeouts.long_operation` | duration | `2m` | CompleteMultipart |

### `rate_limit`

| Key | Type | Default | Description |
|---|---|---|---|
| `rate_limit.requests_per_second` | float64 | `300` | Sustained token-bucket rate per tenant |
| `rate_limit.burst` | int | `500` | Max burst tokens per tenant |
| `rate_limit.max_tenants` | int | `10000` | Max tracked tenant limiters before LRU eviction |
| `rate_limit.cleanup_ttl` | duration | `10m` | Idle TTL before tenant limiter is pruned |
| `rate_limit.cleanup_interval` | duration | `5m` | Periodic cleanup interval |

### `cache`

| Key | Type | Default | Description |
|---|---|---|---|
| `cache.enabled` | bool | `true` | Enable in-process LRU cache (used for categories) |
| `cache.max_size` | int | `1000` | Max number of entries |
| `cache.ttl` | duration | `5m` | Cache entry TTL |

### `idempotency`

| Key | Type | Default | Description |
|---|---|---|---|
| `idempotency.enabled` | bool | `true` | Enable idempotency key checking |
| `idempotency.ttl` | duration | `24h` | How long idempotency records are kept |

### `otel`

| Key | Type | Default | Description |
|---|---|---|---|
| `otel.enabled` | bool | `false` | Enable OpenTelemetry traces and metrics |
| `otel.endpoint` | string | `otel-collector:4317` | OTLP exporter endpoint |
| `otel.protocol` | string | `grpc` | Exporter protocol: `grpc` or `http` |
| `otel.insecure` | bool | `true` | Skip TLS for OTLP (local) |
| `otel.resource.service.name` | string | `paladin` | OTel resource service name |
| `otel.resource.deployment.environment` | string | `local` | OTel resource environment |

### `housekeeping`

| Key | Type | Default | Description |
|---|---|---|---|
| `housekeeping.enable_reaper` | bool | `true` | Enable background reaper goroutine |
| `housekeeping.pending_ttl` | duration | `24h` | TTL for objects stuck in `pending` status |
| `housekeeping.multipart_ttl` | duration | `72h` | TTL for expired multipart uploads |
| `housekeeping.audit_log_ttl` | duration | — | TTL for audit log entries (if set) |
| `housekeeping.gc_interval` | duration | `1h` | How often the reaper runs a cleanup cycle |
| `housekeeping.delete_orphaned_parts` | bool | `false` | Physically delete orphaned S3 multipart parts |

## Kubernetes Secret Injection

Passwords and access keys are resolved from Kubernetes Secrets at startup via `internal/config/resolver.go`. `SecretRef` objects are read using the in-cluster Kubernetes API. The resolved values replace the inline YAML values before any service is initialized.
