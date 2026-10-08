# Paladin Go SDK

```bash
go get github.com/oleg-tkachuk/paladin/sdk/go
```

Two parts:

- `gen/` — protobuf types and Connect clients generated from
  [`proto/`](../../proto). The service clients are these, unwrapped.
- `paladin/` — three layers over them. What every call needs: credentials and
  sessions, idempotency keys, retries. `Connect`, a client for every service
  of each plane. And what takes more than one call: paging, waiting on an
  operation, update masks, uploads and downloads.

Only this module's own dependencies come with it — protobuf, Connect and the
annotation packages the stubs import. Nothing of the server.

## Quick start

```go
import (
	"context"

	"connectrpc.com/connect"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

session, err := paladin.NewSession(ctx, iamURL, subject, password)
if err != nil {
	return err
}
p, err := paladin.Connect(paladin.Endpoints{Data: dataURL, Admin: adminURL, IAM: iamURL},
	paladin.WithTokens(session), paladin.WithRetries(3, 0))
if err != nil {
	return err
}
// Every service of every plane; each call carries the token for its plane
// and, when it has side effects, an idempotency key.
tenant, err := p.Admin.Tenant.CreateTenant(ctx, connect.NewRequest(&adminv1.CreateTenantRequest{ /* … */ }))
```

With an API token instead of a session, pass
`paladin.WithTokens(paladin.StaticToken(apiToken))`.

## Planes

Paladin serves three planes on separate listeners, and a token is issued for
one of them. `Connect` builds a client for every service of each plane you
give a URL — `p.Data`, `p.Admin`, `p.IAM`, nil for a plane left out — and
sends each the token for its own audience. The fields are the services'
names without `Service`: `p.Data.Object`, `p.Admin.EventSubscription`,
`p.IAM.Auth`. They are generated from the contract (`go generate
./paladin`), and a test fails when they fall behind it.

| Plane | Package | Services |
| --- | --- | --- |
| Admin | `gen/paladin/admin/v1/paladinadminv1connect` | tenants, buckets, backends, policies, quotas, tokens, audit |
| Data | `gen/paladin/data/v1/paladindatav1connect` | objects, multipart uploads, tags, batches, presigning |
| IAM | `gen/paladin/iam/v1/paladiniamv1connect` | login, tokens, users, health |

For one plane alone, `New` gives the pieces the generated `New…ServiceClient`
constructors take:

```go
c, err := paladin.New(adminURL, paladin.WithTokenSource(session, paladin.AudienceAdmin))
tenants := paladinadminv1connect.NewTenantServiceClient(c.HTTPClient(), c.BaseURL(), c.ClientOptions()...)
```

## `paladin` package

### Constructing a client

| Function | Does |
| --- | --- |
| `New(baseURL string, opts ...Option) (*Client, error)` | A client for the plane at `baseURL`. Returns `ErrEmptyBaseURL`, `ErrInvalidBaseURL` (not an absolute `http`/`https` URL) or `ErrInvalidRetries`. A trailing `/` is dropped. |
| `(*Client) HTTPClient() connect.HTTPClient` | First argument of every generated `New…ServiceClient`. |
| `(*Client) BaseURL() string` | Second argument. |
| `(*Client) ClientOptions() []connect.ClientOption` | The rest. A copy; changing it does not change the client. |

### Options

| Option | Does |
| --- | --- |
| `WithBearerToken(token)` | Sends `Authorization: Bearer <token>`. An API token (`paladin_pat_…`) and an OIDC JWT are both accepted there. |
| `WithAPIToken(token)` | Sends an API token in `X-Paladin-API-Token`, for a proxy that strips `Authorization`. |
| `WithCapability(token)` | Sends a capability token in `X-Paladin-Capability`: the JWT or its Biscuit form, narrowed offline with `capability.Attenuate` or not. Can be combined with a bearer token. |
| `WithCapabilitySource(source)` | Sends the capability `source(ctx)` returns for each call, for a client acting for many callers; an empty one leaves `WithCapability`'s. DPoP proofs sign over it. |
| `WithDPoP(key)` | Proves possession of `key` — any `crypto.Signer` with an Ed25519 or P-256 public key, a KMS-held one included — on every call that sends a capability: each request, each retry included, carries a fresh RFC 9449 proof in `DPoP`. A capability issued with `confirmation_jkt = DPoPThumbprint(key.Public())` is refused without one. Proofs are signed for `POST`, so do not combine it with `connect.WithHTTPGet`. |
| `WithHeader(name, value)` | Sends a header on every call, replacing what the SDK would send there — `User-Agent` included, which is `paladin-sdk-go/<module version>` by default. |
| `WithRetries(attempts, baseDelay)` | Retries a unary call on `Unavailable` or `ResourceExhausted`, up to `attempts` calls in total. The wait is drawn at random up to a ceiling that doubles from `baseDelay` (zero means `DefaultRetryBaseDelay`, 100ms) to `DefaultRetryMaxDelay` (5s), and is never shorter than the server's `Retry-After`, given in seconds or as an HTTP date. A retry that could not start before the context's deadline is not made, and the server's error is returned. Only calls safe to repeat are retried: RPCs the contract declares side-effect free or idempotent, and calls that carry an idempotency key — which every other call does, see below. Streams are never retried. |
| `WithHTTPClient(c)` | Replaces `http.DefaultClient` — for timeouts, proxies, custom TLS. |
| `WithClientOptions(opts...)` | Passes Connect options through, e.g. `connect.WithGRPC()` or `connect.WithSendGzip()`. |

