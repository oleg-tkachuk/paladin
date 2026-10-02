# Paladin Python SDK

Not published to PyPI. Install from the repository, pinned to an SDK release
— a `sdk/go/vX.Y.Z` tag from the
[releases](https://github.com/oleg-tkachuk/paladin/releases):

```bash
pip install "paladin-sdk @ git+https://github.com/oleg-tkachuk/paladin@sdk/go/vX.Y.Z#subdirectory=sdk/python"
```

In a `requirements.txt` or `pyproject.toml`, the same `paladin-sdk @ git+…`
line. Pin the tag: a branch moves under a lock file.

Two parts, both imported as `paladin`:

- `paladin.admin.v1`, `paladin.data.v1`, `paladin.iam.v1`, `paladin.common.v1` —
  protobuf messages and Connect clients generated from
  [`proto/`](../../proto). The service clients are these, unwrapped: a
  synchronous `…ClientSync` and an asynchronous `…Client` for every service.
- Three layers over them. What every call needs (`paladin.client`,
  `paladin.auth`): credentials and sessions, idempotency keys, retries.
  `connect` (`paladin.connect`), a client for every service of each plane.
  And what takes more than one call (`paladin.workflows`): paging, waiting on
  an operation, update masks, uploads and downloads.

Built on [connect-python](https://github.com/connectrpc/connect-python).
See [Compatibility](#compatibility) for the Python, protobuf and
connect-python versions it supports.

## Quick start

```python
import paladin
from paladin.admin.v1 import tenant_service_pb2

session = paladin.Session.sign_in(iam_url, subject, password)
p = paladin.connect(
    paladin.Endpoints(data=data_url, admin=admin_url, iam=iam_url),
    token_source=session,
    retry=paladin.Retry(attempts=3),
)
# Every service of every plane; each call carries the token for its plane
# and, when it has side effects, an idempotency key.
tenant = p.admin.tenant.create_tenant(tenant_service_pb2.CreateTenantRequest(...))
```

With an API token instead of a session, pass
`token_source=paladin.StaticToken(api_token)`. For asyncio,
`paladin.connect_async(...)` with an `AsyncSession` returns the async clients.

## Planes

Paladin serves three planes on separate listeners, and a token is issued for
one of them. `connect` builds a client for every service of each plane you
give a URL — `p.data`, `p.admin`, `p.iam`, `None` for a plane left out — and
sends each the token for its own audience. The attributes are the services'
names in snake case without `Service`: `p.data.object`,
`p.admin.event_subscription`, `p.iam.auth`. They are generated from the
contract (`scripts/gen_facade.py`, run by `generate.sh`), and a test fails
when they fall behind it.

| Plane | Package | Services |
| --- | --- | --- |
| Admin | `paladin.admin.v1` | tenants, buckets, backends, policies, quotas, tokens, audit |
| Data | `paladin.data.v1` | objects, multipart uploads, tags, batches, presigning |
| IAM | `paladin.iam.v1` | login, tokens, users, health |

For one plane alone, a `Client` supplies what a generated client takes:

```python
from paladin.admin.v1.tenant_service_connect import TenantServiceClientSync

client = paladin.Client(admin_url, token_source=session, audience=paladin.AUDIENCE_ADMIN)
with TenantServiceClientSync(client.base_url, interceptors=client.interceptors()) as tenants:
    ...
```

## `paladin` package

### `Client`

| Member | Does |
| --- | --- |
| `Client(base_url, *, bearer_token=None, api_token=None, capability=None, retry=None, headers=None, token_source=None, audience=None)` | A client for the plane at `base_url`. Raises `ValueError` unless it is an absolute `http`/`https` URL. A trailing `/` is dropped. `bearer_token` is sent as `Authorization: Bearer <token>` — an API token (`paladin_pat_…`) and an OIDC JWT are both accepted. `api_token` is sent in `X-Paladin-API-Token`, for a proxy that strips `Authorization`. `capability` is sent in `X-Paladin-Capability`. `headers` are sent on every call and replace what the SDK would send there — `User-Agent` included, which is `paladin-sdk-python/<version>` by default. |
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

### Tokens

| Name | Does |
| --- | --- |
| `Session.sign_in(iam_url, subject, password, *, clock=time.monotonic)` | Signs in at the IAM plane and keeps the refresh token. `token(audience)` returns that plane's access token: the IAM one by refreshing, the others by `ExchangeAudience`. Each is cached until `TOKEN_REFRESH_MARGIN` (30s) before it expires. When the refresh token itself is refused, the session signs in again. Thread-safe; concurrent callers wait for one mint. |
| `Session.from_refresh_token(iam_url, refresh_token)` | Resumes from a stored refresh token; cannot sign in again when it expires. `refresh_token` is the current one to store — refreshing rotates it. |
| `AsyncSession` | The same for asyncio: `await AsyncSession.sign_in(...)`, `await session.token(audience)`. |
| `StaticToken(token)` | The same token for every plane: an API token, or a JWT from elsewhere. |
| `Client(..., token_source=…, audience=…)` | Sends `token_source`'s token for `audience` on every call. A call refused as unauthenticated is made once more with a fresh one — the server authenticates before anything else, so the first attempt changed nothing. `connect` sets `audience` per plane. |
| `AUDIENCE_DATA`, `AUDIENCE_ADMIN`, `AUDIENCE_IAM` | The audience names; `tests/test_headers.py` keeps them equal to the Go SDK's, which the server imports. |

### `connect`

| Name | Does |
| --- | --- |
| `connect(Endpoints(data=…, admin=…, iam=…), *, transport=None, **client_options)` | A `Paladin` with a synchronous client for every service of each plane given. `client_options` are `Client`'s; `transport` goes to every generated client — `timeout_ms`, `http_client`, `proto_json`. `Endpoints()` with no URL raises `ValueError`. |
| `connect_async(...)` | The same with the async clients, as an `AsyncPaladin`. |

### Workflows

| Name | Does |
| --- | --- |
| `pages(call, request, items)` | Calls a List RPC page by page, following `next_page_token`, and yields every element of the repeated field `items`, e.g. `pages(p.data.object.list_objects, ListObjectsRequest(parent=c), "objects")`. Stopping early makes no further calls. `TypeError` for a message without `page`. `apages` is the async form. |
| `wait(get, *, poll=0.5, max_poll=10.0, timeout=None)` | Calls `get` until the operation it returns is done, the pause doubling from `poll` to `max_poll` seconds. Raises `OperationFailed` (with `operation` and the Connect `code`) for one that failed, `TimeoutError` past `timeout`. `await_operation` is the async form; bound it with `asyncio.timeout`. |
| `mask(MessageClass, *paths)` | An update mask from proto field names, nested ones with `.`, each checked against the descriptor: `ValueError` for one it lacks. |
| `upload(p.data, *, parent, content_type, body, size, key="", metadata=None, tags=None, multipart_threshold=8 MiB, part_concurrency=3)` | Uploads `body` (bytes, or a seekable binary file) and completes it: one presigned PUT up to the threshold, multipart above it, aborted if any part fails. A refused transfer raises `TransferError`. Synchronous; from asyncio, run it with `asyncio.to_thread`. |
| `download(p.data, name)` | The object's content, fetched through a presigned URL. |

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

## Compatibility

| | Supported | Why the bound |
| --- | --- | --- |
| Python | 3.10–3.14 | `requires-python`; each is tested |
| `protobuf` | `>=6.33.5,<8` | the stubs' gencode version is the floor; 6.x and 7.x are tested |
| `connect-python` | `>=0.9.0,<0.10` | pre-1.0: a minor may change the API the generated clients call |
| `googleapis-common-protos` | `>=1.75.5,<2` | the first release that accepts protobuf 7 |

Both protobuf majors are supported so the SDK can share an environment with
libraries still on protobuf 6 — `grpcio-tools`, and workflow engines built on
it such as `hatchet-sdk`. CI installs the built wheel beside each protobuf
major on every Python, and beside `hatchet-sdk`, and runs the tests there
([`compat.json`](compat.json); locally,
`task -t Taskfile.dev.yaml verify-py-sdk-compat`).

The stubs are generated with an older `grpcio-tools` on purpose: a stub
refuses a protobuf runtime older than the protoc that generated it, so the
generator's version *is* the floor. `scripts/py-sdk-compat.test.sh` fails when
the stubs, the declared floor and the matrix disagree.

## Versioning

The package version is the SDK's tag, `sdk/go/vX.Y.Z`, cut automatically from
the commits that touch `sdk/` or `proto/`; hatch-vcs reads it at build time
(a checkout without the tags builds as `0.0.0`). Pre-1.0, a minor version may break the contract or this
package's own API; see [`docs/upgrading.md`](../../docs/upgrading.md). The buf.validate module the contract's descriptors depend
on ships inside the wheel as `buf.validate`, because no PyPI package provides
it for the `protobuf` runtime.
