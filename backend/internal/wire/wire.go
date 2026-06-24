// Provider set for the single-stack PALADIN server. Repositories, storage
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
	"time"

	"github.com/google/uuid"

	"github.com/google/wire"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/audith"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/backendh"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/bucketh"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/eventsubh"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/quotah"
	"github.com/oleg-tkachuk/paladin/internal/api/iam/v1/apikeyh"
	"github.com/oleg-tkachuk/paladin/internal/api/iam/v1/authh"
	"github.com/oleg-tkachuk/paladin/internal/api/iam/v1/userh"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/batch"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/bucket"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/multipart"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
	objectkey "github.com/oleg-tkachuk/paladin/internal/api/v1/object_key"
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
	policy "github.com/oleg-tkachuk/paladin/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres"
)

// Version / Commit / BuildTime carry build metadata from the CLI entrypoint.
type Version string
type Commit string
type BuildTime string
type ConfigPath string

// BootstrapLogger is a distinct type so Wire can inject an early-boot logger
// that exists before the config-driven logger is built.
type BootstrapLogger *zap.Logger

// ProviderSet assembles every handler, engine, and long-lived resource in
// the PALADIN runtime. Storage/Repos are *not* provided here — the caller must
// pass concrete implementations into InitializeApp.
var ProviderSet = wire.NewSet(
	ProvidePgxPool,
	ProvideCELEvaluator,
	ProvidePolicyStore,
	ProvidePolicyEngine,
	ProvideStateMachine,

	ProvideObjectKeyHandler,
	ProvideBucketHandler,
	ProvideTenantHandler,
	ProvideObjectTagHandler,
	ProvideOperationHandler,
	ProvideBatchHandler,
	ProvideObjectHandler,
	ProvidePresignHandler,
	ProvideMultipartHandler,

	wire.Bind(new(batch.Submitter), new(*operation.Handler)),
	wire.Bind(new(policy.Store), new(*policy.PostgresStore)),
)

// ProvidePgxPool extracts the concrete *pgxpool.Pool from *postgres.DB.
// statemachine + policy store need the concrete pool for LISTEN/NOTIFY
// and transaction APIs the narrower interface doesn't expose.
func ProvidePgxPool(db *postgres.DB) (*pgxpool.Pool, error) {
	p, ok := db.Pool.(*pgxpool.Pool)
	if !ok {
		return nil, errors.New("wire: DB.Pool is not *pgxpool.Pool — tests must wire this directly")
	}
	return p, nil
}

func ProvideCELEvaluator() *cel.Evaluator {
	return cel.NewEvaluator()
}

func ProvidePolicyStore(pool *pgxpool.Pool, _ *zap.Logger) *policy.PostgresStore {
	return policy.NewPostgresStore(pool)
}

func ProvidePolicyEngine(store policy.Store, cfg config.Config) *policy.Engine {
	ttl := cfg.Cedar.PolicyCacheTTL
	if ttl == 0 {
		ttl = 30 * time.Second
	}
	return policy.NewEngine(store, ttl)
}

func ProvideStateMachine(pool *pgxpool.Pool) *statemachine.Transitioner {
	return statemachine.New(pool)
}

// Repos bundles all repository interfaces. The caller constructs this from
// its Postgres adapters (see cmd/server).
type Repos struct {
	Object    object.Repository
	ObjectKey objectkey.Repository
	Bucket    bucket.Repository
	Tenant    tenant.Repository
	ObjectTag objecttag.Repository
	Presign   presign.Repository
	Multipart multipart.Repository
	Operation operation.Repository

	// v2 admin/iam stores.
	BackendV2     admindomain.BackendRepository
	BucketV2      admindomain.BucketRepository
	Audit         admindomain.AuditRepository
	Quota         admindomain.QuotaRepository
	EventSub      admindomain.EventSubscriptionRepository
	IAMUser       authstore.UserRepository
	IAMApiKey     authstore.ApiKeyRepository
	IAMRefresh    authstore.RefreshTokenRepository
	ObjectVersion object.VersionRepository

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

func ProvideObjectKeyHandler(repos Repos, pe *policy.Engine, cfg config.Config) *objectkey.Handler {
	return objectkey.NewHandler(repos.ObjectKey, pe, cfg.Storage.DefaultBackend)
}

func ProvideBucketHandler(repos Repos, storage Storage, cfg config.Config) *bucket.Handler {
	return bucket.NewHandler(repos.Bucket, storage.Provisioner, cfg.Storage.DefaultBackend)
}

func ProvideTenantHandler(repos Repos, pe *policy.Engine) *tenant.Handler {
	return tenant.NewHandler(repos.Tenant, pe)
}

func ProvideObjectTagHandler(repos Repos) *objecttag.Handler {
	return objecttag.NewHandler(repos.ObjectTag)
}

func ProvideOperationHandler(repos Repos, pe *policy.Engine) *operation.Handler {
	return operation.NewHandler(repos.Operation, pe)
}

func ProvideBatchHandler(opH *operation.Handler, pe *policy.Engine) *batch.Handler {
	return batch.NewHandler(opH, pe)
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

func ProvideApiKeyHandler(repos Repos, iss *issuer.Issuer, pe *policy.Engine) *apikeyh.Handler {
	return apikeyh.NewHandler(repos.IAMApiKey, iss, pe).
		WithTenantSlugLookup(tenantSlugLookup(repos.Tenant))
}

// tenantSlugLookup adapts the tenant.Repository to the Slug-resolver shape
// authh / apikeyh expect. Lightweight cache: per-tenant slugs are
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

func ProvideBackendV2Handler(repos Repos, pe *policy.Engine, cfg config.Config) *backendh.Handler {
	// The concrete *BackendRepoV2 satisfies backendh.Repository (domain
	// interface + ADR-0003 tx seam); Repos.BackendV2 is the pgx-free domain
	// type, so assert to the wider local interface here (same as bucketh).
	return backendh.NewHandler(repos.BackendV2.(backendh.Repository), pe, cfg.Storage.DefaultBackend)
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
	return object.NewVersionHandler(repos.Object, repos.ObjectVersion)
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
