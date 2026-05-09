package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/app"
	"github.com/oleg-tkachuk/paladin/internal/health"
	"github.com/oleg-tkachuk/paladin/internal/worker"
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
		ctx, stop := signalCtx()
		defer stop()

		cfg, l, db := boot(ctx)
		defer func() { _ = db.Close }()

		deps, err := app.BuildSharedDeps(ctx, cfg, db, l)
		if err != nil {
			l.Fatal("failed to build shared deps", zap.Error(err))
		}

		dispatcherPool := deps.Pool
		if cfg.Datastores.Postgres.MigrateDSN != "" {
			// Pass MigratePassword explicitly — the runtime path
			// (postgres.New for the deps.Pool) injects cfg.Password
			// into pgxpool.ConnConfig.Password after parse, since the
			// resolver populates the field from migrate_password_secret
			// at boot. The dispatcher's pool needs the same treatment
			// or pgx falls back to no-password and SASL fails with
			// 28P01. Symptom before this fix: outbox loop spammed
			// "failed SASL auth: FATAL: password authentication
			// failed for user paladin_migrate" every poll_interval, and
			// /readyz flipped to 503 because the outbox health check
			// also can't acquire a connection.
			pool, err := newDispatcherPool(
				ctx,
				cfg.Datastores.Postgres.MigrateDSN,
				cfg.Datastores.Postgres.MigratePassword,
				l,
			)
			if err != nil {
				l.Fatal("failed to open dispatcher pool", zap.Error(err))
			}
			dispatcherPool = pool
			defer pool.Close()
		} else {
			l.Warn("dispatcher: MigrateDSN not set; using runtime pool — " +
				"RLS will gate the outbox loop. Set datastores.postgres.migrate_dsn " +
				"to a BYPASSRLS role for production.")
		}

		// Subscription read-seam used by OutboxRunner per-row. The admin
		// repo's Get matches what we need; reuse via deps.Repos.EventSub.
		store := dispatcherSubStore{r: deps.Repos.EventSub}

		dispatcher := &worker.Dispatcher{
			Store:  store,
			Logger: l.Named("event-dispatcher"),
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
		opsMux, _ := dispatcherOpsMux(deps, runner, l)
		opsSrv := &http.Server{
			Addr:              opsAddr,
			ReadHeaderTimeout: 5 * time.Second,
			Handler:           opsMux,
		}
		go func() {
			l.Info("dispatcher ops listener", zap.String("addr", opsAddr))
			if err := opsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				l.Error("dispatcher ops listener exited", zap.Error(err))
			}
		}()

		runErr := make(chan error, 1)
		go func() { runErr <- runner.Run(ctx) }()

		select {
		case <-ctx.Done():
			l.Info("dispatcher shutdown signal received")
		case err := <-runErr:
			if err != nil && !errorsIsCancelled(err) {
				l.Error("outbox runner exited", zap.Error(err))
			}
		}

		shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownGrace)
		defer cancel()
		_ = opsSrv.Shutdown(shutdownCtx)
		// Wait for the runner to drain its in-flight batch (bounded by
		// per-delivery HTTP timeouts).
		<-runErr
	},
}

// newDispatcherPool opens a minimal pgxpool aimed at the dispatcher's
// outbox loop. Skips the RLS BeforeAcquire / AfterRelease hooks — the
// loop legitimately spans tenants and the MigrateDSN role is BYPASSRLS.
//
// `password` is the secret-resolved migrate password (populated at
// config-load time from migrate_password_secret). When non-empty it
// overrides whatever the DSN string carries — production deploys
// keep the DDL credential out of the YAML in a Kubernetes Secret,
// so the DSN never has the password embedded.
func newDispatcherPool(ctx context.Context, dsn, password string, l *zap.Logger) (*pgxpool.Pool, error) {
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
	cfg.ConnConfig.RuntimeParams["application_name"] = "paladin-dispatcher"
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = strconv.Itoa(int((10 * time.Second).Milliseconds()))
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("pgxpool init: %w", err)
	}
	l.Info("dispatcher pool initialized",
		zap.String("user", cfg.ConnConfig.User),
		zap.String("host", cfg.ConnConfig.Host),
	)
	return pool, nil
}

// dispatcherOpsMux assembles the dispatcher pod's ops surface. Same
// shape as the worker / api / admin planes. Subsystem check: outbox
// table reachable (a backlog is not a probe failure — operators
// route on the count metric instead).
func dispatcherOpsMux(deps *app.SharedDeps, runner *worker.OutboxRunner, l *zap.Logger) (http.Handler, *health.Handler) {
	healthH := app.NewHealthHandler(deps.DB, deps.Cfg.Runtime, l).WithRole("dispatcher")
	app.AddSubsystemCheck(healthH, "outbox", true, func(ctx context.Context) error {
		_, err := runner.PendingCount(ctx)
		return err
	})
	mux := http.NewServeMux()
	healthH.Register(mux)
	// Backwards-compat alias for chart probe paths that historically
	// hit /healthz on worker-class pods.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/livez"
		mux.ServeHTTP(w, r2)
	})
	return mux, healthH
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
