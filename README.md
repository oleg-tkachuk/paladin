# paladin

Presign-only control plane for S3-compatible object storage (AWS S3 / SeaweedFS / MinIO-compatible).

## What it does

- Creates object records (metadata) in Postgres
- Returns pre-signed URLs for:
  - PUT object (single-part)
  - GET object (download)
  - Multipart upload (create / sign part / complete / abort)
- Exposes:
  - gRPC API (internal)
  - HTTP API via Gin (external)

## Local run (Docker Compose)

```bash
task build
task up
curl -s http://localhost:8080/health/readyz | jq .
```

The compose stack includes:
- Postgres
- SeaweedFS (master/volume/filer + s3 gateway)
- paladin

## Notes

- This repo intentionally does not stream data through the service.
- For SeaweedFS S3 gateway you can use dummy credentials; the service uses static credentials by default.
