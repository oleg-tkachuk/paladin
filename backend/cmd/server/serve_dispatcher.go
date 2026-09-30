package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/spf13/cobra"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/app"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/health"
	"github.com/oleg-tkachuk/paladin/backend/internal/middleware"
	"github.com/oleg-tkachuk/paladin/backend/internal/observability"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// serveDispatcherCmd runs the durable webhook fan-out loop introduced
// by migration 028. Producer (admin pod) writes one event_deliveries
// row per matching subscription on each inbound event; this loop polls
// the outbox via FOR UPDATE SKIP LOCKED, posts to sinks, and updates
// status / attempts / next_attempt_at. Multiple replicas safe — the
// SKIP LOCKED clause hands each row to exactly one replica per cycle.
//
// Why a separate pod (vs. running this in `serve worker`):
//   - Worker pods own per-job leases — the dispatcher loop is one
//     across all rows, not one-per-tenant. Lease wrapping would just
//     cap concurrency to a single replica.
//   - The HTTP fan-out's failure modes (slow customers, transient 5xx,
//     DNS flaps) are different from the worker's DB-shaped jobs. Keep
//     them in their own resource budget so a noisy webhook doesn't
//     starve lifecycle / housekeeping.
//
// Ops listener on cfg.Dispatcher.Ops.Addr (defaults to :8099).
//
// Cross-tenant note: the outbox loop SELECTs across tenants — which
// the runtime DSN's paladin_app role cannot do under RLS. The dispatcher
// pod opens its pool from cfg.Datastores.Postgres.MigrateDSN when
// set (paladin_migrate, BYPASSRLS) and falls back to the runtime pool
// with a warning when unset (dev / test only — RLS will gate).
var serveDispatcherCmd = &cobra.Command{
	Use:   "dispatcher",
	Short: "Run the event-delivery outbox loop",
	Run: func(cmd *cobra.Command, args []string) {
		fx.New(
			fx.Supply(configSource()),
			fx.Supply(buildMeta()),
			dispatcherModule,
		).Run()
	},
}

// dispatcherModule is the dispatcher role's fx graph: the DB-backed BaseModule
// plus the outbox lifecycle. Extracted so both the command and the
// graph-validation test (fx_validate_test.go) reference the same wiring.
var dispatcherModule = fx.Options(
	app.BaseModule,
	fx.Invoke(runDispatcher),
)

