# Paladin Python SDK

Not published to PyPI. Install from the repository, pinned to an SDK release
— a `sdk/go/vX.Y.Z` tag from the
[releases](https://github.com/oleg-tkachuk/paladin/releases):

```bash
pip install "paladin-sdk @ git+https://github.com/oleg-tkachuk/paladin@sdk/go/vX.Y.Z#subdirectory=sdk/python"
```

In a `requirements.txt` or `pyproject.toml`, the same `paladin-sdk @ git+…`
line. Pin the tag: a branch moves under a lock file.

Where the build has no git — a slim image, a vendored copy — install the
tag's source archive instead; `git archive` records the version in it:

```bash
pip install "paladin-sdk @ https://github.com/oleg-tkachuk/paladin/archive/refs/tags/sdk/go/vX.Y.Z.tar.gz#subdirectory=sdk/python"
```

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
with TenantServiceClientSync(
    client.base_url, interceptors=client.interceptors(), http_client=client.http_client()
) as tenants:
    ...
```

The `http_client` relays each response's headers to the SDK, which reads the
server's release and `Retry-After` from them: connect-python shows an
interceptor no response headers. An HTTP client of your own does the same with
its transport wrapped, `pyqwest.SyncClient(paladin.RelaySyncTransport(t))`;
without that, typed errors carry no `server_version` or `retry_after`, retries
ignore `Retry-After`, and the first such error warns with a `RuntimeWarning`.

## `paladin` package

### `Client`

| Member | Does |
| --- | --- |
| `Client(base_url, *, bearer_token=None, api_token=None, capability=None, retry=None, headers=None, token_source=None, audience=None, dpop_key=None)` | A client for the plane at `base_url`. Raises `ValueError` unless it is an absolute `http`/`https` URL. A trailing `/` is dropped. `bearer_token` is sent as `Authorization: Bearer <token>` — an API token (`paladin_pat_…`) and an OIDC JWT are both accepted. `api_token` is sent in `X-Paladin-API-Token`, for a proxy that strips `Authorization`. `capability` is sent in `X-Paladin-Capability`: the JWT, or the `biscuit` that `CapabilityService.Issue` returns beside it. `dpop_key` — a `cryptography` Ed25519 or P-256 private key, with the `dpop` extra installed — proves possession of the key a capability is bound to: each call, each retry included, carries a fresh RFC 9449 proof in `DPoP`; issue the capability with `confirmation_jkt=dpop_thumbprint(key.public_key())`. `headers` are sent on every call and replace what the SDK would send there — `User-Agent` included, which is `paladin-sdk-python/<version>` by default. |
| `base_url` | First argument of every generated client. |
| `interceptors()` | Interceptors for a generated `…ClientSync`. |
| `async_interceptors()` | Interceptors for a generated async `…Client`. |
| `http_client(transport=None)`, `async_http_client(transport=None)` | The `http_client` for a generated client: a `pyqwest` client over `RelaySyncTransport` / `RelayTransport`, wrapping `transport` or pyqwest's shared one. |
| `RelaySyncTransport(inner=None)`, `RelayTransport(inner=None)` | A `pyqwest` transport that relays response headers to the SDK's interceptors. `connect`, `connect_async` and `http_client` build on them. |

### `Retry`

| Member | Does |
| --- | --- |
| `Retry(attempts, base_delay=0.1, max_delay=5.0, retryable=None)` | Retries a unary call that failed with an error `retryable` accepts — by default `default_retryable`: `UNAVAILABLE` or `RESOURCE_EXHAUSTED` — up to `attempts` calls in total. The wait (seconds) is drawn at random up to a ceiling that doubles from `base_delay` to `max_delay`, and is never shorter than the server's `Retry-After`, in seconds or an HTTP date (`parse_retry_after`). A retry that could not start before the call's `timeout_ms` is not made, and the server's error is raised. Only calls safe to repeat are retried: RPCs the contract declares side-effect free or idempotent, and calls that carry an idempotency key — which every other call does, see below. Streams are never retried. A `connectrpc.client.ResponseMetadata` around a retried call sees the last attempt's headers. Raises `ValueError` for `attempts < 1` or delays outside `0 < base_delay <= max_delay`. |
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
| `no_idempotency_key()` | A context manager: every call inside the block goes out with no key — not the default one, not the request's field — and so is never retried, unless the contract declares it side-effect free or idempotent. For an operation that must run again when repeated rather than be answered with the first response. The server refuses `Create*` and `Issue*` without a key. The innermost block wins. |

### Tokens

| Name | Does |
| --- | --- |
| `Session.sign_in(iam_url, subject, password, *, clock=time.monotonic)` | Signs in at the IAM plane and keeps the refresh token. `token(audience)` returns that plane's access token: the IAM one by refreshing, the others by `ExchangeAudience`. Each is cached until `TOKEN_REFRESH_MARGIN` (30s) before it expires. When the refresh token itself is refused, the session signs in again. Thread-safe; concurrent callers wait for one mint. |
| `Session.from_refresh_token(iam_url, refresh_token)` | Resumes from a stored refresh token; cannot sign in again when it expires. `refresh_token` is the current one to store — refreshing rotates it. |
| `AsyncSession` | The same for asyncio: `await AsyncSession.sign_in(...)`, `await session.token(audience)`. |
| `StaticToken(token)` | The same token for every plane: an API token, or a JWT from elsewhere. |
| `Client(..., token_source=…, audience=…)` | Sends `token_source`'s token for `audience` on every call. A call refused as unauthenticated is made once more with a fresh one — the server authenticates before anything else, so the first attempt changed nothing. `connect` sets `audience` per plane. |
| `AUDIENCE_DATA`, `AUDIENCE_ADMIN`, `AUDIENCE_IAM` | The audience names; `tests/test_headers.py` keeps them equal to the Go SDK's, which the server imports. |

### Narrowing a capability offline

`CapabilityService.Issue` and `Delegate` return `biscuit` beside `token`: the
same capability as a Biscuit v3 token, which whoever holds it can narrow with
no key and no call to the server — to hand a sub-agent less than it was given.
Install the `biscuit` extra (`pip install "paladin-sdk[biscuit] @ git+…"`);
`biscuit-python` ships wheels for CPython 3.10–3.13, and elsewhere builds
from source with Rust.

```python
from datetime import datetime, timedelta, timezone

narrowed = paladin.attenuate(
    issued.biscuit,
    ops=["get"],
    resource_prefixes=["corpus/public/"],
    planes=["data"],  # the capability planes: "data", "admin", "mcp"
    expires_at=datetime.now(timezone.utc) + timedelta(minutes=10),
)
client = paladin.Client(data_url, capability=narrowed)
```

| Name | Does |
| --- | --- |
| `attenuate(token, *, ops=None, resource_prefixes=None, resource_uris=None, planes=None, expires_at=None, bind_jkt=None, max_requests=None, max_budget_micros=None)` | `token` with one more block appended, as a new token; `token` keeps working. An argument left `None` leaves that dimension as it is; a set one replaces it and must be within what the token allows, or the server refuses the whole token. Setting `resource_prefixes` or `resource_uris` replaces both. `expires_at` is timezone-aware and kept to the second. `bind_jkt` (`dpop_thumbprint(key.public_key())`) binds the token to a key, and only an unbound token can be bound. `max_requests` and `max_budget_micros` (positive `int`s) give the copy limits of its own, counted apart from other copies and within every limit already in force; `CapabilityService.get_biscuit_usage` reports each limit in force on a copy with what has been counted against it. Raises `ValueError` for a JWT or anything else that is not a Paladin Biscuit, `ImportError` without the extra. |

The block holds only the facts the server reads
([`sdk/testdata/biscuit_vocabulary.json`](../testdata/biscuit_vocabulary.json),
which the server's tests check too); a token the SDK attenuated is verified by
the server's code in `capability/`'s tests.

`CapabilityService.revoke_biscuit` revokes one copy: the token sent and every
copy attenuated from it. The copy it came from, its siblings and the
capability's JWT keep working; `revoke` stops them all.

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
| `upload(p.data, *, parent, content_type, body, size, key="", metadata=None, tags=None, multipart_threshold=8 MiB, part_concurrency=3, on_session=None, resume=None)` | Uploads exactly `size` bytes of `body` (0 is an empty object) and completes the object. `body` is bytes or a binary file, seekable or not (a pipe, a response body); it is read once, front to back. Every upload URL is signed for its body's size and SHA-256, so a body is hashed before it is presigned and held in memory while it is sent: the whole object up to the threshold, in one presigned PUT whose checksum is recorded on the object; above it multipart, one part per request and `part_concurrency` parts in flight, aborted if any part fails. `checksum(algorithm, data)` computes the value for a caller that presigns itself. `on_session` receives the open multipart session as an `UploadSession` (`object_name`, `upload_id`, `part_size`, `total_parts`) and leaves a failed upload open; `resume=session` continues it, sending only the parts storage does not hold. Synchronous; from asyncio, run it with `asyncio.to_thread`. |
| `download_stream(p.data, name, *, offset=0, length=0)` | The object's content as a file-like `ObjectReader` — `read`, `readinto`, `chunks()`, `content_type`, `content_length`, `object` — streamed, never held whole; use it in `with`. `offset`/`length` read a byte range (`RangeIgnoredError` when storage answers with the whole object). A whole read is verified: the read that reaches the end raises `IntegrityError` when the size or the recorded checksum (SHA-256, MD5; CRC32C with the `crc32c` extra) does not match. A retried request goes through a URL bound to the object's ETag as it was first read: `ObjectChangedError` when the object was replaced in between. |
| `download(p.data, name, *, offset=0, length=0)` | `download_stream` read whole, as bytes. |

### Transfers

`upload` and `download` move bytes through presigned URLs, straight to the
storage backend. Those requests are not Paladin calls: the client's
credentials, retries and interceptors are not on them. They go through the
data plane's `Transfer`, declared once, which retries each on its own: a busy
store, a transport failure or an expired URL sends it again, up to
`attempts` times, through a freshly presigned URL, and a URL within
`PRESIGN_EXPIRY_SKEW` (30 s) of its expiry is presigned again before it is
sent. A single PUT whose retry meets an object already stored (412, the URL's
If-None-Match) completes. `expired(err)` and `already_stored(err)` classify a
`TransferError`:

```python
transfer = paladin.Transfer(
    # URLs signed for the public storage host, sent to its in-cluster address
    # with the signed Host header kept.
    split_horizon=("https://s3.example.com", "http://seaweedfs-s3.storage.svc:8333"),
)
p = paladin.connect(endpoints, token_source=session, transfer=transfer)
```

| Name | Does |
| --- | --- |
| `Transfer(*, split_horizon=None, rewrite=None, connect_timeout=10.0, read_timeout=30.0, pool_max_idle_per_host=32, transport=None, attempts=4)` | With no arguments: a connection timeout and a read timeout (`DEFAULT_TRANSFER_…`), but no bound on a whole transfer; 32 connections per host kept for reuse; redirects refused; `DEFAULT_TRANSFER_ATTEMPTS` attempts per presigned request (`ValueError` below 1). Thread-safe, and meant to be shared. |
| `split_horizon=(signed_origin, internal_origin)` | A URL signed for `signed_origin` is sent to `internal_origin`, keeping `Host: <signed host>`: the signature covers the header, not the address. Other origins go as signed. `ValueError` for anything but `scheme://host[:port]`. |
| `rewrite=fn` | The general form: any URL to any URL, the signed Host still kept. Not with `split_horizon`. |
| `transport=` | A `pyqwest.SyncHTTPTransport` of your own — a proxy, TLS settings. Build it with `follow_redirects=False`: one that follows them cannot be stopped from here. |
| `connect(…, transfer=t)` | Every `upload` and `download` through that data plane uses `t`; without it, a shared default. |
| `stream(method, signed, headers=None, content=None)`, `astream(…)` | One presigned request of your own — a URL the server signed that the workflows do not send, such as a PUT minted for another service. A context manager yielding storage's 2xx `pyqwest` response, unread; `method` applies when `signed.method` is empty. The signed Host and `signed.required_headers` are sent, and the rewrite applied; `Content-Length` among them too, since a streamed body has no length the HTTP client could find — send a body of exactly that length. Any other status is a `TransferError`, reported to the hooks; on success report it yourself with `ended(method, host_of(signed), moved, started, None)`, `started` from `time.monotonic()` before the call. |
| `TransferError` | A request storage refused, or answered with a redirect: `method`, `host` (the URL's query is the signature and is not kept), `status`, and the first 512 bytes of the `body`. |

### TLS

`TLS` makes connections with a CA bundle, a client certificate and an
expected server identity, re-read whenever the files change on disk, so
certificates a workload-identity agent rotates are picked up without a
restart. Give it to `connect` for the connections to Paladin, and to a
`Transfer` for those to storage. It matches the Go SDK's `TLS`: the same
fields, defaults, checks and errors.

```python
identity = paladin.TLS(
    ca_file="/var/run/secrets/spiffe/bundle.pem",
    cert_file="/var/run/secrets/spiffe/svid.pem",
    key_file="/var/run/secrets/spiffe/svid-key.pem",
    server_id="spiffe://cluster.local/ns/paladin/sa/paladin-core",
)
p = paladin.connect(endpoints, token_source=session, tls=identity,
                    transfer=paladin.Transfer(tls=identity))
```

| Name | Does |
| --- | --- |
| `TLS(*, ca_file=None, cert_file=None, key_file=None, reload_interval=30.0, server_id=None, verify_peer=None, min_version=DEFAULT_TLS_MIN_VERSION)` | `ca_file` is the only trust when given; without it the system roots. `cert_file` and `key_file` are both or neither (`TLSKeyPairError`). The files are checked for a change at most every `reload_interval` seconds (`DEFAULT_TLS_RELOAD_INTERVAL`); a rotation caught half-written keeps the last good files. |
| `server_id` | The SPIFFE ID the server must present. Its certificate is verified as an X.509-SVID — one URI SAN, not a CA, `digitalSignature` — against `ca_file` as the trust bundle, instead of against the host name, which an SVID does not carry. A malformed ID is `ServerIDError` at once, and so is a mismatch at connection; without `ca_file` it is `ServerIDNeedsCAError`. |
| `verify_peer` | Called with the server's leaf, a `cryptography` `x509.Certificate`, after the built-in checks pass — for an audit log, or a check of your own: an exception refuses the connection. |
| `min_version` | The lowest TLS version offered, an `ssl.TLSVersion`: `DEFAULT_TLS_MIN_VERSION`, TLS 1.2, or TLS 1.3. Lower is `TLSMinVersionError`. |
| `connect(…, tls=t)`, `connect_async(…, tls=t)` | The connections to Paladin. `tls` builds the `http_client`; giving both is `TLSAndHTTPError`. |
| `Transfer(tls=t)` | The connections to storage. Not with `transport=` (`TLSAndHTTPError`). |
| `t.sync_transport(**settings)`, `t.async_transport(**settings)` | The rotating transports these build, for a client of your own — the Go SDK's `TLS.RoundTripper()`. `settings` are pyqwest's names for what they honour: `connect_timeout`, `read_timeout`, `pool_idle_timeout`, `pool_max_idle_per_host`, `enable_otel`, `tracer_provider`. Any other pyqwest setting is deprecated: it keeps a pyqwest transport, without `server_id`, `verify_peer`, `min_version` or the closing below, and is refused in the next release. To wrap one (a circuit breaker, metrics), keep the SDK's header relay outermost: `Client.http_client(transport=wrap(t.sync_transport()))`. |
| `NoCAError`, `ServerIDError`, `ServerIDNeedsCAError`, `TLSAndHTTPError`, `TLSKeyPairError`, `TLSMinVersionError` | The Go SDK's `ErrNoCA`, `ErrServerID`, `ErrServerIDNeedsCA`, `ErrTLSAndHTTP`, `ErrTLSKeyPair` and `ErrTLSMinVersion`, each a `ValueError`. A refused server reaches the caller as a `ConnectError` whose `__cause__` is the `ServerIDError`. |

After a rotation every new request goes out on a connection made with the
new files, over HTTP/2 as well as HTTP/1.1; a request in flight finishes on
its own, and the old connections close once nothing uses them.

The connections under `TLS` are made by the standard library's `ssl`, under
httpcore: pyqwest, the HTTP stack connect-python runs on, has no hook to
check a peer or set a protocol floor. The SPIFFE ID and `verify_peer` are
checked after the handshake and before anything is written, so a refused
server never receives a request, a token or a body. Responses are decoded and
errors raised as pyqwest does — a timeout is a `TimeoutError`, so the call's
`timeout_ms` still ends it with `DEADLINE_EXCEEDED`. Plaintext connections
stay on pyqwest.

### Errors

Every failed call through a `Client`'s interceptors — so through `connect` —
raises a `PaladinError` subclass for its code. Each is still a
`ConnectError`, so an `except ConnectError` keeps working. Catch these, not
codes or messages:

| Exception | Code | Means |
| --- | --- | --- |
| `NotFoundError` | `NOT_FOUND` | |
| `AlreadyExistsError` | `ALREADY_EXISTS` | |
| `PermissionDeniedError` | `PERMISSION_DENIED` | |
| `FailedPreconditionError` | `FAILED_PRECONDITION` | |
| `VersionConflictError` | `ABORTED` | The resource changed since it was read: read it again and retry the change. |
| `ResourceExhaustedError` | `RESOURCE_EXHAUSTED` | `retry_after` is how long the server asked to wait, in seconds. |
| `UnauthenticatedError` | `UNAUTHENTICATED` | |
| `ContractSkewError` | `UNIMPLEMENTED` | The server does not implement the call: it is older than the SDK. The message names the procedure, the server's release (`HEADER_SERVER_VERSION`) and the SDK's. |

Each carries `procedure`, the server's `reason` (a
`paladin.common.v1.error_reason_pb2.ErrorReason` value, from the
`google.rpc.ErrorInfo` in domain `ERROR_DOMAIN`; `reason_name` is its name),
`retry_after`, `server_version`, `sdk_version` and the `decoded_details`.
`reason(err)` returns the reason of any exception,
`ERROR_REASON_UNSPECIFIED` when there is none — or one newer than this SDK,
which a caller treats as the kind alone. A code with no kind (`INTERNAL`,
`UNAVAILABLE`, …) stays a plain `ConnectError`.

```python
from paladin.common.v1 import error_reason_pb2

try:
    p.admin.collection.delete_collection(req)
except paladin.NotFoundError:
    pass  # already gone
except paladin.FailedPreconditionError as err:
    if err.reason == error_reason_pb2.ERROR_REASON_COLLECTION_NOT_EMPTY:
        ...  # empty it first
```
### Resource names and object URIs

The server's naming rules, as types (`paladin.names`): a name built here is
one it accepts, and a name parsed here is one it would. Under a tenant a
name takes the tenant's **id**, a UUID, not its slug; a collection may
contain `/`; object and version ids are UUIDs. `InvalidNameError` (a
`ValueError`) for anything else. `str(name)` prints it; `.parse` reads it.

| Type | Form |
| --- | --- |
| `TenantName` | `tenants/{tenant}` — the id or the slug |
| `CollectionName` | `tenants/{tenant-id}/collections/{collection}` |
| `ObjectName` | `…/collections/{collection}/objects/{object-id}` |
| `ObjectVersionName` | `…/objects/{object-id}/versions/{version-id}` |
| `ObjectURI` | `paladin://tenants/{tenant-id}/collections/{collection}/keys/{key}` — an object by its key; the collection and the key are escaped path segments |

`lookup_object(p.data, uri)` finds the object an `ObjectURI` (or its string)
names, and `download_uri(p.data, uri, offset=0, length=0)` streams it.

To create a bucket and bind collections to it at every boot, call
`p.data.storage_bootstrap.ensure_tenant_storage`: it is idempotent on the
server, and reports which collections it created and which already existed.

### Observability

Nothing is observed unless asked, and the SDK depends on no OpenTelemetry
package.

| Name | Does |
| --- | --- |
| `Client(…, user_agent_suffix="worker/2.1")`, `connect(…, user_agent_suffix=…)` | Appends the application to the SDK's `User-Agent`, so the server's logs name it. |
| `Hooks(on_retry=…, on_transfer=…)` | `connect(…, hooks=h)` calls `on_retry(RetryEvent)` before each retry — `procedure`, the failed `attempt`, the `wait`, the `error`. `Transfer(hooks=h)` calls `on_transfer(TransferEvent)` as each presigned request ends — `method`, `host`, `bytes` moved, `duration`, `error`. |
| the `paladin` logger (`LOGGER_NAME`) | Retries and transfers at debug, failed transfers at warning, with their fields in the record's `extra` for a structured formatter. |
| `Client(…, interceptors=[…])`, `connect(…, interceptors=[…])` | Interceptors of your own, run outside the SDK's, so one that times or traces a call covers its retries. |

**OpenTelemetry** comes from the HTTP stack connect-python runs on,
`pyqwest`, which makes a span for every request and sends W3C trace
context:

```python
http = pyqwest.SyncClient(
    paladin.RelaySyncTransport(pyqwest.SyncHTTPTransport(tracer_provider=provider))
)
p = paladin.connect(endpoints, transport={"http_client": http},
                    transfer=paladin.Transfer(otel=True, tracer_provider=provider))
```

connect-python's own `connectrpc-otel` 0.2.0 does not work with
connect-python 0.9.0 — it reads `RequestContext.method` as an attribute,
which 0.9.0 has as a method — so it is not used here.

### Verifying webhook deliveries

An HTTP event subscription with a signing secret receives each delivery with
`X-Paladin-Webhook-Signature: t=<unix seconds>,v1=<hex>`, an HMAC-SHA256 of
`<t>.<body>`. Verify it against the raw body before trusting anything in it:

```python
try:
    paladin.verify_webhook(secret, request.headers[paladin.HEADER_WEBHOOK_SIGNATURE], raw_body)
except (KeyError, paladin.WebhookSignatureError):
    return 401
```

| Name | Does |
| --- | --- |
| `verify_webhook(secret, header, body, *, tolerance=DEFAULT_WEBHOOK_TOLERANCE, now=None)` | Returns when one `v1` matches under `secret`, compared in constant time, and `t` is within `tolerance` seconds (300) of `now`, either way; otherwise raises `WebhookSignatureError`, a `ValueError`. Standard library only, no extra. |
| `sign_webhook(secret, t, body)` | The header value the server sends, for a test of your own handler. |

A replay inside the window verifies: deduplicate on `X-Paladin-Event-Id`,
which is stable across retries.

### Testing with a fake: `paladin.testing`

`FakePaladin` is an in-memory data plane for the tests of a program built on
the SDK. Uploads and downloads go through presigned URLs on its own storage,
as against the real server.

```python
from paladin.testing import FakePaladin

with FakePaladin() as fake:
    p = fake.connect()
    obj = paladin.upload(p.data, parent=str(fake.collection()), key="a.pdf",
                         content_type="application/pdf", body=body, size=len(body))
    assert fake.content(obj.name) == body
```

It serves `ObjectService` (upload, complete, get, lookup, list, download,
delete), `MultipartUploadService`, `PresignService` (`regenerate_upload_url`,
`presign_download`) and `StorageBootstrapService`; every other RPC answers
`UNIMPLEMENTED`. Like the server it refuses a collection
named by the tenant's slug and a completion whose ETag, when given, is not
the content's; completing a completed object returns it. Like the server it
holds one object per key: an upload to a key another object holds, in any
state and the trash included, is refused with `AlreadyExistsError` until a
`permanent` delete frees it, and `lookup_object` finds an object in any state
but deleted. It keeps an upload's metadata and tags. It records the
checksum an upload completes with — so a download verifies — and answers
range requests. `ensure_tenant_storage` reports a bucket and collections
created the first time and existing after, for any backend id. `put` stores
an object directly; `mark_failed` fails a pending one, as the server's
reconciler does when its URL expired with nothing stored; `tenant` and `collection()` name the fake's tenant and
its collections, all of which exist; `requests()` lists the RPCs received,
each a `Request` with its `procedure`, `headers` by lower-case name and
`message` — a copy of the request — for a test of what the client sent.
`calls(procedure, match=None)` returns a procedure's requests whose message
`match` accepts, so a test sharing the fake counts its own:

```python
mine = fake.calls("/paladin.data.v1.ObjectService/GetObject", lambda m: m.name == obj.name)
```

`presign_download` signs a GET on the fake's storage, as the server does: it
expires after `DEFAULT_DOWNLOAD_TTL` (15 minutes) unless the request names a
TTL up to `MAX_PRESIGN_TTL`, carries `If-Match` when `require_etag_match` is
set, and answers with the request's `content_disposition`. A pending or
failed object is `FAILED_PRECONDITION`, an unknown one `NOT_FOUND`.

Failures are injected on both sides. `fail_rpc(procedure, times, code)` makes
the next `times` calls of a procedure of a served service answer `code`, with
the `ErrorInfo` reason the server attaches to it, so the typed errors and
`paladin.reason` read it as they would the server's; calls after those are
served, every failed one is in `requests()`, and the function it returns
clears what is left:

```python
fake.fail_rpc("/paladin.data.v1.ObjectService/GetObject", 1, Code.UNAVAILABLE)
# the first get_object is UNAVAILABLE, the second is served
```

`fail_rpc_if(procedure, match, times, code)` fails only the calls whose
request `match` accepts — one object, one collection — and serves the
procedure's other calls, so tests sharing one fake each fail their own. Its
failures stack; the latest that matches takes a call, and a later `fail_rpc`
still replaces the earlier one. The fake serves each connection on its own
thread, so callers sharing it are not queued behind one another.

`fail_storage` answers storage requests with a status of the test's choosing
— an expired URL, a busy store — and `fail_storage_after_storing` loses a
PUT's answer after storing its body; `storage_ops()` lists what storage
received.

By default the fake serves every call, whatever credential it carries.
`FakePaladin(strict_auth=True)` checks credentials as the data plane does.
The data plane takes the tenant from the credential — there is no tenant
header — so the fake issues the credentials it accepts, each for a tenant:
`issue_bearer_token`, `issue_api_token` (with the server's `paladin_pat_`
prefix) and `issue_capability`. `revoke(token)` revokes one.

```python
with FakePaladin(strict_auth=True) as fake:
    p = fake.connect(bearer_token=fake.issue_bearer_token(fake.tenant))
```

It refuses as the server does, with the server's code and message and no
`ErrorInfo` reason, since the server's authentication sends none:

| Call | Answer |
| --- | --- |
| No credential, or an `Authorization` that is not a bearer token | `UNAUTHENTICATED` |
| A bearer or API token the fake did not issue, or revoked | `UNAUTHENTICATED` |
| A capability the fake did not issue, or revoked | `PERMISSION_DENIED` — the server's answer to any capability it cannot verify |
| A `name` or `parent` in another tenant, or another tenant's multipart upload | `PERMISSION_DENIED` |

It checks only that: the credential is one it issued, not revoked, and of
the tenant the call names. It verifies no signature, Biscuit, caveat,
scope, audience or expiry, and has no platform admin acting in another
tenant. A refused call is in `requests()`, and is refused before
`fail_rpc`'s failures, which it does not spend.

### Concurrency and asyncio

A `Paladin` from `connect`, a `Transfer` and a `Session` are safe to share
between threads, and meant to be: build one at start-up. An `AsyncPaladin`
from `connect_async` and an `AsyncSession` belong to one event loop. The
connection pools are what make many calls cheap — a `Transfer` keeps
`DEFAULT_TRANSFER_POOL_MAX_IDLE_PER_HOST` connections to each storage host.

Every workflow has an async form for the clients of `connect_async`:
`aupload`, `adownload_stream` (an `AsyncObjectReader`: `await read()`,
`async for` over `chunks()`, `async with`), `adownload`, `alookup_object`,
`adownload_uri`, alongside `apages` and `await_operation`. `aupload` reads
the body in a worker thread, so a file read does not block the loop.

| Name | Does |
| --- | --- |
| `upload(…, part_concurrency=3)` | Parts of one multipart upload in flight at once. |
| `download_many(p.data, names, concurrency=8)` | Downloads many objects, `concurrency` at a time (`DEFAULT_BULK_CONCURRENCY`), and yields `(name, content)` as each finishes — or `(name, error)`, so one bad object does not stop the rest. Each is held whole; for large ones, `download_stream` per object. |
| `adownload_many(p.data, names, concurrency=8)` | The same for the async clients, as an async iterator. |
| `upload_many(p.data, items, concurrency=8, multipart_threshold=8 MiB, part_concurrency=3)` | Uploads many objects, `concurrency` at a time, each as `upload` does, and yields `(item, object)` as each finishes — or `(item, error)`, so one bad item does not stop the rest; each is completed, or aborted, on its own. An item is an `UploadItem(parent, content_type, body, size, key="", metadata=None, tags=None)`, compared by identity. `items` is drawn only as a slot frees, so it may be a generator that opens each file in turn. Up to `concurrency` bodies are held in memory, whole up to the threshold or `part_concurrency` parts above it. A crashed upload to resume goes through `upload`. |
| `aupload_many(p.data, items, …)` | The same for the async clients, as an async iterator. |

### Parity with the Go SDK

Both SDKs run the same scenarios against a live server
([`sdk/testdata/scenarios.json`](../testdata/scenarios.json), in CI's stack
gate) and parse names against the same table
([`sdk/testdata/names.json`](../testdata/names.json)), which the server's
tests hold its parsers to as well. Where they differ, it is on purpose:

| | Go | Python | Why |
| --- | --- | --- | --- |
| Typed errors | `errors.Is(err, paladin.ErrNotFound)`, `*paladin.Error` | `except paladin.NotFoundError`, `PaladinError` | Each language's idiom; the same kinds, fields and reasons. |
| Server identity over TLS | `TLS.ServerID`: the SPIFFE ID, checked by `go-spiffe` | `TLS(server_id=…)`: the same check, after the handshake and before any request byte | Python's connections under `TLS` run on `ssl` and httpcore, because `pyqwest` has no peer-verification hook. |
| Minimum TLS version | `TLS.MinVersion`, 1.2 by default | `TLS(min_version=…)`, `ssl.TLSVersion.TLSv1_2` by default | |
| CRC32C verification | Always | With the `crc32c` extra; otherwise not verified | The standard library has no CRC32C. |
| Webhook signatures | `VerifyWebhook`, options for the window and clock | `verify_webhook`, keyword arguments | Each language's idiom; both run the vectors in `sdk/testdata/webhook_signatures.json`. |
| Biscuit attenuation | `capability.Attenuate`, from the capability module | `attenuate`, with the `biscuit` extra | Go uses the server's own code; Python writes the same facts with `biscuit-python`. |
| OpenTelemetry | connect's `otelconnect` and `otelhttp`, through the options | `pyqwest`'s own spans, through `http_client` and `Transfer(otel=True)` | `connectrpc-otel` 0.2.0 fails on connect-python 0.9.0 (BACKLOG). |
| Bulk transfers | `DownloadMany`, a callback per reader; `UploadMany`, objects in input order and failures by index | `download_many` / `adownload_many` and `upload_many` / `aupload_many`, iterators of results as each finishes | Each language's idiom. |
| asyncio | — | An `a…` form of every workflow | Go has goroutines. |

### Cookbook

Runnable recipes in [`examples/`](examples); `tests/test_examples.py` runs each
against `paladin.testing`:

| Recipe | Shows |
| --- | --- |
| [`split_horizon.py`](examples/split_horizon.py) | URLs signed for a public storage host, sent in-cluster with the signed `Host`. |
| [`mtls.py`](examples/mtls.py) | mTLS from a rotating workload identity, for the RPCs and storage. |
| [`rotating_token.py`](examples/rotating_token.py) | A token read from the store operators rotate it in, on every call. |
| [`bulk_ingestion.py`](examples/bulk_ingestion.py) | Many documents through `download_many` and `adownload_many`. |
| [`resumable_multipart.py`](examples/resumable_multipart.py) | A multipart upload resumed from `list_parts` after a crash. |
| [`durable_upload.py`](examples/durable_upload.py) | `upload` tried again until it completes, its multipart session kept where a restart finds it and resumed; its own key, the content's SHA-256 in its metadata, keeps a retry after a lost answer from storing it twice — what an earlier attempt left at the key is taken, completed, or deleted and uploaded again. |
| [`streaming.py`](examples/streaming.py) | Upload from a file, download into a parser, verified at the end. |
| [`migrating_from_connect_json.py`](examples/migrating_from_connect_json.py) | A hand-written Connect-JSON call and its SDK form. |

### Constants

`HEADER_AUTHORIZATION`, `HEADER_API_TOKEN`, `HEADER_CAPABILITY`, `HEADER_DPOP`
and `HEADER_IDEMPOTENCY_KEY` are the header names the server reads, and
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
| `CapabilityService` | `issue`, `delegate`, `revoke`, `revoke_biscuit`, `get_biscuit_usage`, `list`, `get_usage` |
| `CollectionService` | `create_collection`, `get_collection`, `update_collection`, `delete_collection`, `list_collections`, `set_collection_policy`, `bind_collection_to_bucket` |
| `EventSubscriptionService` | `create_subscription`, `get_subscription`, `update_subscription`, `delete_subscription`, `list_subscriptions`, `test_subscription`, `redrive_failed_deliveries` |
| `MCPInspectService` | `inspect`, `list_sessions`, `get_bridge_status` |
| `PlatformOperationService` | `get_operation`, `list_operations`, `cancel_operation` |
| `PolicyService` | `validate`, `simulate_authz`, `get_effective_policy` |
| `QuotaService` | `get_quota`, `set_quota`, `reset_usage` |
| `SystemService` | `get_config`, `get_dispatcher_stats`, `get_platform_stats`, `list_platform_stats_tenants` |
| `TenantBudgetService` | `get`, `set`, `summarize` |
| `TenantService` | `create_tenant`, `get_tenant`, `update_tenant`, `delete_tenant`, `list_tenants`, `set_inherited_policy`, `restore_tenant`, `purge_tenant`, `rename_tenant_slug`, `migrate_tenant_storage_layout`, `get_tenant_storage_migration`, `resolve_renamed_slug`, `get_tenant_default_binding`, `set_tenant_default_binding`, `clear_tenant_default_binding` |

### Data plane — `paladin.data.v1`

| Service | Methods |
| --- | --- |
| `BatchService` | `batch_delete_objects`, `batch_copy_objects`, `batch_restore_objects`, `batch_update_tags` |
| `MultipartUploadService` | `initiate_multipart_upload`, `presign_part`, `complete_multipart_upload`, `abort_multipart_upload`, `list_parts` |
| `ObjectService` | `upload_object`, `download_object`, `get_object`, `lookup_object`, `update_object`, `complete_object`, `delete_object`, `restore_object`, `copy_object`, `list_objects`, `count_objects`, `list_object_versions`, `get_object_version`, `restore_object_version`, `set_object_retention`, `set_object_legal_hold`, `get_object_lock`, `set_object_taint` |
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
(a source archive of a tag reads it from `.git_archival.txt`; a tree with
neither builds as `0.0.0+unknown`). An install from the git tag whose build
had no git — Poetry fetches through Dulwich — records `0.0.0+unknown`, and
`sdk_version()`, the `User-Agent` and errors report the tag's version
instead, read from the install's `direct_url.json` (PEP 610). Pre-1.0, a minor version may break the contract or this
package's own API; see [`docs/upgrading.md`](../../docs/upgrading.md). The buf.validate module the contract's descriptors depend
on ships inside the wheel as `buf.validate`, because no PyPI package provides
it for the `protobuf` runtime.
