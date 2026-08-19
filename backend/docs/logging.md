# Logging - Paladin

## Overview

Paladin uses [Zap](https://github.com/uber-go/zap) for high-performance, structured logging. Logs are emitted in JSON format by default in production.

## Configuration

- `logger.level`: `debug`, `info`, `warn`, `error`, `fatal`.
- `logger.format`: `json` (production) or `console` (development).
- `logger.development`: Enables development mode (prettier stack traces).

## Common Fields

All structured logs include the following fields where applicable:

- `ts`: Timestamp.
- `level`: Log level.
- `msg`: Log message.
- `request_id`: Unique ID for tracing the request.
- `tenant_id`: The ID of the tenant making the request.
- `user_id`: If authenticated, the user ID.
- `method`: HTTP/Connect method.
- `path`: Request path.
- `status`: Response status code.
- `duration_ms`: Latency.

## Example Request Log

```json
{
  "level": "info",
  "ts": "2026-03-10T15:00:00Z",
  "msg": "HTTP request completed",
  "request_id": "req-123456",
  "tenant_id": "tenant-abc",
  "method": "POST",
  "path": "/v1/objects",
  "status": 201,
  "duration_ms": 45,
  "size_bytes": 1024
}
```

## Log Event Taxonomy

- **Access Logs**: Emitted by Gin/Connect middleware for every request.
- **Dependency Logs**: Connectivity issues with Postgres or S3.
- **Error Logs**: Validated by `internal/errors` and mapped to HTTP/Connect codes.
- **Housekeeping Logs**: Produced by the Reaper worker during cleanup.