// runDispatcher is the dispatcher role's fx lifecycle. It builds the outbox
// runner, its sink connection pools, and the ops listener up front (any failure
// aborts start, matching the pre-fx Fatal), then OnStart pre-warms the sink
// dials and spawns the runner; OnStop stops the ops listener, drains the
// in-flight batch, then closes the sink pools, the dispatcher's own DB pool,
// and the shared DB, and flushes OTel — the same teardown order the pre-fx
// serve dispatcher produced via LIFO defers.
func runDispatcher(
	lc fx.Lifecycle,
	cfg config.Config,
	l *zap.Logger,
	db *postgres.DB,
	otel observability.ShutdownFunc,
	deps *app.SharedDeps,
) error {
	dispatcherPool := deps.Pool
	var ownPool *pgxpool.Pool // non-nil only when we opened a dedicated BYPASSRLS pool
	// The dispatcher drains event_deliveries cross-tenant (no request principal
	// → no paladin.tenant_id GUC), so it needs BYPASSRLS. That's pure DML, so it
	// runs on the least-privilege paladin_reaper role (reaper_dsn); falls back to
	// paladin_migrate when reaper_dsn is unset (dev parity). See migration 058.
	if bypassDSN, bypassPwd := bypassRLSConn(cfg); bypassDSN != "" {
		// Pass the password explicitly — the runtime path (postgres.New for the
		// deps.Pool) injects cfg.Password into pgxpool.ConnConfig.Password after
		// parse (the resolver populates it from *_password_secret at boot). This
		// pool needs the same treatment or pgx falls back to no-password and
		// SASL fails with 28P01 — the outbox loop would spam "password
		// authentication failed" every poll_interval and /readyz would flip 503.
		pool, err := newDispatcherPool(
			context.Background(),
			bypassDSN,
			bypassPwd,
			"paladin-dispatcher",
			l,
		)
		if err != nil {
			return fmt.Errorf("open dispatcher pool: %w", err)
		}
		dispatcherPool = pool
		ownPool = pool
	} else {
		l.Warn("dispatcher: MigrateDSN not set; using runtime pool — " +
			"RLS will gate the outbox loop. Set datastores.postgres.migrate_dsn " +
			"to a BYPASSRLS role for production.")
	}

	// Subscription read-seam used by OutboxRunner per-row. It MUST bind to the
	// same dispatcherPool the drain loop scans on: that loop is cross-tenant
	// and sets no paladin.tenant_id GUC, so under a BYPASSRLS MigrateDSN the pool
	// sees every tenant's rows — but deps.Repos.EventSub is bound to the
	// runtime RLS pool (paladin_app), where a GUC-less Get returns zero rows and
	// the runner mis-reports every delivery as "subscription deleted". (The
	// runtime repo stays correct for the per-tenant admin handlers, which run
	// with the GUC set by middleware.)
	store := dispatcherSubStore{r: adapters.NewEventSubscriptionRepoV2(sqlc.New(dispatcherPool))}

	// One NATS connection pool shared by every NATS sink. Created
	// unconditionally — empty until the first nats-sink delivery
	// dials a server. Closed on shutdown so in-flight publishes
	// have a chance to flush.
	natsPool := worker.NewNatsConnPool(l.Named("nats-pool"))

	// SQS + RabbitMQ sink clients, same lazy contract as the NATS pool:
	// empty until the first sqs/rabbitmq-sink delivery dials. The SQS
	// pool holds stateless HTTP clients (no Close); the RabbitMQ pool
	// holds live AMQP connections, drained on shutdown.
	sqsPool := worker.NewSQSClientPool(l.Named("sqs-pool"))
	rabbitPool := worker.NewRabbitMQConnPool(l.Named("rabbitmq-pool"))
	kafkaPool := worker.NewKafkaWriterPool(l.Named("kafka-pool"))

	dispatcher := &worker.Dispatcher{
		Store:    store,
		NATS:     natsPool,
		SQS:      sqsPool,
		RabbitMQ: rabbitPool,
		Kafka:    kafkaPool,
		// Delivery-time resolver for "k8s:<name>/<key>" refs in sink
		// credential fields (HTTP HMAC, Kafka SASL/mTLS, NATS creds,
		// AMQP URL). Referenced Secret names must be in the pod's RBAC
		// secret allowlist.
		Secrets: app.NewSinkSecretResolver(l.Named("sink-secrets")),
		Logger:  l.Named("event-dispatcher"),
	}

	runner := &worker.OutboxRunner{
		Pool:               dispatcherPool,
		Dispatcher:         dispatcher,
		Logger:             l.Named("outbox-runner"),
		PollInterval:       cfg.Dispatcher.PollInterval,
		BatchSize:          cfg.Dispatcher.BatchSize,
		BaseBackoff:        cfg.Dispatcher.BaseBackoff,
		MaxBackoff:         cfg.Dispatcher.MaxBackoff,
		DefaultMaxAttempts: cfg.Dispatcher.DefaultMaxAttempts,
	}

	opsAddr := cfg.Dispatcher.Ops.Addr
	if opsAddr == "" {
		opsAddr = ":8099"
	}
	opsMux, _ := dispatcherOpsMux(deps, runner, natsPool, rabbitPool, l)
	opsSrv := &http.Server{
		Addr:              opsAddr,
		ReadHeaderTimeout: 5 * time.Second,
		Handler:           opsMux,
	}

	// workCtx bounds the outbox loop; cancelled OnStop so runner.Run returns.
	workCtx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)

	l.Info("starting dispatcher",
		zap.String("version", version),
		zap.String("commit", commit),
		zap.String("build_time", buildTime),
	)

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			// Pre-warm: scan event_subscriptions once at boot and dial each
			// unique sink URL. Drops first-delivery latency from a cold
			// TLS+SASL handshake to a queue-and-flush inside the hot tick
			// loop, and gives the health probe something to report before
			// any row hits the dispatcher. Errors are logged but never
			// fatal — per-row deliver retries the dial under its own budget.
			preWarmNATS(workCtx, dispatcherPool, natsPool, l)
			preWarmRabbitMQ(workCtx, dispatcherPool, rabbitPool, l)

			go func() {
				l.Info("dispatcher ops listener", zap.String("addr", opsAddr))
				if err := opsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					l.Error("dispatcher ops listener exited", zap.Error(err))
				}
			}()
			go func() { runErr <- runner.Run(workCtx) }()
			return nil
		},
		OnStop: func(context.Context) error {
			l.Info("dispatcher shutdown signal received")
			cancel() // stop the outbox loop
			shutdownCtx, c := context.WithTimeout(context.Background(), defaultShutdownGrace)
			defer c()
			_ = opsSrv.Shutdown(shutdownCtx)
			// Wait for the runner to drain its in-flight batch (bounded by
			// per-delivery HTTP timeouts).
			if err := <-runErr; err != nil && !errorsIsCancelled(err) {
				l.Error("outbox runner exited", zap.Error(err))
			}
			// Drain the sink pools (reverse of construction), then flush OTel
			// and close DB pools — matching the pre-fx LIFO defer order.
			kafkaPool.Close()
			rabbitPool.Close()
			natsPool.Close()
			if ownPool != nil {
				ownPool.Close()
			}
			// Bounded (5s) fresh-context OTel flush — the fx OnStop context
			// carries the 90s StopTimeout, so a slow/unreachable OTLP endpoint
			// would otherwise block teardown past the pod's termination grace
			// and get SIGKILLed mid-shutdown.
			flushOTel(otel)
			deps.StopWatchers() // release the Cedar LISTEN conn before pool close
			db.Close()
			_ = l.Sync()
			return nil
		},
	})
	return nil
}

