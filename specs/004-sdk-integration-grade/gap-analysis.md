# Gap Analysis: Go and Python SDKs to Integration Grade

**Date**: 2026-10-02 | **SDK version reviewed**: `sdk/go/v0.12.0` (Go and Python) |
**Base**: `main` at `41c31d5a`

Each item of the brief, its state in the code today, and the plan. Verdicts:

- **Gap**: missing; to build.
- **Partial**: some of it exists; the rest is to build.
- **Solved**: already done; evidence given, nothing to build.
- **Wrong as stated**: the brief's premise contradicts the contract or the
  runtime; a decision is needed before building.

The decisions taken are in [Decisions](#decisions) at the end.

---

## 1. Python SDK cannot coexist with protobuf 6 runtimes — **Gap, fix proven**

**Today**

- `sdk/python/pyproject.toml:13` pins `connect-python==0.9.0`.
- `sdk/python/pyproject.toml:16` requires `protobuf>=7.35.1`. The stubs are
  generated with the protoc bundled in `grpcio-tools==1.84.0`, which itself
  requires `protobuf>=7.35.1,<8`. Every `_pb2.py` validates its runtime at
  import against 7.35.1 (`ValidateProtobufRuntimeVersion(PUBLIC, 7, 35, 1, …)`).
- `hatchet-sdk` 1.41.1 (latest, checked on PyPI) requires `protobuf>=6.30.2,<7`.
  `uv pip compile` of `hatchet-sdk==1.41.1` with the SDK's current
  requirements fails: *"your requirements are unsatisfiable"*.
- The SDK is **not on PyPI**, and stays so: it installs from the repository
  (`paladin-sdk @ git+…@sdk/go/vX.Y.Z#subdirectory=sdk/python`). The
  acceptance runs that way, and against the built wheel in CI.

**Evidence the fix works** (a probe, to be repeated on the real stubs in the PR):

- A message generated with `grpcio-tools==1.76.0` (protoc for protobuf
  6.31.1) imports and serialises on protobuf 6.31.1, 6.33.5 and 7.36.2, with
  no warning under `-W always`.
- `hatchet-sdk==1.41.1` + `connect-python==0.9.0` + `protobuf>=6.31.1,<8` +
  `googleapis-common-protos>=1.75.5` resolves on Python 3.10, 3.12 and 3.14
  (to protobuf 6.33.6). protobuf 6.33.6 ships `cp39-abi3` wheels, so the 3.14 ×
  6.x cell runs the compiled runtime and not a pure-Python fallback.

**Plan (PR 1)**

- Generate with `grpcio-tools==1.81.1`, the newest release whose protoc emits
  protobuf-6 gencode (6.33.5); 1.82.x already emits 7.35.0. **This deliberately
  breaks the latest-stable rule**: the generator's age *is* the compatibility
  floor. A comment beside the pin says so.
- Declare `protobuf>=6.33.5,<8`: the gencode version, and also the floor
  `googleapis-common-protos` 1.75.5 needs (the first release accepting 7.x). Regenerate `buf.validate` the same way: it is
  shipped in the wheel (`pyproject.toml`, `packages`).
- `connect-python`: set `>=0.9.0,<0.10`. It is pre-1.0, so a minor may break
  the API the generated clients call. A wider range is not honest until the
  matrix tests it. The README says why.
- CI matrix job: Python 3.10–3.14 × protobuf {6.33.5 (the floor), latest 7.x}.
  Each cell runs the full pytest suite. One more cell installs beside
  `hatchet-sdk==1.41.1` and runs the suite.
- The drift check (`generate.sh`) still compares the generated output byte for
  byte. It now pins the older generator.

A consistency test fails when the stubs' gencode, the declared floor and the
matrix disagree.

---

## 2. Presigned transfers — **Partial (Go), Gap (Python)**

**Today, Go** (`sdk/go/paladin/objects.go`)

- **Streaming**:
  - `Download` already streams and returns `io.ReadCloser` (`:292`).
  - `Upload` reads `io.ReaderAt` (`:60`), so a file streams without being
    loaded into memory.
  - A non-seekable stream (a pipe, an HTTP body) cannot be uploaded.
- **Transport**:
  - The HTTP client is passed per call (`UploadOptions.HTTPClient`, the
    `Download` argument), not declared once.
  - A nil client falls back to `http.DefaultClient` (`:75-80`, `:316`), which
    has no timeout and follows ten redirects.
- **Missing features**: no split-horizon rewrite, no range read, no checksum
  check.
- **Bug**: `Download` returns `ErrNoUploadURL` when the *download* URL is
  missing (`:299`).
- **Errors**: `TransferError{Method, Status, Body}` exists (`:42`). The body is
  capped at `errorBodyLimit`.

**Today, Python** (`sdk/python/src/paladin/workflows.py`)

- `urllib.request.urlopen(req)` has no timeout (`:202`). It cannot be given a
  client, a TLS context, a proxy or a rewrite.
- `download()` returns the whole object as `bytes` (`:337`).
- `upload()` accepts bytes or a seekable file and reads one part at a time
  (`_Body`, `:214`). A single PUT up to the threshold reads `size` bytes into
  memory.
- `TransferError(method, status, body)` exists (`:185`). It mirrors Go.

**Plan (PRs 2a Go, 2b Python)**

- **A `Transfers` value declared once** on the client and used by every
  presigned request:
  - **HTTP client**: an injectable `*http.Client`, or a `pyqwest` transport,
    which is what connect-python already depends on, so no new dependency.
  - **Defaults**: a bounded overall timeout and redirects disabled. A presigned
    URL never redirects legitimately, and following one would send the signed
    headers elsewhere.
  - **`Rewrite(url) → url`**: keeps the signed `Host`. In Go,
    `req.URL.Host = internal` and `req.Host = signed host`. In Python, an
    explicit `Host` header. SigV4 signs `host`, so the signature still holds.
    A test signs a URL for host A, serves it on host B and asserts that
    storage saw `Host: A`.
- **Streaming**:
  - Go: `Upload` also takes an `io.Reader` of known size, sent as one
    streamed PUT. Multipart from a non-seekable stream buffers one part at a
    time.
  - Python: `download()` gains a streaming form, a context-managed file-like
    object that yields chunks, and `upload()` streams from a file or iterator.
    The existing `bytes` form stays as a convenience.
- **Range reads**: `Range: bytes=a-b` on the presigned GET. It is not a signed
  header, so it can be added client-side. A `206` is required; a `200` to a
  range request is an error.
- **Verification**:
  - Content length is checked against `Object.size_bytes`.
  - The checksum is checked against `Object.checksum` when the server sends
    one (`proto/paladin/data/v1/types.proto`, `ChecksumDigest{algorithm,
    value}`). Algorithms: SHA256, CRC32C and MD5.
  - Verification happens on full reads only, since a range cannot be checked
    against a whole-object digest.
  - The digest is computed while streaming. The final read returns a
    `ChecksumMismatch` instead of EOF.
- **Errors**: `TransferError` gains the same fields in both languages (method,
  status, capped body excerpt, URL host without the query) and the same cap.
  The `ErrNoUploadURL` misuse is fixed with `ErrNoDownloadURL`.

---

## 3. TLS and workload identity — **Gap; one part infeasible in Python as stated**

**Today**

- **Go**: `WithHTTPClient` (`client.go:80`) accepts any `connect.HTTPClient`.
  The consumer builds the `tls.Config`, the rotation and the SAN check
  themselves.
- **Python**: no TLS option on `Client`/`connect`. `transport=` reaches the
  generated clients (`connect.py:61`), and the consumer builds a `pyqwest`
  transport. The brief's "httpx event hooks" describes the consumer's code;
  the SDK's HTTP stack is `pyqwest` (Rust `reqwest`), not httpx.

**What `pyqwest` 0.11.0 allows** (inspected):

- **Constructor arguments**: `HTTPTransport(tls_ca_cert, tls_cert, tls_key,
  tls_include_system_certs, proxy, timeout, connect_timeout, read_timeout,
  pool_max_idle_per_host, follow_redirects, max_redirects, enable_otel, …)`.
- **Peer-verification callback**: none.
- **Certificate reload**: none. The certificates are fixed when the transport
  is built.

**Plan (PR 3)**

- **Go**:
  - `WithTLS(TLSConfig{CAFile, CertFile, KeyFile, VerifyPeer func(*x509.Certificate) error})`.
  - The client certificate goes through `tls.Config.GetClientCertificate`,
    which re-reads the files when they change (by mtime, checked at most every
    N seconds), so rotation needs no restart.
  - `VerifyPeer` runs in `VerifyConnection`. A ready-made `SPIFFEID("spiffe://…")`
    asserts the URI SAN.
  - The same `TLSConfig` can be given to `Transfers`.
  - No `go-spiffe` dependency. A URI SAN check is a string compare on
    `cert.URIs`. A Workload API source is out of scope; a consumer with
    `go-spiffe` passes its own `tls.Config`.
- **Python**:
  - `TLS(ca_file, cert_file, key_file)` builds the `pyqwest` transports.
  - **Rotation**: the SDK watches the files' mtime and builds a fresh
    transport on change. Calls already in flight finish on the old one. This
    is the only reload `pyqwest` allows.
  - **Peer verification**: there is no hook, so it is limited to pinning the
    CA bundle. A URI-SAN assertion cannot be done on the `pyqwest` stack. The
    README states this in the parity table. Getting it would mean a different
    HTTP stack under connect-python, which this plan does not propose.

---

## 4. Credentials — **Solved, docs missing**

- **Re-read per call**: `TokenSource.Token` is called on every call (Go
  `auth.go:224`, Python `_TokensSync` in `client.py`). A source reading a
  settings store sees a rotated value on the next call.
- **Retry once on `Unauthenticated`, in both languages**:
  - Go: `auth.go:233-249`.
  - Python: `_TokensSync`/`_TokensAsync`, tested in
    `sdk/python/tests/test_auth.py:204` (`test_a_refused_token_is_retried_once`),
    including "retried once, not more".
- **Gap**: neither README says which source to use when: `StaticToken` for a
  PAT, `Session` for a user, a custom source for a rotating store. A store-backed
  source that wants the retry must implement `Invalidate` (Go) or `invalidate`
  (Python). That is undocumented.

**Plan**: documentation only, folded into PR 13 (cookbook: rotating token from a
settings store), plus one test per language with a source whose value changes
between two calls.

---

## 5. Idempotency and retries — **Partial; the release-notes complaint is half right**

**Today**

- **Keys**: 0.12.0 stamps a random key on every unary call whose
  `idempotency_level` is unknown (Go `interceptors.go:54-65`, Python
  `client.py` `_key_for`). A caller can **override** it: Go
  `WithIdempotencyKey(ctx, key)`, Python `with idempotency_key(key):`. There is
  **no opt-out**.
- **Was 0.12.0 marked breaking?**
  - `docs/upgrading.md:45-60` documents it as one of three breaking changes,
    with migration steps.
  - The commits carried no `!`, so the `sdk/go/v0.12.0` GitHub release lists it
    under *Features*, with no *Behaviour changes* section and no link to the
    guide.
  - The upgrade note's sentence "a repeat create … now gets the first response
    back" is true only for a key held across calls (a context or block key),
    not for the per-call default. It needs correcting.
- **The brief's example** ("minting a fresh presigned URL must not replay") is
  already safe: each call mints its own key, and only a *retry of that same
  call* replays, returning the same URL. The opt-out is still worth having for
  a call that must never be retried.
- **`Retry-After`**: seconds only (Go `interceptors.go:139-149`, Python
  `client.py:239`). An HTTP-date is ignored.
- **Classification**: fixed to `Unavailable` and `ResourceExhausted` (Go
  `interceptors.go:161-175`, Python `client.py:55`).
- **Deadlines**:
  - Go never sleeps past the context deadline (`interceptors.go:110`).
  - Python does the same against the call's timeout (`client.py:259`; connect-python's
    `timeout_ms()` returns the *remaining* time).
  - **Solved** in both.