### Idempotency

A unary call the contract does not declare side-effect free or idempotent
sends an `Idempotency-Key` of its own: the context's when it has one, else the
request's `idempotency_key` field when set, else a fresh random key. Every
retry of that call sends the same key. The server requires one on `Create*`
and `Issue*` calls and answers a repeated key with the first response.

| Function | Does |
| --- | --- |
| `WithIdempotencyKey(ctx, key) context.Context` | Every call made with the returned context that the contract does not declare side-effect free or idempotent sends `Idempotency-Key: <key>`. The server replays the first response to the same request with a key it has seen, so repeating a mutating call with the same key is safe; the same key with a different request to that method is `InvalidArgument`. `Upload`, `Download` and their `Many` forms keep the key for the calls that create or complete an object — per input, as `key/<index>`, in `UploadMany` — and give every other call its own. A response that carries a credential (a minted API or capability token, a generated password) is not replayed: the repeat returns `AlreadyExists`, because the credential is delivered once and not stored. Reuse the key only for the same logical operation. |
| `IdempotencyKey(ctx) (string, bool)` | The key attached to `ctx`; an empty key counts as none. |
| `WithoutIdempotencyKey(ctx) context.Context` | Every call made with the returned context goes out with no key — not the default one, not the request's field — and so is never retried, unless the contract declares it side-effect free or idempotent. For an operation that must run again when repeated rather than be answered with the first response. The server refuses `Create*` and `Issue*` without a key. The last `WithIdempotencyKey` or `WithoutIdempotencyKey` on a context wins. |
| `WithRetryable(func(error) bool) Option` | Replaces `DefaultRetryable` (`Unavailable`, `ResourceExhausted`) as the test of which failures `WithRetries` retries. It cannot make an unsafe call retried: the rule above still applies. |

### Tokens

| Name | Does |
| --- | --- |
| `NewSession(ctx, iamURL, subject, password, opts...) (*Session, error)` | Signs in at the IAM plane and keeps the refresh token. `Token(ctx, audience)` returns that plane's access token: the IAM one by refreshing, the others by `ExchangeAudience`. Each is cached until `TokenRefreshMargin` (30s) before it expires. When the refresh token itself is refused, the session signs in again. Safe for concurrent use; concurrent callers wait for one mint, and a cached token is served while another audience's is minted. A token that cannot be had because IAM is down fails the call as `Unavailable`, not `Unauthenticated`. |
| `SessionFromRefreshToken(iamURL, refreshToken, opts...)` | Resumes from a stored refresh token; cannot sign in again when it expires. `(*Session).RefreshToken()` is the current one to store — refreshing rotates it. |
| `WithSessionClock(now)` | Replaces `time.Now`, for tests. |
| `WithSessionClientOptions(opts...)` | Configures the client the session reaches IAM with, as `New`'s options do any other: `WithTLS` for an IAM behind mTLS, `WithRetries`, `WithHTTPClient`, `WithHooks`. |
| `StaticToken(token)` | The same token for every plane: an API token, or a JWT from elsewhere. |
| A `TokenSource` of your own | `Token(ctx, audience)` is asked on every call, with that call's context. An empty token and no error sends the call without `Authorization`, for a caller authenticated by a capability alone. |
| `WithTokens(ts)` | With `Connect`: each plane gets `ts`'s token for its own audience. `New` refuses it with `ErrNoAudience`. |
| `WithTokenSource(ts, audience)` | One plane's client: the token for `audience`. A call refused as `Unauthenticated` is made once more with a fresh token — the server authenticates before anything else, so the first attempt changed nothing. |
| `AudienceData`, `AudienceAdmin`, `AudienceIAM` | The audience names; the server imports them from here. |
| `NewCapabilityCache(mint, opts...)` | One capability per key — a tenant, a prefix — minted by `mint` and kept until `DefaultCapabilityRefreshMargin` (30s, `WithCapabilityRefreshMargin`) before it expires, for a service that acts for many callers; feed `Token(ctx, key)` to `WithCapabilitySource`. Concurrent callers for one key wait for one mint; a failed mint is not cached. `Invalidate(key)` drops one the server refused. |

### Narrowing a capability offline

`CapabilityService.Issue` and `Delegate` return `biscuit` beside `token`: the
same capability as a Biscuit v3 token, which whoever holds it can narrow with
no key and no call to the server. This SDK has no helper of its own: import
the capability module, whose `Attenuate` is the code the server's verifier is
tested against, so there is no second implementation of the vocabulary to
drift from it.

```bash
go get github.com/oleg-tkachuk/paladin/capability
```

```go
import "github.com/oleg-tkachuk/paladin/capability"

narrowed, err := capability.Attenuate(issued.GetBiscuit(), capability.Attenuation{
    Ops:              []capability.Op{capability.OpGet},
    ResourcePrefixes: []string{"corpus/public/"},
    Planes:           []string{capability.AudiencePlaneData},
    ExpiresAt:        time.Now().Add(10 * time.Minute),
})
client, err := paladin.New(dataURL, paladin.WithCapability(narrowed))
```

A field left empty leaves that dimension as it is; a set one replaces it and
must be within what the token allows, or the server refuses the whole token.
Setting `ResourcePrefixes` or `ResourceURIs` replaces both. `ConfirmationJKT`
(`DPoPThumbprint(key.Public())`) binds the token to a key, and only an unbound
token can be bound. `MaxRequests` and `MaxBudgetMicros` give the copy limits of
its own, counted apart from other copies and within every limit already in
force; spending past them answers `ResourceExhausted`.
`CapabilityService.GetBiscuitUsage` takes a copy and reports each limit in
force on it with what has been counted against it. The facts it writes are listed in
[`sdk/testdata/biscuit_vocabulary.json`](../testdata/biscuit_vocabulary.json),
which the Python SDK's `attenuate` writes too.

