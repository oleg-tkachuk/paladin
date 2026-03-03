# Configuration

The `paladin` service uses a structured YAML configuration file. By default, the application looks for `/app/configs/paladin.yaml`, which can be overridden via the `-c` or `--config` flag at runtime.

The configuration structs are defined in `internal/config/types.go`.

## Configuration Schema

Below is the structured configuration schema detailing the sections, keys, and their mapping into the application.

### `app`

Application identity block.

- `name` (string): Service name identifier (default: `paladin`).
- `env` (string): Deployment environment (e.g., `local`, `production`).

### `logger`

Logging behavior powered by `zap`.

- `level` (string): Minimum log level (`debug`, `info`, `warn`, `error`).
- `format` (string): Log output format (`json` or `console`).
- `development` (bool): Enables development mode (stack traces, colored output).
- `fields` (object): Static fields to inject into every log entry (e.g., `service`, `env`).

### `server`

Control HTTP and gRPC server settings.

- `mode` (string): e.g. `release` or `debug`.
- `shutdown_timeout` (duration): Maximum time to wait for a graceful shutdown (e.g., `20s`).
- `http`:
  - `addr` (string): HTTP bind address (e.g., `0.0.0.0:8080`).
  - `read_timeout`, `write_timeout`, `idle_timeout` (durations).
  - `tls`: Enable HTTPS via `enabled`, `cert_path`, and `key_path`.
- `grpc`:
  - `addr` (string): gRPC bind address (e.g., `0.0.0.0:9090`).

### `datastores`

Connections to backing state.

**`postgres`**

- `dsn` (string): Connection string for the PostgreSQL database.
- `password_secret` (object): Optional reference to a Kubernetes Secret for resolving the DB password.
- `pool`: Connection pool parameters (`max_conns`, `min_conns`, `max_conn_lifetime`).

**`s3`**

- `bucket` (string): The logical bucket name.
- `region` (string): AWS region or equivalent.
- `endpoint` (string): URL to the S3-compatible server (e.g., `http://seaweed-s3:8333`).
- `force_path_style` (bool): If true, uses path-style addressing (`http://s3.amazonaws.com/BUCKET/KEY`).
- `access_key` / `secret_key` (strings): Credentials. Can also be resolved via `access_key_secret` / `secret_key_secret`.
- `presign_ttl` (duration): Expiration for generated presigned read URLs (e.g., `15m`).
- `part_size` (string): Multipart part size (e.g., `8MB`).

### `policy`

Business logic invariants and constraints for objects.

- `max_object_size` (string): Maximum total size of an uploaded object (e.g., `100MB`).
- `max_multipart_size` (string): Max size for a single multipart upload part.
- `allowed_content_types` (list of strings): Allowed MIME types.
- Other limits regarding label counts, external references, etc.

### `housekeeping`

Background worker logic settings.

- `enable_reaper` (bool): Whether to run the Reaper job.
- `pending_ttl` (duration): Time before incomplete uploads are reaped.
- `multipart_ttl` (duration): Time before abandoned multipart uploads are aborted.
- `audit_log_ttl` (duration): Time before audit logs are pruned.
- `gc_interval` (duration): How often the Reaper runs.

### `otel`

OpenTelemetry configuration.

- `enabled` (bool): Enables metric and trace exports.
- `endpoint` (string): OTLP collector endpoint.
- `insecure` (bool): If true, disables TLS for the OTLP exporter.
- `resource`: Service identity attributes (`service.name`, `deployment.environment`).

### `security`

- `trust_tenant_id_from_request` (bool): Allows clients to assert their `tenant_id` context.
- `enable_rls` (bool): Assumes Row-Level Security is active in Postgres.
- `log_sensitive` (bool): Enables or disables logging of potentially sensitive request aspects.

## Environment Variable Mapping

While the application primarily loads from YAML, the custom configuration parser may map specific keys or secret references to environment variables if designed within `internal/config`. Additionally, properties like `password_secret` suggest integration with Kubernetes secrets at runtime.

*Source: `internal/config/types.go`*