**Plan (PR 5)**

- **Opt-out**: Go `WithoutIdempotencyKey(ctx)`, Python
  `with no_idempotency_key():`. The call goes out without a key and is
  therefore never retried. Tests in both languages.
- **`Retry-After` as an HTTP-date**: parsed with the standard library
  (`http.ParseTime`; `email.utils.parsedate_to_datetime`), not by hand.
  Table tests: seconds, a date, a past date, garbage.
- **Configurable classification**: Go `WithRetryClassifier(func(connect.AnyRequest, error) bool)`
  and Python `Retry(retryable=…)`. The safety rule (only idempotent or keyed
  calls) stays outside the classifier and cannot be overridden.
- **Release notes**: PR 10 adds a *Behaviour changes* section.

---

## 6. Errors — **Gap; half of it is a server change**

**Today**

- No typed hierarchy. Callers get `connect.Error` or `ConnectError` and match
  codes. The only typed errors are `OperationError`/`OperationFailed` (Go
  `wait.go:29`, Python `workflows.py:98`) and `TransferError`.
- **The server attaches no error details**: no `AddDetail`/`NewErrorDetail`
  anywhere in `backend/internal`. "Unwrap the server's error details" has
  nothing to unwrap today.
- BACKLOG already holds the server half: *Errors carry no machine-readable
  reason* (`apiutil.MapError` attaches `google.rpc.ErrorInfo`).

