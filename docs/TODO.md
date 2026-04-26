# TODO

## ObjectKeyService Domain Layer

The `ObjectKeyService` Connect service (5 RPCs) is currently stubbed with `Unimplemented` responses.
Implementing it requires a new object_key domain layer that does not exist yet.

### Stubbed RPCs

| RPC | Proto Path | Description |
|-----|-----------|-------------|
| `CreateObjectKey` | `POST /v1/tenants/{tenant_id}/object_keys` | Create a new object_key |
| `DeleteObjectKey` | `DELETE /v1/tenants/{tenant_id}/object_keys/{name}` | Delete a object_key |
| `ListObjectKeys` | `GET /v1/tenants/{tenant_id}/object_keys` | List object_keys with pagination |
| `GetBucketConfiguration` | `GET /v1/tenants/{tenant_id}/object_keys/{name}/configuration` | Read versioning, lifecycle, ACL |
| `UpdateBucketConfiguration` | `PATCH /v1/tenants/{tenant_id}/object_keys/{name}/configuration` | Update versioning, lifecycle, ACL |

### Required Work

1. **Domain model**: `ObjectKey`, `BucketConfiguration`, `VersioningConfiguration`, `LifecycleRule`, `AccessControlEntry`
2. **Repository**: `BucketRepository` interface + Postgres implementation + migrations
3. **Service**: `ObjectKeyService` domain interface + implementation with authorization
4. **Handler**: Replace stubs in `internal/api/connect/bucket_handler.go`
5. **DI**: Wire the new service into the dependency graph (`wire/sets.go`, `wire_gen.go`)

### Proto Definitions

- [object_key_service.proto](../proto/paladin/v1/object_key_service.proto)
- [types.proto](../proto/paladin/v1/types.proto) — `ObjectKey`, `BucketConfiguration`, lifecycle/ACL types