// bypassRLSConn resolves the DSN + password for a cross-tenant BYPASSRLS
// plumbing pool (dispatcher outbox, ingest dedup). Prefers the dedicated
// least-privilege paladin_reaper role (reaper_dsn) — those loops are pure DML, so
// they don't need the DDL owner — and falls back to paladin_migrate when reaper_dsn
// is unset (dev parity). Returns ("","") when neither is configured, so the
// caller degrades to the RLS runtime pool. See migration 058 / serve_worker.
func bypassRLSConn(cfg config.Config) (dsn, password string) {
	if cfg.Datastores.Postgres.ReaperDSN != "" {
		return cfg.Datastores.Postgres.ReaperDSN, cfg.Datastores.Postgres.ReaperPassword
	}
	return cfg.Datastores.Postgres.MigrateDSN, cfg.Datastores.Postgres.MigratePassword
}

// newDispatcherPool opens a minimal pgxpool aimed at the dispatcher's
// outbox loop. Skips the RLS PrepareConn / AfterRelease hooks — the
// loop legitimately spans tenants and the pool's role is BYPASSRLS.
//
// `password` is the secret-resolved migrate password (populated at
// config-load time from migrate_password_secret). When non-empty it
// overrides whatever the DSN string carries — production deploys
// keep the DDL credential out of the YAML in a Kubernetes Secret,
// so the DSN never has the password embedded.
func newDispatcherPool(ctx context.Context, dsn, password, appName string, l *zap.Logger) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse migrate_dsn: %w", err)
	}
	if password != "" {
		cfg.ConnConfig.Password = password
	}
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["application_name"] = appName
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = strconv.Itoa(int((10 * time.Second).Milliseconds()))
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("pgxpool init: %w", err)
	}
	l.Info("BYPASSRLS pool initialized",
		zap.String("application_name", appName),
		zap.String("user", cfg.ConnConfig.User),
		zap.String("host", cfg.ConnConfig.Host),
	)
	return pool, nil
}

