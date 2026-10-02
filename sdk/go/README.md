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
| `WithCapability(token)` | Sends a capability token in `X-Paladin-Capability`. Can be combined with a bearer token. |
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
| `WithIdempotencyKey(ctx, key) context.Context` | Every call made with the returned context sends `Idempotency-Key: <key>`. The server replays the first response for a key it has seen, so repeating a mutating call with the same key is safe. A response that carries a credential (a minted API or capability token, a generated password) is not replayed: the repeat returns `AlreadyExists`, because the credential is delivered once and not stored. Reuse the key only for the same logical operation. |
| `IdempotencyKey(ctx) (string, bool)` | The key attached to `ctx`; an empty key counts as none. |
| `WithoutIdempotencyKey(ctx) context.Context` | Every call made with the returned context goes out with no key — not the default one, not the request's field — and so is never retried, unless the contract declares it side-effect free or idempotent. For an operation that must run again when repeated rather than be answered with the first response. The server refuses `Create*` and `Issue*` without a key. The last `WithIdempotencyKey` or `WithoutIdempotencyKey` on a context wins. |
| `WithRetryable(func(error) bool) Option` | Replaces `DefaultRetryable` (`Unavailable`, `ResourceExhausted`) as the test of which failures `WithRetries` retries. It cannot make an unsafe call retried: the rule above still applies. |

### Tokens

| Name | Does |
| --- | --- |
| `NewSession(ctx, iamURL, subject, password, opts...) (*Session, error)` | Signs in at the IAM plane and keeps the refresh token. `Token(ctx, audience)` returns that plane's access token: the IAM one by refreshing, the others by `ExchangeAudience`. Each is cached until `TokenRefreshMargin` (30s) before it expires. When the refresh token itself is refused, the session signs in again. Safe for concurrent use; concurrent callers wait for one mint. |
| `SessionFromRefreshToken(iamURL, refreshToken, opts...)` | Resumes from a stored refresh token; cannot sign in again when it expires. `(*Session).RefreshToken()` is the current one to store — refreshing rotates it. |
| `WithSessionClock(now)` | Replaces `time.Now`, for tests. |
| `StaticToken(token)` | The same token for every plane: an API token, or a JWT from elsewhere. |
| `WithTokens(ts)` | With `Connect`: each plane gets `ts`'s token for its own audience. `New` refuses it with `ErrNoAudience`. |
| `WithTokenSource(ts, audience)` | One plane's client: the token for `audience`. A call refused as `Unauthenticated` is made once more with a fresh token — the server authenticates before anything else, so the first attempt changed nothing. |
| `AudienceData`, `AudienceAdmin`, `AudienceIAM` | The audience names; the server imports them from here. |

### Workflows

