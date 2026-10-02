# Paladin Python SDK

Not published to PyPI. Install from the repository:

```bash
pip install "git+https://github.com/oleg-tkachuk/paladin#subdirectory=sdk/python"
```

Two parts, both imported as `paladin`:

- `paladin.admin.v1`, `paladin.data.v1`, `paladin.iam.v1`, `paladin.common.v1` —
  protobuf messages and Connect clients generated from
  [`proto/`](../../proto). The service clients are these, unwrapped: a
  synchronous `…ClientSync` and an asynchronous `…Client` for every service.
- `paladin.client` — a thin client that holds what every call needs: one
  plane's base URL, credentials, the idempotency key, and retries for calls
  that are safe to repeat.

Built on [connect-python](https://github.com/connectrpc/connect-python),
pinned to `0.9.0`. Requires Python 3.10+.

## Planes

Paladin serves three planes on separate listeners. Create one `Client` per
plane you talk to, and build that plane's service clients from it.

| Plane | Package | Services |
| --- | --- | --- |
| Admin | `paladin.admin.v1` | tenants, buckets, backends, policies, quotas, tokens, audit |
| Data | `paladin.data.v1` | objects, multipart uploads, tags, batches, presigning |
| IAM | `paladin.iam.v1` | login, tokens, users, health |

## Example

```python
from paladin import Client, Retry, idempotency_key
from paladin.admin.v1 import tenant_service_pb2
from paladin.admin.v1.tenant_service_connect import TenantServiceClientSync

client = Client("https://admin.paladin.example", bearer_token=api_token, retry=Retry(attempts=3))

with TenantServiceClientSync(client.base_url, interceptors=client.interceptors()) as tenants:
    with idempotency_key(request_id):
        tenant = tenants.create_tenant(tenant_service_pb2.CreateTenantRequest(...))
```

Asynchronously, the same client supplies `async_interceptors()`:

```python
from paladin.admin.v1.tenant_service_connect import TenantServiceClient

async with TenantServiceClient(client.base_url, interceptors=client.async_interceptors()) as tenants:
    tenant = await tenants.get_tenant(tenant_service_pb2.GetTenantRequest(name=name))
```

## `paladin` package

### `Client`

| Member | Does |
| --- | --- |
| `Client(base_url, *, bearer_token=None, api_token=None, capability=None, retry=None, headers=None)` | A client for the plane at `base_url`. Raises `ValueError` unless it is an absolute `http`/`https` URL. A trailing `/` is dropped. `bearer_token` is sent as `Authorization: Bearer <token>` — an API token (`paladin_pat_…`) and an OIDC JWT are both accepted. `api_token` is sent in `X-Paladin-API-Token`, for a proxy that strips `Authorization`. `capability` is sent in `X-Paladin-Capability`. `headers` are sent on every call and replace what the SDK would send there — `User-Agent` included, which is `paladin-sdk-python/<version>` by default. |
| `base_url` | First argument of every generated client. |
| `interceptors()` | Interceptors for a generated `…ClientSync`. |
| `async_interceptors()` | Interceptors for a generated async `…Client`. |

### `Retry`

| Member | Does |
| --- | --- |
| `Retry(attempts, base_delay=0.1, max_delay=5.0)` | Retries a unary call on `UNAVAILABLE` or `RESOURCE_EXHAUSTED`, up to `attempts` calls in total. The wait (seconds) is drawn at random up to a ceiling that doubles from `base_delay` to `max_delay`, and is never shorter than the server's `Retry-After`. A retry that could not start before the call's `timeout_ms` is not made, and the server's error is raised. Only calls safe to repeat are retried: RPCs the contract declares side-effect free or idempotent, and calls that carry an idempotency key — which every other call does, see below. Streams are never retried. Each attempt reads its headers through its own `connectrpc.client.ResponseMetadata`, so one wrapped around a retried call sees nothing. Raises `ValueError` for `attempts < 1` or delays outside `0 < base_delay <= max_delay`. |
| `delays()` | The ceiling of the pause before each retry. |
| `wait(ceiling, retry_after)` | The pause before one retry: a random share of `ceiling`, at least `retry_after`. |

### Idempotency

A unary call the contract does not declare side-effect free or idempotent
sends an `Idempotency-Key` of its own: the one set by `idempotency_key` when
there is one, else the request's `idempotency_key` field when set, else a
fresh random key. Every retry of that call sends the same key. The server
requires one on `Create*` and `Issue*` calls.

| Function | Does |
| --- | --- |
| `idempotency_key(key)` | A context manager: every call inside the block sends `Idempotency-Key: <key>`. The server replays the first response for a key it has seen, so repeating a mutating call with the same key is safe. Reuse a key only for the same logical operation. Scoped with `contextvars`, so it follows `asyncio` tasks. |
| `current_idempotency_key()` | The key set for the current context; an empty key counts as none. |

### Constants

`HEADER_AUTHORIZATION`, `HEADER_API_TOKEN`, `HEADER_CAPABILITY` and
`HEADER_IDEMPOTENCY_KEY` are the header names the server reads, and
`HEADER_USER_AGENT` and `HEADER_RETRY_AFTER` the two the SDK sends and reads
besides; `tests/test_headers.py` keeps them equal to the Go SDK's, which the
server imports. `user_agent()` is the `User-Agent` the client sends. `DEFAULT_RETRY_BASE_DELAY` and `DEFAULT_RETRY_MAX_DELAY` are
`Retry`'s defaults.

## Services and methods

Every method takes the request message and returns the response message; the
keyword arguments `headers` and `timeout_ms` are available on each. The
messages, and what each field means, are documented in the `.proto` files
under [`proto/paladin`](../../proto/paladin).

### Admin plane — `paladin.admin.v1`

| Service | Methods |
| --- | --- |
| `APITokenService` | `create`, `revoke`, `list`, `get_self`, `get_usage` |
| `AuditLogService` | `list_audit_log`, `get_audit_log_entry`, `export_audit_log` |
| `BackendService` | `create_backend`, `get_backend`, `update_backend`, `delete_backend`, `list_backends`, `rotate_credentials`, `test_backend`, `set_backend_enabled`, `set_backend_read_only`, `set_backend_maintenance` |
| `BillingService` | `get_tenant_summary`, `get_tenant_time_series` |
| `BucketService` | `create_bucket`, `get_bucket`, `update_bucket`, `delete_bucket`, `list_buckets`, `set_bucket_policy`, `set_lifecycle_rules`, `set_object_lock`, `set_versioning`, `set_replication`, `list_accessible_buckets` |
| `CELService` | `validate` |
| `CapabilityService` | `issue`, `delegate`, `revoke`, `list`, `get_usage` |
| `CollectionService` | `create_collection`, `get_collection`, `update_collection`, `delete_collection`, `list_collections`, `set_collection_policy`, `bind_collection_to_bucket` |
| `EventSubscriptionService` | `create_subscription`, `get_subscription`, `update_subscription`, `delete_subscription`, `list_subscriptions`, `test_subscription`, `redrive_failed_deliveries` |
| `MCPInspectService` | `inspect`, `list_sessions`, `get_bridge_status` |
| `PlatformOperationService` | `get_operation`, `list_operations`, `cancel_operation` |
| `PolicyService` | `validate`, `simulate_authz`, `get_effective_policy` |
| `QuotaService` | `get_quota`, `set_quota`, `reset_usage` |
| `SystemService` | `get_config`, `get_dispatcher_stats`, `get_platform_stats` |
| `TenantBudgetService` | `get`, `set`, `summarize` |
| `TenantService` | `create_tenant`, `get_tenant`, `update_tenant`, `delete_tenant`, `list_tenants`, `set_inherited_policy`, `restore_tenant`, `purge_tenant`, `rename_tenant_slug`, `migrate_tenant_storage_layout`, `get_tenant_storage_migration`, `resolve_renamed_slug`, `get_tenant_default_binding`, `set_tenant_default_binding`, `clear_tenant_default_binding` |

### Data plane — `paladin.data.v1`

| Service | Methods |
| --- | --- |
| `BatchService` | `batch_delete_objects`, `batch_copy_objects`, `batch_restore_objects`, `batch_update_tags` |
| `MultipartUploadService` | `initiate_multipart_upload`, `presign_part`, `complete_multipart_upload`, `abort_multipart_upload`, `list_parts` |
| `ObjectService` | `upload_object`, `download_object`, `get_object`, `lookup_object`, `update_object`, `complete_object`, `delete_object`, `restore_object`, `copy_object`, `list_objects`, `count_objects`, `list_object_versions`, `get_object_version`, `restore_object_version`, `set_object_retention`, `set_object_legal_hold`, `get_object_lock` |
| `ObjectTagService` | `get_object_tags`, `put_object_tags`, `delete_object_tags`, `list_distinct_tags` |
| `OperationService` | `get_operation`, `list_operations`, `cancel_operation` |
| `PresignService` | `regenerate_upload_url`, `presign_download` |
| `StorageBootstrapService` | `ensure_tenant_storage` |

### IAM plane — `paladin.iam.v1`

| Service | Methods |
| --- | --- |
| `AuthService` | `login`, `refresh_token`, `revoke`, `who_am_i`, `change_password`, `exchange_audience`, `list_my_memberships`, `switch_tenant` |
| `HealthService` | `get_version`, `get_health` |
| `UserService` | `create_user`, `get_user`, `update_user`, `delete_user`, `list_users`, `grant_scopes`, `revoke_scopes`, `reset_password` |
| `UserSettingsService` | `get_mine`, `update_mine`, `get_for_user`, `list_by_tenant`, `delete_for_user` |

`tests/test_readme.py` fails when a generated service or method is missing
from the tables above.

## Development

```bash
uv sync --group dev
scripts/generate.sh     # regenerate the stubs from proto/
uv run pytest
uv run ruff check src tests
```

The stubs are committed; `task -t Taskfile.dev.yaml verify-py-sdk` fails when
they no longer match the contract.

## Versioning

The package version is the API contract's: version X.Y.Z is generated from
`api/vX.Y.Z`. The buf.validate module the contract's descriptors depend
on ships inside the wheel as `buf.validate`, because no PyPI package provides
it for the `protobuf` runtime.