// dispatcherOpsMux assembles the dispatcher pod's ops surface. Same
// shape as the worker / api / admin planes. Subsystem checks:
//   - outbox: table reachable (a backlog is not a probe failure —
//     operators route on the count metric instead).
//   - nats:   for every URL the pool has dialed, at least one server
//     in the pool reports CONNECTED. Subsystem is registered as
//     non-required so a temporary NATS outage flips the JSON to
//     degraded but does NOT take /readyz to 503 — the rest of the
//     pod (HTTP delivery, outbox writes) is still healthy.
func dispatcherOpsMux(deps *app.SharedDeps, runner *worker.OutboxRunner, natsPool *worker.NatsConnPool, rabbitPool *worker.RabbitMQConnPool, l *zap.Logger) (http.Handler, *health.Handler) {
	healthH := app.NewHealthHandler(deps.DB, deps.Cfg.Runtime, l).WithRole("dispatcher")
	app.AddSubsystemCheck(healthH, "outbox", true, func(ctx context.Context) error {
		_, err := runner.PendingCount(ctx)
		return err
	})
	app.AddSubsystemCheck(healthH, "nats", false, func(ctx context.Context) error {
		st := natsPool.Statuses()
		if len(st) == 0 {
			// No NATS subs configured / pool not warmed. Treat as
			// healthy-but-empty rather than failing the probe.
			return nil
		}
		for url, status := range st {
			if status != nats.CONNECTED {
				return fmt.Errorf("nats %s: status=%s", url, status)
			}
		}
		return nil
	})
	// Non-critical, mirrors the nats check: a dropped/closed RabbitMQ
	// connection the dispatcher was using fails the probe (surfaced on
	// /system/health.json) without gating readiness. Empty pool (no
	// rabbitmq sub dialed / warmed) → healthy-but-empty.
	app.AddSubsystemCheck(healthH, "rabbitmq", false, func(ctx context.Context) error {
		for url, healthy := range rabbitPool.Statuses() {
			if !healthy {
				return fmt.Errorf("rabbitmq %s: connection unhealthy", url)
			}
		}
		return nil
	})
	mux := http.NewServeMux()
	healthH.Register(mux)

	// /metrics beside the health endpoints: this ops listener is already plain
	// HTTP and cluster-internal, which is what the scraper needs.
	if h := metricsHandler(deps); h != nil {
		mux.Handle(middleware.PathMetrics, h)
	}

	// Backwards-compat alias for chart probe paths that historically
	// hit /healthz on worker-class pods.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/livez"
		mux.ServeHTTP(w, r2)
	})
	// Operator view: global queue depth + per-subscription stuck-work
	// breakdown. Computed HERE (not in the admin pod) because the
	// dispatcher's pool is the BYPASSRLS one — event_deliveries is RLS'd
	// per tenant and this view is deliberately cross-tenant. The admin
	// plane's SystemService.GetDispatcherStats proxies this endpoint after
	// its own platform-admin gate; the ops listener itself is cluster-
	// internal only (same trust posture as /system/health.json).
	mux.HandleFunc("GET /system/dispatcher-stats.json", func(w http.ResponseWriter, r *http.Request) {
		stats, err := runner.DeliveryStats(r.Context())
		if err != nil {
			l.Warn("dispatcher stats failed", zap.Error(err))
			http.Error(w, "stats unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(stats)
	})
	return mux, healthH
}

// preWarmNATS scans every nats-sink subscription once at boot and
// dials the pool for each unique (url, credentials_ref) pair. Best
// effort — failures are logged and the dispatcher continues; the
// per-row deliver path will retry the dial under the row's normal
// retry budget. Cross-tenant SELECT is safe here: the dispatcher
// pod's pool already runs as the BYPASSRLS migrate role.
func preWarmNATS(ctx context.Context, pool *pgxpool.Pool, natsPool *worker.NatsConnPool, l *zap.Logger) {
	rows, err := pool.Query(ctx,
		`SELECT sink_config FROM event_subscriptions
		  WHERE sink_kind = 'nats' AND disabled = false`)
	if err != nil {
		l.Warn("nats pre-warm: scan failed", zap.Error(err))
		return
	}
	defer rows.Close()
	seen := make(map[string]worker.NatsTarget)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			l.Warn("nats pre-warm: row scan failed", zap.Error(err))
			continue
		}
		var cfg struct {
			URL            string `json:"url"`
			CredentialsRef string `json:"credentials_ref"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			continue
		}
		if cfg.URL == "" {
			continue
		}
		key := cfg.URL + "\x00" + cfg.CredentialsRef
		seen[key] = worker.NatsTarget{URL: cfg.URL, CredentialsRef: cfg.CredentialsRef}
	}
	targets := make([]worker.NatsTarget, 0, len(seen))
	for _, t := range seen {
		targets = append(targets, t)
	}
	if len(targets) == 0 {
		l.Info("nats pre-warm: no nats-sink subscriptions configured")
		return
	}
	l.Info("nats pre-warm: dialing servers", zap.Int("targets", len(targets)))
	natsPool.Warmup(targets)
}

// preWarmRabbitMQ scans every rabbitmq-sink subscription once at boot and
// dials the pool for each unique broker URL, so the /system/health.json
// "rabbitmq" check reports on configured brokers before the first delivery.
// Best-effort, mirroring preWarmNATS: dial failures are logged, never fatal.
func preWarmRabbitMQ(ctx context.Context, pool *pgxpool.Pool, rabbitPool *worker.RabbitMQConnPool, l *zap.Logger) {
	rows, err := pool.Query(ctx,
		`SELECT sink_config FROM event_subscriptions
		  WHERE sink_kind = 'rabbitmq' AND disabled = false`)
	if err != nil {
		l.Warn("rabbitmq pre-warm: scan failed", zap.Error(err))
		return
	}
	defer rows.Close()
	seen := make(map[string]struct{})
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			l.Warn("rabbitmq pre-warm: row scan failed", zap.Error(err))
			continue
		}
		var cfg struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil || cfg.URL == "" {
			continue
		}
		seen[cfg.URL] = struct{}{}
	}
	if len(seen) == 0 {
		l.Info("rabbitmq pre-warm: no rabbitmq-sink subscriptions configured")
		return
	}
	urls := make([]string, 0, len(seen))
	for u := range seen {
		urls = append(urls, u)
	}
	l.Info("rabbitmq pre-warm: dialing brokers", zap.Int("targets", len(urls)))
	rabbitPool.Warmup(urls)
}

// dispatcherSubStore satisfies worker.SubscriptionStore over the admin
// repository. The dispatcher only needs Get; List exists for interface
// completeness — the consumer-side path never paginates.
type dispatcherSubStore struct {
	r admindomain.EventSubscriptionRepository
}

func (s dispatcherSubStore) List(ctx context.Context, args admindomain.ListEventSubscriptionsArgs) ([]admindomain.EventSubscription, string, error) {
	return s.r.List(ctx, args)
}

func (s dispatcherSubStore) Get(ctx context.Context, id uuid.UUID) (admindomain.EventSubscription, error) {
	return s.r.Get(ctx, id)
}