| Name | Does |
| --- | --- |
| `Pages(ctx, call, req, items) iter.Seq2[Item, error]` | Calls a List RPC page by page, following `next_page_token`, and yields every item; `items` picks them out of a response, e.g. `(*datav1.ListObjectsResponse).GetObjects`. The first error is yielded and ends it; leaving the loop makes no further calls. `ErrNotPaged` for an RPC without `page`. |
| `Wait(ctx, get) (Op, error)` | Polls `get` until the operation is done, from `DefaultPollInterval` (500ms) doubling to `DefaultMaxPollInterval` (10s). An operation that failed comes back with an `*OperationError` carrying its status; `Code()` is the Connect code. `ctx` bounds the wait. Works for both planes' `Operation`. |
| `Mask[M](paths...) (*fieldmaskpb.FieldMask, error)` | An update mask for `M` from proto field names, nested ones with `.`, each checked against `M`'s descriptor: `ErrUnknownMaskPath` for one it lacks. |
| `Upload(ctx, p.Data, UploadInput, UploadOptions) (*datav1.Object, error)` | Uploads `Size` bytes from exactly one of `Body` (an `io.ReaderAt`: `*os.File`, `*bytes.Reader`) and `Stream` (an `io.Reader` read once: a pipe, a response body) and completes the object; neither is read into memory whole. Up to `MultipartThreshold` (default `DefaultMultipartThreshold`, 8 MiB) one presigned PUT, which records the content's SHA-256 on the object; above it multipart, `PartConcurrency` parts at a time (default 3), aborted if any part fails. `ErrUploadBody` for neither or both. |
| `Download(ctx, p.Data, name, DownloadOptions) (*ObjectReader, error)` | Streams the object's content; close the reader. `DownloadOptions{Offset, Length}` reads a byte range (`ErrRangeIgnored` when storage answers with the whole object). A whole read is verified: the last `Read` returns an `*IntegrityError` instead of `io.EOF` when the size or the recorded checksum (SHA-256, CRC32C, MD5) does not match. `ObjectReader` carries the `Object`, `ContentType` and `ContentLength`. |

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
| `*TransferError` | A request storage refused, or answered with a redirect: `Method`, `Host` (the URL's query is the signature and is not kept), `Status`, and the first 512 bytes of the `Body`. |

### TLS and workload identity

`WithTLS` makes the client's connections to Paladin, and `WithTransferTLS` a
`Transfer`'s connections to storage, with a CA bundle, a client certificate
and an expected server identity. The files are read when the client is built
and again whenever they change on disk, so certificates a workload-identity
agent rotates are picked up without a restart; a connection already open
keeps the one it was made with.

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

`(TLS).Transport()` returns the `*http.Transport` both options build, for a
client of your own; `WithTLS` with `WithHTTPClient` is `ErrTLSAndHTTP`.
These connections are made directly: a proxy from the environment would
make the TLS connection itself, without the files.
||||||| parent of 24044e2c (feat(sdk): give go callers typed errors with the server's reason)
### Errors

Every failed call through a client from `New` or `Connect` returns an
`*Error` that wraps the `*connect.Error` — `connect.CodeOf` and
`errors.As(err, &connectErr)` work as before — and matches the kind of
failure with `errors.Is`. Match on these, not on codes or messages:

| Kind | Code | Means |
| --- | --- | --- |
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

### Header names

`HeaderAuthorization`, `HeaderAPIToken`, `HeaderCapability` and
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
| `CapabilityService` | `Issue`, `Delegate`, `Revoke`, `List`, `GetUsage` |
| `CELService` | `Validate` |
| `CollectionService` | `CreateCollection`, `GetCollection`, `UpdateCollection`, `DeleteCollection`, `ListCollections`, `SetCollectionPolicy`, `BindCollectionToBucket` |
| `EventSubscriptionService` | `CreateSubscription`, `GetSubscription`, `UpdateSubscription`, `DeleteSubscription`, `ListSubscriptions`, `TestSubscription`, `RedriveFailedDeliveries` |
| `MCPInspectService` | `Inspect`, `ListSessions`, `GetBridgeStatus` |
| `PlatformOperationService` | `GetOperation`, `ListOperations`, `CancelOperation` |
| `PolicyService` | `Validate`, `SimulateAuthz`, `GetEffectivePolicy` |
| `QuotaService` | `GetQuota`, `SetQuota`, `ResetUsage` |
| `SystemService` | `GetConfig`, `GetDispatcherStats`, `GetPlatformStats` |
| `TenantBudgetService` | `Get`, `Set`, `Summarize` |
| `TenantService` | `CreateTenant`, `GetTenant`, `UpdateTenant`, `DeleteTenant`, `ListTenants`, `SetInheritedPolicy`, `RestoreTenant`, `PurgeTenant`, `RenameTenantSlug`, `MigrateTenantStorageLayout`, `GetTenantStorageMigration`, `ResolveRenamedSlug`, `GetTenantDefaultBinding`, `SetTenantDefaultBinding`, `ClearTenantDefaultBinding` |

### Data plane

| Service | Methods |
| --- | --- |
| `BatchService` | `BatchDeleteObjects`, `BatchCopyObjects`, `BatchRestoreObjects`, `BatchUpdateTags` |
| `MultipartUploadService` | `InitiateMultipartUpload`, `PresignPart`, `CompleteMultipartUpload`, `AbortMultipartUpload`, `ListParts` |
| `ObjectService` | `UploadObject`, `DownloadObject`, `GetObject`, `LookupObject`, `UpdateObject`, `CompleteObject`, `DeleteObject`, `RestoreObject`, `CopyObject`, `ListObjects`, `CountObjects`, `ListObjectVersions`, `GetObjectVersion`, `RestoreObjectVersion`, `SetObjectRetention`, `SetObjectLegalHold`, `GetObjectLock` |
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
