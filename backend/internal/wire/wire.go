// Provider set for the single-stack Paladin server. Repositories, storage
// adapters, and the Connect routing shim are accepted as external
// dependencies (`Repos`, `Storage`) so tests can substitute fakes.
//
// Seams the caller is expected to supply:
//
//   - Storage: an S3 (or S3-compatible) adapter that satisfies every
//     handler-package Storage interface: object.Storage, multipart.Storage,
//     presign.Storage, object.StreamSink.
//   - Repos: Postgres-backed adapters over the sqlc-generated queries in
//     internal/store/postgres/sqlc that satisfy the per-handler Repository
//     interfaces.
//   - Connect adapter: thin shim (typically in cmd/server) that binds the
//     generated *Connect.*Handler interfaces to the business-logic methods
//     on the handlers here. It owns proto⇄struct decoding and the HTTP
//     mux; this package does not import connect-go.

package wire

import (
	"context"

	"errors"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/audith"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/backendh"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/bucketh"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/eventsubh"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/quotah"
	"github.com/oleg-tkachuk/paladin/internal/api/iam/v1/authh"
	"github.com/oleg-tkachuk/paladin/internal/api/iam/v1/userh"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/batch"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/bucket"
	objectkey "github.com/oleg-tkachuk/paladin/internal/api/v1/collection"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/multipart"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
	objecttag "github.com/oleg-tkachuk/paladin/internal/api/v1/object_tag"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/operation"
	policyh "github.com/oleg-tkachuk/paladin/internal/api/v1/policy"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/presign"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/tenant"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/auth/issuer"
	authstore "github.com/oleg-tkachuk/paladin/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/internal/middleware"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
	policy "github.com/oleg-tkachuk/paladin/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/internal/statemachine"
)

// NOTE: this package holds the plain constructor functions (Provide*) + the
// shared Repos/Storage seam types that internal/app wires by hand. The former
// Google Wire ProviderSet (wire.NewSet + wire.Bind) was removed: Wire (archived
// upstream) was never actually code-generating here — no injector, no
// wire_gen.go — so it was dead. Dependency injection + lifecycle now run
// through Uber fx (internal/app/appfx.go). The batch.Submitter/policy.Store
// bindings the old wire.Bind expressed are satisfied structurally where the
// handlers are passed.

// Repos bundles all repository interfaces. The caller constructs this from
// its Postgres adapters (see cmd/server).
type Repos struct {
	Object     object.Repository
	Collection objectkey.Repository
	Bucket     bucket.Repository
	Tenant     tenant.Repository
	ObjectTag  objecttag.Repository
	Presign    presign.Repository
	Multipart  multipart.Repository
	Operation  operation.Repository

	// v2 admin/iam stores.
	BackendV2     admindomain.BackendRepository
	BucketV2      admindomain.BucketRepository
	Audit         admindomain.AuditRepository
	Quota         admindomain.QuotaRepository
	EventSub      admindomain.EventSubscriptionRepository
	IAMUser       authstore.UserRepository
	IAMRefresh    authstore.RefreshTokenRepository
	ObjectVersion object.VersionRepository
	// ObjectLock persists object_locks rows (ADR-0013). Optional: nil leaves
	// the lock RPCs Unimplemented and skips applying bucket default retention
	// at promote time.
	ObjectLock object.LockRepository

	// Idempotency is the per-(tenant, method, key) response cache used
	// by the Create* enforcement gate (see internal/middleware/idempotency.go).
	// The gate itself only checks header presence today; per-handler
	// memoize will read/write via this repo when it lands.
	Idempotency middleware.IdempotencyStore
}

// Storage bundles the storage-side adapters. Every handler package declares
// its own narrow Storage interface; a single backend adapter typically
// implements all five.
type Storage struct {
	Object      object.Storage
	Multipart   multipart.Storage
	Presign     presign.Storage
	Stream      object.StreamSink
	Provisioner bucket.Provisioner
}

