# paladin

Presign-only control plane for S3-compatible object storage (AWS S3 / SeaweedFS / MinIO-compatible).

## Features

- **Metadata Management**: Creates and tracks object records in PostgreSQL.
- **Presigned URLs**: Secure, time-limited access for PUT, GET, and Multipart operations.
- **Dual API**: Exposes internal gRPC and external HTTP (Gin) interfaces.
- **CUE-powered Configuration**: Robust validation and default values using CUE.
- **Enhanced Observability**: Comprehensive logging for S3, Postgres, and migrations.

## Configuration

The service uses a CUE schema (`internal/config/schema.cue`) for validation. Configuration is loaded from `configs/paladin.yaml`.

### Logger

Customizable logging levels and formats:

```yaml
logger:
  level: info # debug, info, warn, error, dpanic, panic, fatal
  format: json # json, console
  development: false
  disable_caller: false
  disable_stacktrace: false
```

### Health Probes

Exposes standard endpoints and supports toggling probe logs:

- **Endpoints**: `/health/livez`, `/health/readyz`
- **Config**: `server.log_probes: true|false`

## Local Development

```bash
# Build and start the stack
task build
task up

# Check readiness
curl -s http://localhost:8080/health/readyz | jq .
```

The compose stack includes:

- **Postgres**: Metadata store.
- **SeaweedFS**: S3 gateway and filer.
- **Paladin**: This service.

## Notes

- This service intentionally does not stream data; it only manages access via presigned URLs.
- For SeaweedFS S3 gateway, you can use dummy credentials; the service defaults to static credentials for local development.
