# TODO

## BucketService Domain Layer

The `BucketService` gRPC service (5 RPCs) is currently stubbed with `Unimplemented` responses.
Implementing it requires a new bucket domain layer that does not exist yet.

### Stubbed RPCs

| RPC | Proto Path | Description |
|-----|-----------|-------------|
| `CreateBucket` | `POST /v1/tenants/{tenant_id}/buckets` | Create a new bucket |
| `DeleteBucket` | `DELETE /v1/tenants/{tenant_id}/buckets/{name}` | Delete a bucket |
| `ListBuckets` | `GET /v1/tenants/{tenant_id}/buckets` | List buckets with pagination |
| `GetBucketConfiguration` | `GET /v1/tenants/{tenant_id}/buckets/{name}/configuration` | Read versioning, lifecycle, ACL |
| `UpdateBucketConfiguration` | `PATCH /v1/tenants/{tenant_id}/buckets/{name}/configuration` | Update versioning, lifecycle, ACL |

### Required Work

1. **Domain model**: `Bucket`, `BucketConfiguration`, `VersioningConfiguration`, `LifecycleRule`, `AccessControlEntry`
2. **Repository**: `BucketRepository` interface + Postgres implementation + migrations
3. **Service**: `BucketService` domain interface + implementation with authorization
4. **Handler**: Replace stubs in `internal/api/grpc/bucket_handler.go`
5. **DI**: Wire the new service into the dependency graph (`wire/sets.go`, `wire_gen.go`)

### Proto Definitions

- [bucket_service.proto](../proto/paladin/v1/bucket_service.proto)
- [types.proto](../proto/paladin/v1/types.proto) — `Bucket`, `BucketConfiguration`, lifecycle/ACL types
