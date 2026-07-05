package app

import (
	"context"
	"errors"
	"fmt"
	"sync"

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
	Cfg    config.Config
	Logger *zap.Logger
	DB     *postgres.DB
	Pool   *pgxpool.Pool
	// ReaperPool is an optional BYPASSRLS pool for the worker's cross-tenant
	// background DML jobs (least-privilege paladin_reaper in prod; paladin_migrate in
	// dev). Nil on every pod except the worker, which opens it in serve_worker;
	// nil → BuildBackgroundJobs degrades to Pool. The reapers MUST NOT use the
	// RLS-scoped Pool: with no per-request tenant GUC, RLS returns zero rows and
	// every reaper silently no-ops.
	ReaperPool *pgxpool.Pool
	// PartitionPool is the BYPASSRLS pool for the one background job that needs
	// DDL — PartitionMaintainer (CREATE/ATTACH/DROP PARTITION), which requires
	// the migrate role's table ownership that ReaperPool deliberately lacks.
	// Nil outside the worker; nil → BuildBackgroundJobs falls back to ReaperPool
	// then Pool. See migration 058 / serve_worker for the privilege split.
	PartitionPool *pgxpool.Pool
	Repos         wire.Repos
	Storage       wire.Storage

	// Engines built once, shared across handlers.
	PolEngine *policy.Engine
	PolStore  *policy.PostgresStore
	SM        *statemachine.Transitioner
	CELEval   *cel.Evaluator

	// Registry hands out one *s3adapter.Client per storage backend
	// (docs/backend-registry.md). The wire.Storage routers dispatch through
	// it, and any consumer that needs a backend-routed storage view builds
	// one via s3adapter.New*Router(deps.Registry).
	Registry *s3adapter.BackendRegistry

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

	// watcherMu guards watcherStops.
	watcherMu sync.Mutex
	// watcherStops holds the cancel for every long-lived background watcher
	// that holds a pooled connection for the process lifetime — the Cedar
	// policy engine's LISTEN loop (registered here) and the admin plane's
	// audit-stream LISTEN hub (registered by AssembleAdminMux). They MUST all
	// be stopped before db.Close(): pgxpool.Close() blocks until every acquired
	// connection is released, so any still-running watcher would deadlock
	// shutdown until the pod is SIGKILLed. See RegisterWatcherStop/StopWatchers.
	watcherStops []func()
}

// RegisterWatcherStop records a cancel for a background watcher that holds a
// pooled connection, to be run by StopWatchers before the pool is closed.
// Nil-safe; call at construction time.
func (d *SharedDeps) RegisterWatcherStop(cancel func()) {
	if d == nil || cancel == nil {
		return
	}
	d.watcherMu.Lock()
	d.watcherStops = append(d.watcherStops, cancel)
	d.watcherMu.Unlock()
}

// StopWatchers cancels every registered background watcher (Cedar LISTEN loop,
// admin audit-stream hub, …) so their pooled connections are released.
// Idempotent and nil-safe. Every shutdown path MUST call this before closing
// the DB pool — see the watcherStops field.
func (d *SharedDeps) StopWatchers() {
	if d == nil {
		return
	}
	d.watcherMu.Lock()
	stops := d.watcherStops
	d.watcherStops = nil
	d.watcherMu.Unlock()
	for _, cancel := range stops {
		cancel()
	}
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

	// One client per backend, keyed by backend id (docs/backend-registry.md).
	// Warmup eagerly builds every configured backend so a boot misconfiguration
	// fails loudly. Each storage interface below is a router that resolves the
	// target client from the registry per call, keyed on the object's backend
	// id — there is no default backend, so every write path names one explicitly.
	registry := s3adapter.NewBackendRegistry(cfg.Storage)
	if err := registry.Warmup(ctx); err != nil {
		return nil, fmt.Errorf("app: s3 backend registry: %w", err)
	}

	repos := wire.Repos{
		Object:    adapters.NewObjectRepo(db.Queries, pool),
		ObjectKey: adapters.NewObjectKeyRepo(db.Queries, pool),
		Bucket:    adapters.NewBucketRepo(db.Queries),
		Tenant:    adapters.NewTenantRepo(db.Queries, pool),
		ObjectTag: adapters.NewObjectTagRepo(db.Queries),
		Presign:   adapters.NewPresignRepo(db.Queries, pool),
		Multipart: adapters.NewMultipartRepo(db.Queries, pool),
		Operation: adapters.NewOperationRepo(db.Queries, pool),
		BackendV2: adapters.NewBackendRepoV2(db.Queries, pool),
		BucketV2:  adapters.NewBucketRepoV2(db.Queries, pool),
		// pool wired so the audit interceptor's InsertWithOutbox can open a
		// tx that commits the audit row + its mirror event atomically.
		Audit:         adapters.NewAuditRepoV2(db.Queries, pool),
		Quota:         adapters.NewQuotaRepoV2(db.Queries, pool),
		EventSub:      adapters.NewEventSubscriptionRepoV2(db.Queries),
		IAMUser:       adapters.NewUserRepo(db.Queries),
		IAMApiKey:     adapters.NewApiKeyRepo(db.Queries),
		IAMRefresh:    adapters.NewRefreshTokenRepo(db.Queries),
		ObjectVersion: adapters.NewObjectVersionRepo(db.Queries),
		Idempotency:   adapters.NewIdempotencyRepo(db.Queries),
	}
	storage := wire.Storage{
		Object:      s3adapter.NewObjectRouter(registry),
		Multipart:   s3adapter.NewMultipartRouter(registry),
		Presign:     s3adapter.NewPresignRouter(registry),
		Stream:      s3adapter.NewStreamRouter(registry),
		Provisioner: s3adapter.NewProvisionerRouter(registry),
	}

	polStore := policy.NewPostgresStore(pool)
	polEngine := policy.NewEngine(polStore, cfg.Cedar.PolicyCacheTTL,
		policy.WithCanonicalObjectKeyEUID(cfg.Cedar.CanonicalObjectKeyEUID),
		policy.WithLogger(l.Named("cedar")))
	// The engine's LISTEN watcher is a process-lifetime goroutine that holds a
	// pooled connection until its context is cancelled — so it runs on a
	// dedicated background context, NOT the construction `ctx` (which may carry
	// a timeout and, under fx, is context.Background() anyway). StopWatchers
	// cancels it at shutdown, before the pool is closed. Without this the
	// watcher's held connection deadlocks pgxpool.Close() until SIGKILL.
	watchCtx, cancelWatchers := context.WithCancel(context.Background())
	if err := polEngine.Start(watchCtx); err != nil {
		cancelWatchers()
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
		Registry:  registry,
	}
	deps.RegisterWatcherStop(cancelWatchers)

	// Capability subsystem — additive; absence is fine. Built after the
	// rest so the bundle can take a *SharedDeps for logging convenience.
	cap, err := BuildCapabilityBundle(cfg.Capability, deps)
	if err != nil {
		deps.StopWatchers() // don't leak the LISTEN goroutine on a failed boot
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