`CapabilityService.RevokeBiscuit` revokes one copy: the token sent and every
copy attenuated from it. The copy it came from, its siblings and the
capability's JWT keep working; `Revoke` stops them all.

### Workflows

| Name | Does |
| --- | --- |
| `Pages(ctx, call, req, items) iter.Seq2[Item, error]` | Calls a List RPC page by page, following `next_page_token`, and yields every item; `items` picks them out of a response, e.g. `(*datav1.ListObjectsResponse).GetObjects`. The first error is yielded and ends it; leaving the loop makes no further calls. `ErrNotPaged` for an RPC without `page`. |
| `Wait(ctx, get) (Op, error)` | Polls `get` until the operation is done, from `DefaultPollInterval` (500ms) doubling to `DefaultMaxPollInterval` (10s). An operation that failed comes back with an `*OperationError` carrying its status; `Code()` is the Connect code. `ctx` bounds the wait. Works for both planes' `Operation`. |
| `Mask[M](paths...) (*fieldmaskpb.FieldMask, error)` | An update mask for `M` from proto field names, nested ones with `.`, each checked against `M`'s descriptor: `ErrUnknownMaskPath` for one it lacks. |
| `Upload(ctx, p.Data, UploadInput, UploadOptions) (*datav1.Object, error)` | Uploads exactly `Size` bytes (0 is an empty object) from exactly one of `Body` (an `io.ReaderAt`: `*os.File`, `*bytes.Reader`) and `Stream` (an `io.Reader` read once: a pipe, a response body) and completes the object. Every upload URL is signed for its body's size and SHA-256, so each body is hashed before it is presigned: `Body` is read twice and never held whole; `Stream` is held in memory while it is hashed and sent — the whole object below the threshold, the parts in flight above it. Up to `MultipartThreshold` (default `DefaultMultipartThreshold`, 8 MiB) one presigned PUT, whose checksum is recorded on the object; above it multipart, `PartConcurrency` parts at a time (default 3), aborted if any part fails. `ErrUploadBody` for neither or both. `Checksum(algo, r)` computes the value for a caller that presigns itself. Each presigned request is retried — on a busy store, a transport failure or an expired URL, up to `DefaultTransferAttempts` (`WithTransferAttempts`) — through a freshly presigned URL, and a URL within `PresignExpirySkew` of its expiry is presigned again before it is sent; a single PUT whose retry meets an object already stored (412, the URL's If-None-Match) completes. `UploadOptions.OnSession` hands over the open multipart session and leaves a failed upload open; `UploadOptions.Resume` continues it, sending only the parts storage does not hold. |
| `Download(ctx, p.Data, name, DownloadOptions) (*ObjectReader, error)` | Streams the object's content; close the reader. `DownloadOptions{Offset, Length}` reads a byte range (`ErrRangeIgnored` when storage answers with the whole object). A whole read is verified: the last `Read` returns an `*IntegrityError` instead of `io.EOF` when the size or the recorded checksum (SHA-256, CRC32C, MD5) does not match — for a multipart object, the composite of its parts' digests, recomputed over the part size the server records beside it (`ChecksumDigest.PartSizeBytes`); one completed before the server recorded it is checked for its size alone. `ObjectReader` carries the `Object`, `ContentType` and `ContentLength`. The request is retried as an upload is, each attempt through a URL bound to the object's ETag as it was first read: `ErrObjectChanged` when the object was replaced in between. |
| `Ensure(ctx, get, create) (T, created, error)` | Makes a resource exist, for provisioning run at every boot: `get`; on `NotFound`, `create`; on `AlreadyExists` — another process won — `get` again. Give `create` a stable idempotency key. |
| `PresignExpiry(url) (time.Time, bool)` | When a presigned URL stops working, from its `expires_at_rfc3339`; false when the server sent none. An upper bound: the signer may clamp a TTL further. |
| `BeginMultipart(ctx, p.Data, MultipartInput) (UploadSession, error)`, `PresignPart(ctx, p.Data, session, number, checksum)`, `CompleteMultipart(ctx, p.Data, session, parts)`, `AbortMultipart(ctx, p.Data, session)` | The control half of a multipart upload whose bytes another party sends — a browser: open it split as the server recommends (`ErrNoPartSplit`, and the upload aborted, when it names no split), sign each part for the base64 SHA-256 its sender computed (again for one whose URL expired), assemble the parts from the ETags the sender read — quoted, as a header carries them — and return the object, or drop the parts (an upload already gone is not an error). `Upload` does all four when it holds the bytes. |

### Transfers

`Upload` and `Download` move bytes through presigned URLs, straight to the
storage backend. Those requests are not Paladin calls: the client's
credentials, retries and interceptors are not on them. They go through the
client's `Transfer`, declared once:

```go
transfer, err := paladin.NewTransfer(
	// URLs signed for the public storage host, sent to its in-cluster address
	// with the signed Host header kept.
	paladin.WithSplitHorizon("https://s3.example.com", "http://seaweedfs-s3.storage.svc:8333"),
)
p, err := paladin.Connect(endpoints, paladin.WithTokens(session), paladin.WithTransfer(transfer))
```