**Plan**

- **PR 6a (server)**: the BACKLOG entry. `ErrorInfo{reason, domain}` on every
  mapped sentinel, plus `RetryInfo` on `ResourceExhausted` beside the
  `Retry-After` header.
- **PR 6b (SDKs)**:
  - `NotFound`, `AlreadyExists`, `PermissionDenied`, `FailedPrecondition`,
    `ResourceExhausted{RetryAfter}` and `ContractSkew{Procedure, SDKVersion,
    ServerVersion}`, built from the Connect error.
  - Go uses `errors.As` targets that wrap the original. Python uses exception
    subclasses of `ConnectError`, so existing `except ConnectError` keeps
    working.
  - `Reason(err)` and the decoded details are exposed.
  - **`ContractSkew`**: `Unimplemented` on a procedure the SDK knows. The
    server version is read once, lazily, from `HealthService.GetVersion`.

---

## 7. Domain helpers — **Partial; two premises wrong as stated**

- **Resource names**: no builders or parsers today. BACKLOG holds *Resource
  names are strings each side assembles by hand*, with the plan to annotate
  `google.api.resource` and generate helpers.
- **Corrected while building it: the brief was right.** The proto comments
  say `tenant_id_or_slug`, but the server's parsers
  (`connectshim/resolve/resolver.go`, `connectshim/data/conv.go`) require
  the tenant's id, a UUID, in every name under a tenant; only
  `tenants/{tenant}` takes a slug. The comments are fixed, and the
  helpers enforce the UUID. What follows was the original, wrong, reading:
  **"the tenant segment is a UUID" contradicts the contract**: the patterns are `tenants/{tenant_id_or_slug}/…`
  (`proto/paladin/data/v1/object_service.proto:135`, `types.proto:14`). A
  client-side UUID check would reject valid slug names. The builders should
  validate the *shape* (segment count, no empty or `/`-bearing segment), not
  the tenant's form.