func ProvideObjectHandler(
	repos Repos,
	storage Storage,
	pe *policy.Engine,
	fe *cel.Evaluator,
	sm *statemachine.Transitioner,
	cfg config.Config,
) *object.Handler {
	return object.NewHandler(repos.Object, storage.Object, pe, fe, sm, object.PresignConfig{
		DefaultTTL:     cfg.Limits.Presign.DefaultTTL,
		MaxTTL:         cfg.Limits.Presign.MaxTTL,
		DefaultMaxSize: cfg.Limits.Presign.DefaultMaxSize,
	})
}

func ProvideCollectionHandler(repos Repos, pe *policy.Engine, _ config.Config) *objectkey.Handler {
	return objectkey.NewHandler(repos.Collection, pe)
}

func ProvideTenantHandler(repos Repos, pe *policy.Engine, _ config.Config) *tenant.Handler {
	return tenant.NewHandler(repos.Tenant, pe)
}

func ProvideOperationHandler(repos Repos, pe *policy.Engine) *operation.Handler {
	return operation.NewHandler(repos.Operation, pe)
}

func ProvideBatchHandler(repos Repos, opH *operation.Handler, pe *policy.Engine) *batch.Handler {
	// repos.Object satisfies batch.BucketResolver — the submit-time Cedar check
	// resolves each target collection's bucket so bucket:/collection: PAT
	// scopes enforce (the batch worker does not re-check Cedar per object).
	return batch.NewHandler(opH, pe, repos.Object)
}

func ProvidePresignHandler(repos Repos, storage Storage, pe *policy.Engine, cfg config.Config) *presign.Handler {
	return presign.NewHandler(repos.Presign, storage.Presign, pe, presign.Config{
		DefaultTTL:     cfg.Limits.Presign.DefaultTTL,
		MaxTTL:         cfg.Limits.Presign.MaxTTL,
		DefaultMaxSize: cfg.Limits.Presign.DefaultMaxSize,
	})
}

func ProvideMultipartHandler(repos Repos, storage Storage, pe *policy.Engine, sm *statemachine.Transitioner) *multipart.Handler {
	return multipart.NewHandler(repos.Multipart, storage.Multipart, pe, sm)
}

// ─── v2 IAM/admin handlers ──────────────────────────────────────────────────

// ProvideIssuer builds the JWT issuer used by Login / RefreshToken /
// MintScopedToken. Fails at startup if no signing key is configured.
func ProvideIssuer(cfg config.Config) (*issuer.Issuer, error) {
	if cfg.Auth.SigningKey == "" {
		return nil, errors.New("wire: auth.signing_key (or signing_key_secret) required")
	}
	return issuer.New(issuer.Config{
		Issuer:            cfg.Auth.Issuer,
		SigningKey:        []byte(cfg.Auth.SigningKey),
		AccessTokenTTL:    cfg.Auth.AccessTokenTTL,
		RefreshTokenTTL:   cfg.Auth.RefreshTokenTTL,
		ScopedTokenMaxTTL: cfg.Auth.ScopedTokenMaxTTL,
	})
}

// ProvideRefreshDecoder produces the verifier used to validate refresh
// tokens presented to AuthService.RefreshToken. Audience is hard-pinned to
// AudienceIAM regardless of which mux delivered the token.
func ProvideRefreshDecoder(cfg config.Config) *auth.RefreshDecoder {
	return &auth.RefreshDecoder{
		Verifier: &auth.JWTVerifier{
			Key:              []byte(cfg.Auth.SigningKey),
			ExpectedIssuer:   cfg.Auth.Issuer,
			ExpectedAudience: auth.AudienceIAM,
			Leeway:           cfg.Auth.Leeway,
		},
	}
}

func ProvideAuthHandler(repos Repos, iss *issuer.Issuer, dec *auth.RefreshDecoder, pe *policy.Engine) *authh.Handler {
	return authh.NewHandler(repos.IAMUser, repos.IAMRefresh, iss, dec, pe).
		WithTenantSlugLookup(tenantSlugLookup(repos.Tenant))
}

func ProvideUserHandler(repos Repos, pe *policy.Engine) *userh.Handler {
	return userh.NewHandler(repos.IAMUser, pe)
}

