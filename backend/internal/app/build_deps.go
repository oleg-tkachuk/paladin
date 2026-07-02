package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/filter/cel"
	policy "github.com/oleg-tkachuk/paladin/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/internal/storage/s3adapter"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/wire"
)

// SharedDeps is the heavy build product every plane and worker subcommand
// needs: repositories over the sqlc-generated queries, the s3 adapter for
// the configured default backend, a started cedar policy engine, the
// state machine, and the CEL evaluator. Each subcommand calls
// BuildSharedDeps once after migrations and bootstrap, then hands the
// returned bundle to per-plane / per-worker builders.
//
// SharedDeps does NOT own the lifetime of the underlying *postgres.DB.
// The caller (cmd/server) opens and closes the DB pool. Likewise the
// cedar engine's Start() is called here but Stop() — if/when added —
// must be invoked by the caller during graceful shutdown.
type SharedDeps struct {
	Cfg     config.Config
	Logger  *zap.Logger
	DB      *postgres.DB
	Pool    *pgxpool.Pool
	Repos   wire.Repos
	Storage wire.Storage

	// Engines built once, shared across handlers.
	PolEngine *policy.Engine
	PolStore  *policy.PostgresStore
	SM        *statemachine.Transitioner
	CELEval   *cel.Evaluator

	// S3 is the concrete client built from cfg.Storage.Backends[default].
	// Workers and admin bucket-provision flows reuse this single client.
	// Stored as *s3adapter.Client; the wire.Storage view above wraps it.
	S3 *s3adapter.Client

	// Capability is the agent-runtime authorisation primitive. Nil when
	// cfg.Capability.Enabled is false; callers must guard.
	Capability *CapabilityBundle

	// APIToken is the hashed-bearer M2M auth primitive. Nil when
	// cfg.APIToken.Enabled is false; callers must guard.
	APIToken *APITokenBundle

	// BackgroundJobs are the goroutines a listener process must run
	// alongside its HTTP handlers. Empty today — the audit writer is now
	// synchronous + crash-durable (ADR-0004), so there is no background
	// flusher to register. Kept on SharedDeps (not returned separately)
	// so future listener-scoped jobs share the same instance.
	BackgroundJobs []BackgroundJob
}

// BuildSharedDeps materialises SharedDeps. Returns ErrNoSigningKey or a
// wrapped configuration error early so the caller can fail-fast before
// any listener binds. The cedar engine's first refresh is awaited inside
// Start(), so this call may take ~hundreds of ms on a cold cache.
func BuildSharedDeps(ctx context.Context, cfg config.Config, db *postgres.DB, l *zap.Logger) (*SharedDeps, error) {
	pool, ok := db.Pool.(*pgxpool.Pool)
	if !ok {
		return nil, errors.New("app: DB.Pool is not *pgxpool.Pool")
	}
	if cfg.Auth.SigningKey == "" {
		return nil, errors.New("app: auth.signing_key (or signing_key_secret) is required")
	}

	defaultName := cfg.Storage.DefaultBackend
	if defaultName == "" {
		return nil, errors.New("app: storage.default_backend not set")
	}
	// One client per backend, keyed by backend id (docs/backend-registry.md).
	// Warmup(false) eagerly builds only the default — identical to the single
	// New-at-boot it replaces; other backends build lazily on first use. The
	// default client still backs all five storage interfaces below; Step 3 of
	// the landing swaps these for the per-backend routers. Sourcing it through
	// the registry proves the registry on the boot path with zero behavioural
	// change on a single-backend config.
	registry := s3adapter.NewBackendRegistry(cfg.Storage)
	if err := registry.Warmup(ctx, false); err != nil {
		return nil, fmt.Errorf("app: s3 backend registry: %w", err)
	}
	s3c, err := registry.For(ctx, defaultName)
	if err != nil {
		return nil, fmt.Errorf("app: s3 adapter: %w", err)
	}

	repos := wire.Repos{
		Object:        adapters.NewObjectRepo(db.Queries, pool),
		ObjectKey:     adapters.NewObjectKeyRepo(db.Queries, pool),
		Bucket:        adapters.NewBucketRepo(db.Queries),
		Tenant:        adapters.NewTenantRepo(db.Queries, pool),
		ObjectTag:     adapters.NewObjectTagRepo(db.Queries),
		Presign:       adapters.NewPresignRepo(db.Queries, pool),
		Multipart:     adapters.NewMultipartRepo(db.Queries, pool),
		Operation:     adapters.NewOperationRepo(db.Queries, pool),
		BackendV2:     adapters.NewBackendRepoV2(db.Queries, pool),
		BucketV2:      adapters.NewBucketRepoV2(db.Queries, pool),
		Audit:         adapters.NewAuditRepoV2(db.Queries),
		Quota:         adapters.NewQuotaRepoV2(db.Queries, pool),
		EventSub:      adapters.NewEventSubscriptionRepoV2(db.Queries),
		IAMUser:       adapters.NewUserRepo(db.Queries),
		IAMApiKey:     adapters.NewApiKeyRepo(db.Queries),
		IAMRefresh:    adapters.NewRefreshTokenRepo(db.Queries),
		ObjectVersion: adapters.NewObjectVersionRepo(db.Queries),
		Idempotency:   adapters.NewIdempotencyRepo(db.Queries),
	}
	storage := wire.Storage{
		Object:      s3c,
		Multipart:   s3c,
		Presign:     s3c.Presign(),
		Stream:      s3c,
		Provisioner: s3c,
	}

	polStore := policy.NewPostgresStore(pool)
	polEngine := policy.NewEngine(polStore, cfg.Cedar.PolicyCacheTTL,
		policy.WithCanonicalObjectKeyEUID(cfg.Cedar.CanonicalObjectKeyEUID))
	if err := polEngine.Start(ctx); err != nil {
		return nil, fmt.Errorf("app: policy engine start: %w", err)
	}

	deps := &SharedDeps{
		Cfg:       cfg,
		Logger:    l,
		DB:        db,
		Pool:      pool,
		Repos:     repos,
		Storage:   storage,
		PolEngine: polEngine,
		PolStore:  polStore,
		SM:        statemachine.New(pool),
		CELEval:   cel.NewEvaluator(),
		S3:        s3c,
	}

	// Capability subsystem — additive; absence is fine. Built after the
	// rest so the bundle can take a *SharedDeps for logging convenience.
	cap, err := BuildCapabilityBundle(cfg.Capability, deps)
	if err != nil {
		return nil, fmt.Errorf("app: capability bundle: %w", err)
	}
	deps.Capability = cap

	// API-token subsystem — additive; same disabled-by-default rule.
	apiTok, err := BuildAPITokenBundle(cfg.APIToken, deps)
	if err != nil {
		return nil, fmt.Errorf("app: api_token bundle: %w", err)
	}
	deps.APIToken = apiTok

	// Audit writes are synchronous + crash-durable (ADR-0004): the audit
	// interceptor calls repos.Audit.Insert directly on the response path,
	// so the row is committed before the RPC returns. No in-memory buffer,
	// no background flusher, no loss window on an abrupt process kill.

	return deps, nil
}
