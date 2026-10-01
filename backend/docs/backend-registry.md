# Storage backends at runtime

How the planes reach S3 when there is more than one backend. The decision is
[ADR-0015](../../docs/adr/0015-per-tenant-bucket-layout.md).

## One client per backend

`s3adapter.BackendRegistry` (`internal/storage/s3adapter/registry.go`) holds
one S3 client per backend id, built from `storage.backends.<id>` in the config
on first use and cached for the life of the process.

- `Warmup` builds every configured backend at boot, so a misconfigured one
  stops the process instead of failing its first request.
- `For(id)` requires an id. An empty or unknown id is an error; there is no
  default backend to fall back to.
- `Invalidate(id)` drops a cached client so the next call rebuilds it, after
  a credential change.
- Each client labels its `paladin_storage_calls_total` and
  `paladin_storage_call_duration_seconds` with `backend_id`.

Connection and credentials come from config. The operational flags —
`enabled`, `read_only`, `maintenance` — live in the database and are enforced
by the bucket resolver, not by the registry, which still builds a client for a
disabled backend so a drain or migration can read from it.

## Routing

The handlers do not know about backends. Routers in
`internal/storage/s3adapter/router.go` implement the handler-facing storage
interfaces (object, presign, multipart, bucket provisioning) and pick the
client from the backend id the call carries, from the object's or bucket's
`(backend_id, bucket)`. With one backend configured every call resolves to the
same client.

`CopyObject` between two objects on the same backend is a server-side S3 copy.
Across backends it streams: `GetObject` from the source into a multipart
upload on the destination, without buffering the whole object. This is the
path an operator-started storage migration uses.

## Credentials

Each backend has its own `auth.mode` — `static_keys`, `default_chain`,
`assume_role` or `web_identity` — and optional `sse`. The rules are in
[docs/configuration.md](../../docs/configuration.md#storage-backend-auth-modes).

## Backends created through the API

`BackendService.CreateBackend` stores a backend row with its endpoint and a
`credentials_secret_ref`. The admin plane can probe such a backend: it reads
`access_key_id` and `secret_access_key` from that Kubernetes Secret and builds a
throwaway client (`internal/app/backend_prober.go`).

The registry does not read those rows: a backend that exists only in the
database has no client on the data plane or the worker. `BucketService.CreateBucket`
therefore refuses, with `FailedPrecondition`, a bucket on any backend that
`storage.backends` does not declare. A backend registered through the API can
be listed and probed; to hold buckets it has to be declared in the
configuration.
