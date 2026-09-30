# Paladin Go SDK

```bash
go get github.com/oleg-tkachuk/paladin/sdk/go
```

Two parts:

- `gen/` — protobuf types and Connect clients generated from
  [`proto/`](../../proto). The service clients are these, unwrapped.
- `paladin/` — a thin client that holds what every call needs: one plane's
  base URL, credentials, the idempotency key, and retries for calls that are
  safe to repeat.

Only this module's own dependencies come with it — protobuf, Connect and the
annotation packages the stubs import. Nothing of the server.

## Planes

Paladin serves three planes on separate listeners. Create one `paladin.Client`
per plane you talk to, and build that plane's service clients from it.

| Plane | Package | Services |
| --- | --- | --- |
| Admin | `gen/paladin/admin/v1/paladinadminv1connect` | tenants, buckets, backends, policies, quotas, tokens, audit |
| Data | `gen/paladin/data/v1/paladindatav1connect` | objects, multipart uploads, tags, batches, presigning |
| IAM | `gen/paladin/iam/v1/paladiniamv1connect` | login, tokens, users, health |

## Example

```go
import (
	"context"

	"connectrpc.com/connect"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

c, err := paladin.New("https://admin.paladin.example",
	paladin.WithBearerToken(apiToken),
	paladin.WithRetries(3, 0))
if err != nil {
	return err
}
tenants := paladinadminv1connect.NewTenantServiceClient(c.HTTPClient(), c.BaseURL(), c.ClientOptions()...)

ctx = paladin.WithIdempotencyKey(ctx, requestID)
resp, err := tenants.CreateTenant(ctx, connect.NewRequest(&adminv1.CreateTenantRequest{ /* … */ }))
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
| `WithCapability(token)` | Sends a capability token in `X-Paladin-Capability`. Can be combined with a bearer token. |
| `WithRetries(attempts, baseDelay)` | Retries a unary call on `Unavailable` or `ResourceExhausted`, up to `attempts` calls in total, doubling the delay from `baseDelay` (zero means `DefaultRetryBaseDelay`, 100ms) up to `DefaultRetryMaxDelay` (5s). Only calls safe to repeat are retried: RPCs the contract declares side-effect free or idempotent, and any call whose context carries an idempotency key. Streams are never retried. |
| `WithHTTPClient(c)` | Replaces `http.DefaultClient` — for timeouts, proxies, custom TLS. |
| `WithClientOptions(opts...)` | Passes Connect options through, e.g. `connect.WithGRPC()` or `connect.WithSendGzip()`. |

### Idempotency

| Function | Does |
| --- | --- |
| `WithIdempotencyKey(ctx, key) context.Context` | Every call made with the returned context sends `Idempotency-Key: <key>`. The server replays the first response for a key it has seen, so repeating a mutating call with the same key is safe. Reuse the key only for the same logical operation. |
| `IdempotencyKey(ctx) (string, bool)` | The key attached to `ctx`; an empty key counts as none. |

### Header names

`HeaderAuthorization`, `HeaderAPIToken`, `HeaderCapability` and
`HeaderIdempotencyKey` are the names the server reads. The server imports
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
| `EventSubscriptionService` | `CreateSubscription`, `GetSubscription`, `UpdateSubscription`, `DeleteSubscription`, `ListSubscriptions`, `TestSubscription` |
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

The SDK is versioned with the API contract: `sdk/go/vX.Y.Z` is tagged beside
`api/vX.Y.Z`. Pre-1.0, a minor version may break the contract; see
[`docs/upgrading.md`](../../docs/upgrading.md).
