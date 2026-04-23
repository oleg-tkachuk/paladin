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
	"errors"
	"time"

	"github.com/google/wire"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/batch"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/bucket"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/category"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/multipart"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/operation"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/presign"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/tenant"
	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/filter/cel"
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

	ProvideBucketHandler,
	ProvideTenantHandler,
	ProvideCategoryHandler,
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
	Bucket    bucket.Repository
	Tenant    tenant.Repository
	Category  category.Repository
	Presign   presign.Repository
	Multipart multipart.Repository
	Operation operation.Repository
}

// Storage bundles the storage-side adapters. Every handler package declares
// its own narrow Storage interface; a single backend adapter typically
// implements all four.
type Storage struct {
	Object    object.Storage
	Multipart multipart.Storage
	Presign   presign.Storage
	Stream    object.StreamSink
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
		DefaultTTL:     cfg.Presign.DefaultTTL,
		MaxTTL:         cfg.Presign.MaxTTL,
		DefaultMaxSize: cfg.Presign.DefaultMaxSize,
	})
}

func ProvideBucketHandler(repos Repos, pe *policy.Engine) *bucket.Handler {
	return bucket.NewHandler(repos.Bucket, pe)
}

func ProvideTenantHandler(repos Repos) *tenant.Handler {
	return tenant.NewHandler(repos.Tenant)
}

func ProvideCategoryHandler(repos Repos) *category.Handler {
	return category.NewHandler(repos.Category)
}

func ProvideOperationHandler(repos Repos) *operation.Handler {
	return operation.NewHandler(repos.Operation)
}

func ProvideBatchHandler(opH *operation.Handler, pe *policy.Engine) *batch.Handler {
	return batch.NewHandler(opH, pe)
}

func ProvidePresignHandler(repos Repos, storage Storage, pe *policy.Engine, cfg config.Config) *presign.Handler {
	return presign.NewHandler(repos.Presign, storage.Presign, pe, presign.Config{
		DefaultTTL:     cfg.Presign.DefaultTTL,
		MaxTTL:         cfg.Presign.MaxTTL,
		DefaultMaxSize: cfg.Presign.DefaultMaxSize,
	})
}

func ProvideMultipartHandler(repos Repos, storage Storage, pe *policy.Engine, sm *statemachine.Transitioner) *multipart.Handler {
	return multipart.NewHandler(repos.Multipart, storage.Multipart, pe, sm)
}