| Name | Does |
| --- | --- |
| `NewTransfer(opts...) (*Transfer, error)` | With no options: a transport bounded at each stage that can hang — dial, TLS handshake, response headers (`DefaultTransfer…` constants) — but not over a whole transfer, which the caller's context bounds; `DefaultTransferMaxIdleConnsPerHost` (32) connections kept for reuse; redirects refused. Safe for concurrent use and meant to be shared. |
| `WithTransfer(t) Option` | Every `Upload` and `Download` through the client uses `t`. A client without one shares a default. |
| `WithSplitHorizon(signedOrigin, internalOrigin)` | A URL signed for `signedOrigin` is sent to `internalOrigin`, keeping `Host: <signed host>`: the signature covers the header, not the address. Other origins go as signed. `ErrInvalidOrigin` for anything but `scheme://host[:port]`. |
| `WithTransferRewrite(func(*url.URL) *url.URL)` | The general form: any mapping, the signed Host still kept. |
| `WithTransferHTTPClient(c)` | Sends through `c` — a proxy, a TLS configuration, instrumentation. Redirects are still refused. |
| `(*Transfer).Put(ctx, signed, body, size)` | Sends one presigned PUT you drive yourself — a part of a multipart upload — and returns the ETag. |
| `required_headers` | Every header a presigned URL lists is sent as given, except `Content-Length` and `Host`: the signature covers them too, but they are the body's length and the URL's host, which `net/http` writes itself. A request of your own sends a body of exactly the listed length; a browser refuses to set either and does not need to. |
| `*TransferError` | A request storage refused, or answered with a redirect: `Method`, `Host` (the URL's query is the signature and is not kept), `Status`, and the first 512 bytes of the `Body`. |

### TLS and workload identity

`WithTLS` makes the client's connections to Paladin, and `WithTransferTLS` a
`Transfer`'s connections to storage, with a CA bundle, a client certificate
and an expected server identity. The files are read when the client is built
and again whenever they change on disk, so certificates a workload-identity
agent rotates are picked up without a restart.

```go
id := paladin.TLS{
	CAFile:   "/var/run/secrets/spiffe/bundle.pem",
	CertFile: "/var/run/secrets/spiffe/svid.pem",
	KeyFile:  "/var/run/secrets/spiffe/svid-key.pem",
	ServerID: "spiffe://cluster.local/ns/paladin/sa/paladin-core",
}
p, err := paladin.Connect(endpoints, paladin.WithTokens(session), paladin.WithTLS(id))
```

| `TLS` field | Does |
| --- | --- |
| `CAFile` | PEM bundle the server's chain must reach; empty trusts the system roots. `ErrNoCA` when it holds no certificate. |
| `CertFile`, `KeyFile` | The client certificate for mutual TLS; both or neither (`ErrTLSKeyPair`). |
| `ServerID` | The SPIFFE ID the server must present. Its certificate is verified as an X.509-SVID against `CAFile` as that trust domain's bundle, by the SPIFFE project's `go-spiffe`, instead of against the host name, which an SVID does not carry. `ErrServerID` on a mismatch or a malformed ID; `ErrServerIDNeedsCA` without `CAFile`. |
| `VerifyPeer` | Runs on the server's leaf certificate after the built-in checks; its error refuses the connection. |
| `ReloadInterval` | How often the files are checked for a change (`DefaultTLSReloadInterval`, 30s). A rotation caught half-written — a new certificate beside the old key — keeps the last good pair until the next check. |
| `MinVersion` | The lowest TLS version offered, a `crypto/tls` constant; zero is `DefaultTLSMinVersion`, TLS 1.2. Lower, or unknown, is `ErrTLSMinVersion`. |

After a rotation every new request goes out on a connection made with the
new files, over HTTP/2 as well as HTTP/1.1; a request in flight finishes on
the connection it started on, and the old connections close once nothing
uses them. An RPC through `WithTLS` has no
response-header timeout — its context bounds it, as without TLS — while a
transfer keeps `DefaultTransferResponseHeaderTimeout`.

To wrap the TLS transport — a circuit breaker, metrics, a peer-identity
recorder — build the client from `(TLS).RoundTripper()`. It returns the
`*RotatingTransport` `WithTLS` and `WithTransferTLS` use, rotation included,
and `WithTLS` with `WithHTTPClient` is `ErrTLSAndHTTP` for that reason:

```go
rt, err := id.RoundTripper()
rt.ResponseHeaderTimeout = 0 // an RPC client: the call's context bounds it
p, err := paladin.Connect(endpoints,
	paladin.WithHTTPClient(&http.Client{Transport: breaker.Wrap(rt)}))
storage, err := id.RoundTripper() // its own, keeping a transfer's bounds
transfer, err := paladin.NewTransfer(paladin.WithTransferHTTPClient(
	&http.Client{Transport: metrics.Wrap(storage)}))
```

| Name | Does |
| --- | --- |
| `(TLS).RoundTripper()` | A `*RotatingTransport` with the files: each request goes on an `http.Transport` for the files as they are now, cloned from the embedded one, whose fields — bounded like a transfer's — are yours to change before the first request. |
| `RotatingTransport` | `RoundTrip`, and `CloseIdleConnections` over every generation, as `http.Client` expects. A replaced generation takes no request, and is closed once its last response is read. |
| `(TLS).Transport()` | A bare `*http.Transport` with the same files. A rotation reaches its new connections only, the old ones staying open: prefer `RoundTripper`. |

These connections are made directly: a proxy from the environment would
make the TLS connection itself, without the files.

### Errors

Every failed call through a client from `New` or `Connect` returns an
`*Error` that wraps the `*connect.Error` — `connect.CodeOf` and
`errors.As(err, &connectErr)` work as before — and matches the kind of
failure with `errors.Is`. Match on these, not on codes or messages:

| Kind | Code | Means |
| --- | --- | --- |
| `ErrInvalidArgument` | `InvalidArgument` | The request fails the same way however often it is sent; `Reason` says which rule it broke. |
| `ErrNotFound` | `NotFound` | |
| `ErrAlreadyExists` | `AlreadyExists` | |
| `ErrPermissionDenied` | `PermissionDenied` | |
| `ErrFailedPrecondition` | `FailedPrecondition` | |
| `ErrVersionConflict` | `Aborted` | The resource changed since it was read: read it again and retry the change. |
| `ErrResourceExhausted` | `ResourceExhausted` | `RetryAfter` is how long the server asked to wait. |
| `ErrUnauthenticated` | `Unauthenticated` | |
| `ErrContractSkew` | `Unimplemented` | The server does not implement the call: it is older than the SDK. The message names the procedure, the server's release (`HeaderServerVersion`) and the SDK's. |

`*Error` also carries the `Procedure`, the server's `Reason`
(`commonv1.ErrorReason`, from the `google.rpc.ErrorInfo` in domain
`ErrorDomain`), `ServerVersion`, `SDKVersion`, and the decoded `Details`.
`Reason(err)` returns the reason of any error, `ERROR_REASON_UNSPECIFIED`
when there is none — or one newer than this SDK, which a caller treats as
the kind alone.

```go
_, err := p.Admin.Collection.DeleteCollection(ctx, req)
switch {
case paladin.Reason(err) == commonv1.ErrorReason_ERROR_REASON_COLLECTION_NOT_EMPTY:
	// empty it first
case errors.Is(err, paladin.ErrNotFound):
	// already gone
}
```

### Observability

Nothing is observed unless asked, and the SDK depends on no OpenTelemetry
package.

| Name | Does |
| --- | --- |
| `WithUserAgentSuffix("gateway/1.4")` | Appends the application to the SDK's `User-Agent`, so the server's logs name it. |
| `WithHooks(Hooks{OnRetry: …})` | `OnRetry(RetryEvent)` before each retry: `Procedure`, the failed `Attempt`, the `Wait`, the `Err`. For latency, retries and errors per call as metrics of your own. |
| `WithTransferHooks(Hooks{OnTransfer: …})` | `OnTransfer(TransferEvent)` as each presigned request ends: `Method`, `Host`, `Bytes` moved, `Duration`, `Err` — a refused request, or a download that failed verification. |
| `WithLogger(*slog.Logger)`, `WithTransferLogger(*slog.Logger)` | Retries and transfers as structured records at debug level, failed transfers at warn. |

**OpenTelemetry** goes through the extension points there are, with
connect's and OpenTelemetry's own instrumentation — spans for every RPC and
every presigned request, and W3C trace context carried to the server and to
storage:

```go
otelInterceptor, err := otelconnect.NewInterceptor()        // connectrpc.com/otelconnect
transfer, err := paladin.NewTransfer(paladin.WithTransferHTTPClient(&http.Client{
	Transport: otelhttp.NewTransport(http.DefaultTransport), // go.opentelemetry.io/contrib/…/otelhttp
}))
p, err := paladin.Connect(endpoints,
	paladin.WithClientOptions(connect.WithInterceptors(otelInterceptor)),
	paladin.WithTransfer(transfer))
```

### Resource names and object URIs

The server's naming rules, as types: a name built here is one it accepts,
and a name parsed here is one it would. Under a tenant a name takes the
tenant's **id**, a UUID, not its slug; a collection may contain `/`; object
and version ids are UUIDs. `ErrInvalidName` for anything else.

| Type | Form |
| --- | --- |
| `TenantName`, `ParseTenantName` | `tenants/{tenant}` — the id or the slug |
| `CollectionName`, `ParseCollectionName` | `tenants/{tenant-id}/collections/{collection}` |
| `ObjectName`, `ParseObjectName` | `…/collections/{collection}/objects/{object-id}` |
| `ObjectVersionName`, `ParseObjectVersionName` | `…/objects/{object-id}/versions/{version-id}` |
| `ObjectURI`, `ParseObjectURI` | `paladin://tenants/{tenant-id}/collections/{collection}/keys/{key}` — an object by its key; the collection and the key are escaped path segments |
| `BucketName`, `ParseBucketName` | `storageBackends/{backend}/buckets/{bucket}` — a physical bucket on the admin plane, and a bucket quota's parent |

`ObjectResource(tenant, collection, key)` is the resource a capability grants
on an object, or on every object under a key prefix: `object://{tenant-id}/{collection}/{key}`.
`APITokenPrefix` (`paladin_pat_`) starts every API token; the server reads
both from here.

`LookupObject(ctx, p.Data, uri)` finds the object an `ObjectURI` names, and
`DownloadURI(ctx, p.Data, "paladin://…", opts)` downloads it. The URI extends
the `paladin://` resource space the MCP bridge serves.

To create a bucket and bind collections to it at every boot, call
`p.Data.StorageBootstrap.EnsureTenantStorage`: it is idempotent on the
server, and reports which collections it created and which already existed.

### Verifying webhook deliveries

An HTTP event subscription with a signing secret receives each delivery with
`X-Paladin-Webhook-Signature: t=<unix seconds>,v1=<hex>`, an HMAC-SHA256 of
`<t>.<body>`. Verify it against the raw body before trusting anything in it:

```go
body, _ := io.ReadAll(r.Body)
if err := paladin.VerifyWebhook(secret, r.Header.Get(paladin.HeaderWebhookSignature), body); err != nil {
    http.Error(w, "bad signature", http.StatusUnauthorized)
    return
}
```

| Name | Does |
| --- | --- |
| `VerifyWebhook(secret, header, body, opts...)` | nil when one `v1` matches under `secret`, compared in constant time, and `t` is within `DefaultWebhookTolerance` (5 minutes) of now, either way; otherwise an error wrapping `ErrWebhookSignature`. `WithWebhookTolerance(d)` and `WithWebhookClock(now)` change the window and the clock. |
| `SignWebhook(secret, t, body)` | The header value the server sends, for a test of your own handler. |

A replay inside the window verifies: deduplicate on `X-Paladin-Event-Id`,
which is stable across retries.

### Testing with a fake: `paladintest`

`paladintest.New(t)` starts an in-memory data plane for the tests of a
program built on the SDK, stopped when the test ends. Uploads and downloads
go through presigned URLs on its own storage, as against the real server.

```go
srv := paladintest.New(t)
p := srv.Connect()
obj, err := paladin.Upload(ctx, p.Data, paladin.UploadInput{
	Parent: srv.Collection().String(), Key: "a.pdf", ContentType: "application/pdf", Size: n, Body: body,
}, paladin.UploadOptions{})
data, _ := srv.Content(obj.GetName())
```

It serves `ObjectService` (upload, complete, get, lookup, list, download,
delete), `MultipartUploadService`, `PresignService` (`RegenerateUploadUrl`,
`PresignDownload`) and `StorageBootstrapService`; every other RPC answers
`Unimplemented`. Like the server it runs protovalidate on every request,
after authentication, so a request the contract's rules refuse is
`ErrInvalidArgument`; it checks `DeleteObject`'s `resource_version` —
`ErrVersionConflict` for one that is not the object's — and advances it on
every change to the object. `ListObjects` refuses `filter`, `order_by` and
`sort_order` as `Unimplemented` rather than list as if they were not set.
Like the server it refuses a collection
named by the tenant's slug and a completion whose ETag, when given, is not
the content's; completing a completed object returns it. Like the server it
holds one object per key: an upload to a key another object holds, in any
state and the trash included, is refused with `ErrAlreadyExists` until a
`Permanent` delete frees it, and `LookupObject` finds an object in any state
but deleted. It keeps an upload's metadata and tags. It records the
checksum an upload completes with — so `Download` verifies — and answers
range requests. `EnsureTenantStorage` reports a bucket and collections
created the first time and existing after, for any backend id. `Put` stores
an object directly; `MarkFailed` fails a pending one, as the server's
reconciler does when its URL expired with nothing stored; `Tenant` and `Collection` name the fake's tenant and its
collections, all of which exist; `Requests` lists the RPCs received, each
with its `Procedure`, `Header` and `Message` — a copy of the request — for a
test of what the client sent. `Calls(procedure, match)` returns a
procedure's requests whose message `match` accepts, so a test sharing the
fake counts its own:

```go
mine := srv.Calls(paladindatav1connect.ObjectServiceGetObjectProcedure, func(m proto.Message) bool {
	return m.(*datav1.GetObjectRequest).GetName() == obj.GetName()
})
```

`PresignDownload` signs a GET on the fake's storage, as the server does: it
expires after `DefaultDownloadTTL` (15 minutes) unless the request names a
TTL up to `MaxPresignTTL`, carries `If-Match` when `require_etag_match` is
set, and answers with the request's `content_disposition`. A pending or
failed object is `FailedPrecondition`, an unknown one `NotFound`.

Failures are injected on both sides. `FailRPC(procedure, times, code)` makes
the next `times` calls of a procedure of a served service answer `code`, with
the `ErrorInfo` reason the server attaches to it, so `errors.Is` and
`paladin.Reason` read it as they would the server's; calls after those are
served, every failed one is in `Requests`, and the returned `reset` clears
what is left:

```go
srv.FailRPC(paladindatav1connect.ObjectServiceGetObjectProcedure, 1, connect.CodeUnavailable)
// the first GetObject is Unavailable, the second is served
```

`FailRPCIf(procedure, match, times, code)` fails only the calls whose request
`match` accepts — one object, one collection — and serves the procedure's
other calls, so parallel tests on one fake each fail their own. Its failures
stack; the latest that matches takes a call, and a later `FailRPC` still
replaces the earlier one.

`FailStorage` answers storage requests with a status of the test's choosing
— an expired URL, a busy store — and `FailStorageAfterStoring` loses a PUT's
answer after storing its body; `StorageOps` lists what storage received.

By default the fake serves every call, whatever credential it carries.
`paladintest.New(t, paladintest.WithStrictAuth())` checks credentials as the
data plane does. The data plane takes the tenant from the credential — there
is no tenant header — so the fake issues the credentials it accepts, each
for a tenant: `IssueBearerToken`, `IssueAPIToken` (with the server's
`paladin_pat_` prefix) and `IssueCapability`. `Revoke(token)` revokes one.

```go
srv := paladintest.New(t, paladintest.WithStrictAuth())
p := srv.Connect(paladin.WithBearerToken(srv.IssueBearerToken(srv.Tenant())))
```

It refuses as the server does, with the server's code and message and no
`ErrorInfo` reason, since the server's authentication sends none:

| Call | Answer |
| --- | --- |
| No credential, or an `Authorization` that is not a bearer token | `Unauthenticated` |
| A bearer or API token the fake did not issue, or revoked | `Unauthenticated` |
| A capability the fake did not issue, or revoked | `PermissionDenied` — the server's answer to any capability it cannot verify |
| A `name` or `parent` in another tenant, or another tenant's multipart upload | `PermissionDenied` |

It checks only that: the credential is one it issued, not revoked, and of
the tenant the call names. It verifies no signature, Biscuit, caveat,
scope, audience or expiry, and has no platform admin acting in another
tenant. A refused call is in `Requests`, and is refused before `FailRPC`'s
failures, which it does not spend.

### Concurrency

A `*Paladin`, its planes, a `Transfer` and a `Session` are safe for
concurrent use, and meant to be shared: build one at start-up and use it
from every goroutine. The generated clients hold no per-call state, and the
connection pools are what make many calls cheap — a `Transfer` keeps
`DefaultTransferMaxIdleConnsPerHost` connections to each storage host.

| Name | Does |
| --- | --- |
| `UploadOptions.PartConcurrency` | Parts of one multipart upload in flight at once (default 3). |
| `DownloadMany(ctx, p.Data, names, concurrency, fn)` | Downloads many objects, `concurrency` at a time (`DefaultBulkConcurrency`, 8), handing each reader to `fn` — which runs concurrently and reads it. Returns the names that failed, with their errors; one bad object does not stop the rest. |
| `UploadMany(ctx, p.Data, inputs, concurrency, opts)` | Uploads many inputs, `concurrency` at a time (`DefaultBulkConcurrency`), each as `Upload` does with `opts`, and returns the objects in the order of `inputs` — nil for one that failed — with the indexes that failed and their errors; one bad input does not stop the rest, and each is completed, or aborted, on its own. A cancelled `ctx` stops starting more. Up to `concurrency × PartConcurrency` parts are in flight, held in memory for `Stream` inputs. `ErrBulkSession` for every input when `opts` sets `OnSession` or `Resume`: those name one upload, so resume through `Upload`. |

### Parity with the Python SDK

Both SDKs run the same scenarios against a live server
([`sdk/testdata/scenarios.json`](../testdata/scenarios.json), in CI's stack
gate) and parse names against the same table
([`sdk/testdata/names.json`](../testdata/names.json)), which the server's
tests hold its parsers to as well. Where they differ, it is on purpose:

| | Go | Python | Why |
| --- | --- | --- | --- |
| Typed errors | `errors.Is(err, paladin.ErrNotFound)`, `*paladin.Error` | `except paladin.NotFoundError`, `PaladinError` | Each language's idiom; the same kinds, fields and reasons. |
| Server identity over TLS | `TLS.ServerID`: the SPIFFE ID, checked by `go-spiffe` | `TLS(server_id=…)`: the same check, after the handshake and before any request byte | Python's connections under `TLS` run on `ssl` and httpcore, because `pyqwest` has no peer-verification hook; they are the `tls` extra. |
| Minimum TLS version | `TLS.MinVersion`, 1.2 by default | `TLS(min_version=…)`, `ssl.TLSVersion.TLSv1_2` by default | |
| CRC32C verification | Always | With the `crc32c` extra; otherwise not verified | The standard library has no CRC32C. |
| Webhook signatures | `VerifyWebhook`, options for the window and clock | `verify_webhook`, keyword arguments | Each language's idiom; both run the vectors in `sdk/testdata/webhook_signatures.json`. |
| Biscuit attenuation | `capability.Attenuate`, from the capability module | `attenuate`, with the `biscuit` extra | Go uses the server's own code; Python writes the same facts with `biscuit-python`. |
| OpenTelemetry | connect's `otelconnect` and `otelhttp`, through the options | `pyqwest`'s own spans, through `http_client` and `Transfer(otel=True)` | `connectrpc-otel` 0.2.0 fails on connect-python 0.9.0. |
| Bulk transfers | `DownloadMany`, a callback per reader; `UploadMany`, objects in input order and failures by index | `download_many` / `adownload_many` and `upload_many` / `aupload_many`, iterators of results as each finishes | Each language's idiom. |
| asyncio | — | An `a…` form of every workflow | Go has goroutines. |

### Cookbook

Runnable examples, in [`paladin/example_cookbook_test.go`](paladin/example_cookbook_test.go);
`go test` compiles every one and runs those with output against `paladintest`:

| Example | Shows |
| --- | --- |
| `Example_splitHorizonPresign` | URLs signed for a public storage host, sent in-cluster with the signed `Host`. |
| `Example_mutualTLSWithSPIFFE` | mTLS from a rotating workload identity, the server checked by its SPIFFE ID. |
| `Example_rotatingToken` | A token read from the store operators rotate it in, on every call. |
| `Example_bulkIngestion` | Many documents through `DownloadMany` at a bounded concurrency. |
| `Example_resumableMultipart` | A multipart upload resumed from `ListParts` after a crash, through `Transfer.Put`. |
| `Example_durableUpload` | `Upload` tried again until it completes, its multipart session kept where a restart finds it and resumed; its own key, the content's SHA-256 in its metadata, keeps a retry after a lost answer from storing it twice — what an earlier attempt left at the key is taken, completed, or deleted and uploaded again (`cookbook_durable_test.go`). |
| `Example_streamingLargeObjects` | Upload from a stream, download into a consumer, verified at the end. |
| `Example_migratingFromConnectJSON` | A hand-written Connect-JSON call and its SDK form. |

### Header names

`HeaderAuthorization`, `HeaderAPIToken`, `HeaderCapability`, `HeaderDPoP` and
`HeaderIdempotencyKey` are the names the server reads; `HeaderUserAgent` and
`HeaderRetryAfter` are the two the SDK sends and reads besides. The server imports
them from here, so the two cannot drift.

## Services and methods

Every method takes a `context.Context` and a `*connect.Request[…Request]`
and returns a `*connect.Response[…]`. The request and response messages, and
what each field means, are documented in the `.proto` files under
[`proto/paladin`](../../proto/paladin).

### Admin plane

| Service | Methods |
| --- | --- |
| `APITokenService` | `Create`, `Revoke`, `List`, `GetSelf`, `GetUsage` |
| `AuditLogService` | `ListAuditLog`, `GetAuditLogEntry`, `ExportAuditLog` |
| `BackendService` | `CreateBackend`, `GetBackend`, `UpdateBackend`, `DeleteBackend`, `ListBackends`, `RotateCredentials`, `TestBackend`, `SetBackendEnabled`, `SetBackendReadOnly`, `SetBackendMaintenance` |
| `BillingService` | `GetTenantSummary`, `GetTenantTimeSeries` |
| `BucketService` | `CreateBucket`, `GetBucket`, `UpdateBucket`, `DeleteBucket`, `ListBuckets`, `SetBucketPolicy`, `SetLifecycleRules`, `SetObjectLock`, `SetVersioning`, `SetReplication`, `ListAccessibleBuckets` |
| `CapabilityService` | `Issue`, `Delegate`, `Revoke`, `RevokeBiscuit`, `GetBiscuitUsage`, `Get`, `List`, `GetUsage` |
| `CELService` | `Validate` |
| `CollectionService` | `CreateCollection`, `GetCollection`, `UpdateCollection`, `DeleteCollection`, `ListCollections`, `SetCollectionPolicy`, `BindCollectionToBucket` |
| `EventSubscriptionService` | `CreateSubscription`, `GetSubscription`, `UpdateSubscription`, `DeleteSubscription`, `ListSubscriptions`, `TestSubscription`, `RedriveFailedDeliveries` |
| `MCPInspectService` | `Inspect`, `ListSessions`, `GetBridgeStatus` |
| `PlatformOperationService` | `GetOperation`, `ListOperations`, `CancelOperation` |
| `PolicyService` | `Validate`, `SimulateAuthz`, `GetEffectivePolicy` |
| `QuotaService` | `GetQuota`, `SetQuota`, `ResetUsage` |
| `SystemService` | `GetConfig`, `GetDispatcherStats`, `GetPlatformStats`, `ListPlatformStatsTenants` |
| `TenantBudgetService` | `Get`, `Set`, `Summarize` |
| `TenantService` | `CreateTenant`, `GetTenant`, `UpdateTenant`, `DeleteTenant`, `ListTenants`, `SetInheritedPolicy`, `RestoreTenant`, `PurgeTenant`, `RenameTenantSlug`, `MigrateTenantStorageLayout`, `GetTenantStorageMigration`, `ResolveRenamedSlug`, `GetTenantDefaultBinding`, `SetTenantDefaultBinding`, `ClearTenantDefaultBinding` |

### Data plane

| Service | Methods |
| --- | --- |
| `BatchService` | `BatchDeleteObjects`, `BatchCopyObjects`, `BatchRestoreObjects`, `BatchUpdateTags` |
| `MultipartUploadService` | `InitiateMultipartUpload`, `PresignPart`, `CompleteMultipartUpload`, `AbortMultipartUpload`, `ListParts` |
| `ObjectService` | `UploadObject`, `DownloadObject`, `GetObject`, `LookupObject`, `UpdateObject`, `CompleteObject`, `DeleteObject`, `RestoreObject`, `CopyObject`, `ListObjects`, `CountObjects`, `ListObjectVersions`, `GetObjectVersion`, `RestoreObjectVersion`, `SetObjectRetention`, `SetObjectLegalHold`, `GetObjectLock`, `SetObjectTaint` |
| `ObjectTagService` | `GetObjectTags`, `PutObjectTags`, `DeleteObjectTags`, `ListDistinctTags` |
| `OperationService` | `GetOperation`, `ListOperations`, `CancelOperation` |
| `PresignService` | `RegenerateUploadUrl`, `PresignDownload` |
| `StorageBootstrapService` | `EnsureTenantStorage` |

### IAM plane

| Service | Methods |
| --- | --- |
| `AuthService` | `Login`, `RefreshToken`, `Revoke`, `WhoAmI`, `ChangePassword`, `ExchangeAudience`, `ListMyMemberships`, `SwitchTenant` |
| `HealthService` | `GetVersion`, `GetHealth` |
| `UserService` | `CreateUser`, `GetUser`, `UpdateUser`, `DeleteUser`, `ListUsers`, `GrantScopes`, `RevokeScopes`, `ResetPassword` |
| `UserSettingsService` | `GetMine`, `UpdateMine`, `GetForUser`, `ListByTenant`, `DeleteForUser` |

`paladin/readme_test.go` fails when a service or method in `gen/` is missing
from the tables above.

## Versioning

The SDK has its own version, `sdk/go/vX.Y.Z`, cut automatically from the
commits that touch `sdk/` or `proto/` ([docs/releasing.md](../../docs/releasing.md)).
Pre-1.0, a minor version may break the contract or this package's own API; see
[`docs/upgrading.md`](../../docs/upgrading.md).
