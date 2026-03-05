# Logging

**Source:** `internal/logger/`, `internal/middleware/request_logger.go`, `internal/middleware/audit_log.go`, `internal/middleware/context_logger.go`

## Logging Library

- **Library:** `go.uber.org/zap`
- **Format:** JSON (`format: json`) or console (`format: console`)
- **Initialization:** `internal/logger` package — `NewLogger(cfg)` creates a production or development Zap logger based on configuration.
- **Bootstrap Logger:** `logger.NewBootstrapLogger()` is used before config is loaded (writes to stderr, console format).
- **Context Logger:** The logger is attached to each request context via `ContextLogger` middleware → available throughout the call stack via `logger.FromContext(ctx)`.

## Log Sampling

When `logger.sampling.enabled: true`:

- First `initial` log entries per second are passed through.
- Thereafter, every nth entry (`thereafter`) is passed.
- Reduces log noise during sustained high traffic.

## Standard Fields

All log entries emitted by the service include the following base fields (set at logger construction):

| Field | Value Source | Description |
|---|---|---|
| `service` | `logger.fields.service` | Service name (`paladin`) |
| `env` | `logger.fields.env` | Environment label |

Request-scoped fields added by middleware:

| Field | Source | Description |
|---|---|---|
| `request_id` | `X-Request-Id` header or generated UUID | Correlation ID |
| `method` | HTTP method | Request method |
| `path` | URL path | Request path template |
| `status` | HTTP response status code | Response code |
| `latency` | Request duration | Response latency as float64 seconds |
| `client_ip` | `X-Forwarded-For` or TCP remote addr | Client IP |

gRPC interceptor adds:

| Field | Source | Description |
|---|---|---|
| `request_id` | `x-request-id` gRPC metadata value | Correlation ID |
| `method` | `info.FullMethod` | Full gRPC method name |
| `code` | gRPC status code string | Response code |

## Log Event Taxonomy

### Request Logging (HTTP)

Emitted by `RequestLogger` middleware after each request:

- **Success:** No explicit log emitted (only error cases are logged by default to reduce verbosity).
- **Error:** `log.Warn("gRPC request error", ...)` with `method`, `code`, and `error` fields.
- **Probe requests:** Debug-level log when `server.log_probes: true`.

### gRPC Request Logging

Emitted by `LoggerInterceptor` after each unary call:

- **Error calls:** `log.Warn("gRPC request error", method, code, error)`
- **Success calls:** Not logged (commented out to avoid spam).

### Panic Recovery

- HTTP: Gin's built-in `gin.Recovery()` recovers panics and returns `500`.
- gRPC: `RecoveryInterceptor` — `log.Error("gRPC panic recovered", panic)` → returns `codes.Internal`.

### Shutdown

- `log.Info("Shutdown signal received")` on SIGINT/SIGTERM.
- `log.Warn("Server exited", error)` if a server goroutine returns with an error.

### Reaper Worker

- `log.Info("Reaper started", interval)` on startup.
- `log.Info("Reaper stopping", error)` on context cancellation.
- `log.Info("Cleaning up expired pending object", id, key)` per object cleaned.
- `log.Error("Failed to list expired pending objects", error)` on query failure.
- `log.Info("Aborting expired multipart upload", upload_id)` per upload cleaned.
- `log.Info("Pruned expired audit logs", count)` after pruning.

## Redaction Rules

- `security.log_sensitive: false` (default) — sensitive fields such as credentials and tokens are not logged.
- `audit_log.go` records `request_headers` to the DB as JSONB but does **not** log sensitive headers to stdout.
- PII fields: No explicit PII redaction is implemented in code. `security.log_sensitive` controls whether sensitive config is exposed.
- Secrets: Never appear in structured logs; they are injected from Kubernetes Secrets at startup.

## Example Log Entries

### Startup / Info

```json
{
  "level": "info",
  "ts": 1706745600.123,
  "caller": "worker/reaper.go:48",
  "msg": "Reaper started",
  "service": "paladin",
  "env": "prod",
  "interval": "3600000000000"
}
```

### gRPC Error

```json
{
  "level": "warn",
  "ts": 1706745600.456,
  "caller": "middleware/grpc_chain.go:99",
  "msg": "gRPC request error",
  "service": "paladin",
  "env": "prod",
  "request_id": "req-uuid",
  "method": "/paladin.v1.Paladin/CreateObject",
  "code": "InvalidArgument",
  "error": "content_type not allowed"
}
```

### Panic Recovered (gRPC)

```json
{
  "level": "error",
  "ts": 1706745601.000,
  "msg": "gRPC panic recovered",
  "service": "paladin",
  "env": "prod",
  "panic": "runtime error: index out of range"
}
```