- **`paladin://` URIs**: no data URI scheme exists. `paladin://` is already
  taken by the MCP bridge's admin resources (`paladin://tenants`,
  `paladin://backends`, `paladin://buckets`, `backend/internal/mcp/bridge.go:1414-1434`).
  It is also used as capability resource prefixes (`paladin://docs/`). The
  consumer's `paladin://<collection>/<key>` collides with that namespace, and
  it has no tenant, so it is ambiguous across tenants. The format is settled
  under [Decisions](#decisions).
- **Bootstrap**: the server already has it:
  `StorageBootstrapService.EnsureTenantStorage{backend_id, bucket,
  collections}` reports `collections_created`/`collections_existing`. It is
  idempotent by design. The SDK item is a documented one-call wrapper and a
  cookbook entry, not new logic.

**Plan (PR 7)**: the annotation and generator work from the BACKLOG entry. Name
types per resource, with `Parse`/`String` and a round-trip test per pattern. A
`paladin.EnsureStorage` thin wrapper. The URI parser/formatter only after the
scheme decision.

---

## 8. Observability — **Gap**

**Today**

- No OpenTelemetry in `sdk/go/go.mod`. Python's `pyqwest` transport has
  `enable_otel=True` **by default**. The Python SDK therefore already emits
  HTTP-level spans when an OTel SDK is configured in-process. That is
  undocumented, and it contradicts "off by default".
- `User-Agent`: the SDK sets `paladin-sdk-go/<v>` / `paladin-sdk-python/<v>`
  (`useragent.go:17`, `client.py:90`). A caller can only *replace* it
  (`WithHeader`, `headers=`), not append a suffix.

**Plan (PR 8)**

- **Go**: an optional `paladin/otel` sub-package, a separate module so the core
  carries no OTel dependency. It provides a Connect interceptor (via
  `connectrpc.com/otelconnect`) and a `Transfers` hook that wraps the
  presigned client in `otelhttp`.
- **Python**: an `otel` extra using `opentelemetry-api` only. Spans are wrapped
  around RPCs and transfers.
- **`pyqwest`'s own OTel**: off by default in transports the SDK builds, on
  when the caller opts in.
- **Metrics and logging**: a small hooks interface (`OnRetry`, `OnTransfer`
  with bytes and duration, `OnCall` latency) in both languages, and a
  structured-logging adapter (`slog` / `logging`).
- **`User-Agent`**: `WithUserAgentSuffix("gateway/1.4")` and
  `user_agent_suffix=`.

---

## 9. Concurrency — **Partial**

**Today**

- **Python async parity**: async exists for `apages` and `await_operation`
  (`workflows.py:66`, `:154`) and for the generated clients. **`upload` and
  `download` are sync only.**
- **Shared clients**: Go clients are safe for concurrent use, since Connect
  clients and `http.Client` are. That is not documented. Python sync and async
  clients are not documented either way.
- **Multipart**: concurrency is bounded (`DefaultPartConcurrency = 3`, both
  languages).
- **Bulk transfers**: no bounded helper for many objects.

**Plan (PR 9)**

- Python `aupload`/`adownload` (streaming) on the async clients.
- `Transfers` exposes pool sizing: `MaxConnsPerHost` in Go,
  `pool_max_idle_per_host` in Python.
- A bulk helper, `DownloadMany`/`download_many(names, concurrency=)`, that
  yields results in completion order with per-item errors.
- A *Concurrency* section in each README.

---

## 10. Semantic versioning — **Partial**

**Today**

- **Version rules**: pre-1.0, a breaking change is a minor
  (`release.config.cjs`; `docs/releasing.md`). The migration guide exists
  (`docs/upgrading.md`).
- **Release notes** (`scripts/stream-release-notes.sh`): grouped by type, with
  no *Breaking* or *Behaviour changes* section and no link to the guide.
- **Compatibility**: no statement of which server or API versions an SDK
  release supports.

**Plan (PR 10)**

- The notes script adds *Behaviour changes*, from commits marked `!` or with
  `BREAKING CHANGE:`, and a link to the matching `docs/upgrading.md` anchor.
  Test cases are added to `stream-release-notes.test.sh`.
- `docs/releasing.md` states the window: an SDK release supports servers from
  the API baseline (`api/vX.Y.Z`) it was cut against onward. Older servers get
  `ContractSkew` (item 6).
- **Amend the published `sdk/go/v0.12.0` notes** with a *Behaviour changes*
  section that links `docs/upgrading.md#v0120`. This is an edit to a published
  release, so it needs a yes.

---

## 11. Cross-language parity, tested — **Gap; infrastructure exists**

- **Live checks today**: CI already starts the compose stack for e2e
  (`ci.yaml`, `e2e-images` and the end-to-end jobs). `verify:live`
  (`scripts/live-smoke.sh`) runs against a deployed cluster.
- **SDK tests today**: each SDK is tested against its own fakes. BACKLOG holds
  *SDK users have no fake server, and the SDKs no shared scenarios*.

**Plan (PR 11)**

- **Scenarios**: one scenario list in YAML: retries, idempotency replay, token
  refresh, upload/download/range/checksum, TLS, the error mapping.
- **Runners**: a Go runner and a Python runner both execute the scenarios
  against the compose stack in a CI job beside e2e.
- **TLS scenarios**: these need a TLS-terminating front in the compose stack.
  If that is too heavy for CI, they run against the in-memory fake (item 12)
  and are marked so.
- **Parity table**: one in each README, generated from the same scenario list.

---

## 12. Test fakes for consumers — **Gap (in BACKLOG)**

**Plan (PR 12)**

- Go `paladintest` and Python `paladin.testing`: the generated handlers served
  in memory, `Unimplemented` by default.
- An in-memory object store behind presigned URLs, so `Upload`/`Download`
  work end to end.
- The SDKs' own tests move onto it, which closes the "two fakes could
  disagree" half of the entry.

---

## 13. Cookbook — **Gap**

**Plan (PR 13)**

- Runnable examples per language:
  - Go: `example_*_test.go`, compiled and run by `go test`.
  - Python: `examples/` run by pytest against the fake.
- The seven recipes in the brief.
- "Migrating from a hand-written Connect-JSON client" maps raw paths and JSON
  to the generated clients.

---

## Order of pull requests

| PR | Item | Scope |
| --- | --- | --- |
| 1 | 1 | protobuf 6/7 gencode, ranges, CI matrix |
| 2a / 2b | 2 | `Transfers`: transport, split-horizon, streaming, range, verification (Go / Python) |
| 3 | 3 | TLS options, rotation, SPIFFE URI SAN (Go); TLS + rotation (Python) |
| 5 | 5 | idempotency opt-out, `Retry-After` dates, retry classifier |
| 6a / 6b | 6 | `ErrorInfo` on the server / typed errors in both SDKs |
| 7 | 7 | resource annotations and generated name helpers; bootstrap wrapper |
| 8 | 8 | OTel (optional module / extra), hooks, UA suffix |
| 9 | 9 | async transfers, pooling, bulk helper |
| 10 | 10 | behaviour-changes section in notes, support window |
| 11 | 11 | shared conformance scenarios against compose |
| 12 | 12 | `paladintest` / `paladin.testing` |
| 13 | 13, 4 | cookbook, credentials guidance |

Item 4 needs no code. Each PR updates the BACKLOG entries it closes, in the same
commit.

## Decisions

1. **No PyPI.** The SDK installs from the repository, pinned to an
   `sdk/go/vX.Y.Z` tag.
2. **The generator is pinned old on purpose** (item 1): `grpcio-tools` 1.81.1
   is an explicit exception to the latest-stable rule.
3. **Object URIs** (item 7) use the resource name as the path:
   `paladin://tenants/{t}/collections/{c}/objects/{key}`. It extends the MCP
   bridge's `paladin://tenants` rather than colliding with it, and it names
   the tenant.
4. **The tenant segment** (item 7) — superseded: the server requires the
   id under a tenant (see item 7); only a tenant's own name takes a slug.
   Originally: accepts an id or a slug, as the contract
   does.
5. **Python peer verification** (item 3): CA pinning only, documented as an
   intentional difference from Go.
6. **Amending the `sdk/go/v0.12.0` release notes** (item 10) needs an explicit
   yes, since it edits a published release.

## Outcome

Built as planned, in these pull requests; the decisions are in
[ADR-0020](../../docs/adr/0020-sdk-integration-grade.md).

| Item | Pull request |
| --- | --- |
| 1. protobuf 6 and 7 | #179 |
| 2. Presigned transfers | #180 (Go), #181 (Python) |
| 3. TLS and workload identity | #182 |
| 4. Credentials | already solved; the recipe is in the cookbook (#192) |
| 5. Idempotency and retries | #183 |
| 6. Errors | #185 |
| 7. Names and URIs | #186; the bootstrap is the server's `EnsureTenantStorage` |
| 8. Observability | #187 |
| 9. Concurrency | #190 |
| 10. Behaviour changes in release notes | #188, #193 |
| 11. Shared scenarios against the stack | #191 |
| 12. Fakes for consumers | #189 |
| 13. Cookbook | #192 |

Left in BACKLOG.md under *SDK*: the server's own name parsers on the shared
table, the webhook signature and its verifiers, and RPC spans from
connect-python's interceptor once it supports the version in use.