// tenantSlugLookup adapts the tenant.Repository to the Slug-resolver shape
// authh expects. Lightweight cache: per-tenant slugs are
// effectively immutable (rename is a deferred admin RPC — see BACKLOG),
// so a single Get round-trip per token mint is the worst case for now.
// If the mint volume warrants it, drop in a sync.Map cache here.
func tenantSlugLookup(tr tenant.Repository) func(ctx context.Context, tenantID uuid.UUID) (string, error) {
	if tr == nil {
		return nil
	}
	return func(ctx context.Context, tenantID uuid.UUID) (string, error) {
		t, err := tr.Get(ctx, tenantID)
		if err != nil {
			return "", err
		}
		return t.Slug, nil
	}
}

func ProvideBackendV2Handler(repos Repos, pe *policy.Engine, _ config.Config) *backendh.Handler {
	// The concrete *BackendRepoV2 satisfies backendh.Repository (domain
	// interface + ADR-0003 tx seam); Repos.BackendV2 is the pgx-free domain
	// type, so assert to the wider local interface here (same as bucketh).
	return backendh.NewHandler(repos.BackendV2.(backendh.Repository), pe)
}

func ProvideBucketV2Handler(repos Repos, storage Storage, pe *policy.Engine) *bucketh.Handler {
	// admindomain.Provisioner satisfied by the same s3 adapter that backs
	// storage.Provisioner. We need a small interface adapter — bucketh.Provisioner
	// has the same shape, so just type-cast/wrap.
	// The concrete BucketRepoV2 satisfies bucketh.Repository (domain
	// interface + ADR-0003 tx seam); Repos.BucketV2 is the pgx-free domain
	// type, so assert to the wider local interface here.
	return bucketh.NewHandler(repos.BucketV2.(bucketh.Repository), &bucketProvisionerAdapter{storage.Provisioner}, pe)
}

func ProvideQuotaHandler(repos Repos, pe *policy.Engine) *quotah.Handler {
	// The concrete QuotaRepoV2 satisfies quotah.Repository (the domain
	// interface + the ADR-0003 tx seam). Repos.Quota is typed as the
	// pgx-free domain interface, so assert to the wider local one here.
	return quotah.NewHandler(repos.Quota.(quotah.Repository), pe)
}
func ProvideAuditHandler(repos Repos, pe *policy.Engine) *audith.Handler {
	return audith.NewHandler(repos.Audit, pe)
}
func ProvideEventSubHandler(repos Repos, pe *policy.Engine) *eventsubh.Handler {
	return eventsubh.NewHandler(repos.EventSub, pe)
}

func ProvidePolicyHandler(engine *policy.Engine, store policy.Store) *policyh.Handler {
	return policyh.NewHandler(engine, store)
}

func ProvideVersionHandler(repos Repos) *object.VersionHandler {
	if repos.ObjectVersion == nil {
		return nil
	}
	h := object.NewVersionHandler(repos.Object, repos.ObjectVersion)
	// The lock port rides on the version handler because bucket default
	// retention is applied at promote time, which is where versions are
	// written. Nil is fine — a deployment without object lock promotes
	// exactly as before.
	h.SetLockRepository(repos.ObjectLock)
	return h
}

// ProvideLockHandler builds the object-lock RPC handler. Requires both
// version and lock repositories: a lock has to attach to a version, so
// object lock without versioning is not a configuration this can serve.
func ProvideLockHandler(repos Repos, policy cedar.Authorizer) *object.LockHandler {
	if repos.ObjectVersion == nil || repos.ObjectLock == nil {
		return nil
	}
	return object.NewLockHandler(repos.Object, repos.ObjectVersion, repos.ObjectLock, policy)
}

// bucketProvisionerAdapter bridges the v1 bucket.Provisioner interface to
// the bucketh.Provisioner interface; identical shape, distinct types.
type bucketProvisionerAdapter struct {
	v1 bucket.Provisioner
}

func (a *bucketProvisionerAdapter) CreateBucket(ctx context.Context, backendID, bucketName, region string) error {
	if a.v1 == nil {
		return nil
	}
	return a.v1.CreateBucket(ctx, backendID, bucketName, region)
}

func (a *bucketProvisionerAdapter) DeleteBucket(ctx context.Context, backendID, bucketName string) error {
	if a.v1 == nil {
		return nil
	}
	return a.v1.DeleteBucket(ctx, backendID, bucketName)
}
