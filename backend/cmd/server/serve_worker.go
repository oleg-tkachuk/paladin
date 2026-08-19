package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin-private/internal/app"
	"github.com/oleg-tkachuk/paladin-private/internal/config"
	"github.com/oleg-tkachuk/paladin-private/internal/health"
	"github.com/oleg-tkachuk/paladin-private/internal/observability"
	"github.com/oleg-tkachuk/paladin-private/internal/platformstats"
	"github.com/oleg-tkachuk/paladin-private/internal/store/postgres"
	"github.com/oleg-tkachuk/paladin-private/internal/worker/lease"
)

// serveWorkerCmd runs the background-job fan with per-job leader election via
// internal/worker/lease, on Uber fx. Two replicas with anti-affinity → each
// lease is held by exactly one pod at a time; the loser sleeps and races to
// claim on the winner's death (or stuck-process renew failure).
//
// An ops listener on cfg.Worker.Ops.Addr (defaults to :8099) exposes
// /healthz and /readyz so kube-proxy keeps the pod in its endpoint slice until
// SIGTERM. We do NOT reuse the data / iam handlers here — workers don't speak
// Connect, so the worker role composes app.BaseModule with its own fx
// lifecycle rather than the *App container.
var serveWorkerCmd = &cobra.Command{
	Use:   "worker",
	Short: "Run background workers with co-operative leader election",
	Run: func(cmd *cobra.Command, args []string) {
		fx.New(
			fx.Supply(configSource()),
			fx.Supply(buildMeta()),
			workerModule,
		).Run()
	},
}

// workerModule is the worker role's fx graph: the DB-backed BaseModule plus the
// worker lifecycle. Extracted so both the command and the graph-validation test
// (fx_validate_test.go) reference the exact same wiring.
var workerModule = fx.Options(
	app.BaseModule,
	fx.Invoke(runWorker),
)

// runWorker is the worker role's fx lifecycle. It builds one lease per
// background job up front (a build failure aborts start, matching the pre-fx
// Fatal), then OnStart spawns the lease-wrapped job goroutines plus the ops
// listener, and OnStop drains them, closes the DB, and flushes OTel — the same
// teardown order the pre-fx serve worker did via defers.
func runWorker(
	lc fx.Lifecycle,
	cfg config.Config,
	l *zap.Logger,
	db *postgres.DB,
	otel observability.ShutdownFunc,
	deps *app.SharedDeps,
) error {
	// Background jobs are cross-tenant with no request principal, so they must
	// bypass RLS — a query on the RLS runtime pool without a paladin.tenant_id GUC
	// returns ZERO rows and every job silently no-ops. Two BYPASSRLS pools,
	// split by privilege (migration 058):
	//
	//   - PartitionPool (paladin_migrate): the ONE background job that needs DDL —
	//     PartitionMaintainer (CREATE/ATTACH/DROP PARTITION) — runs here; the
	//     migrate role owns the partitioned tables.
	//   - ReaperPool (paladin_reaper): every other, DML-only job runs here, on a
	//     least-privilege role that cannot touch the schema.
	//
	// When reaper_dsn is unset the reaper jobs reuse the migrate pool object
	// (dev parity — one BYPASSRLS role for everything, no redundant pool).
	migrateDSN := cfg.Datastores.Postgres.MigrateDSN
	if migrateDSN != "" {
		pp, err := newDispatcherPool(context.Background(), migrateDSN,
			cfg.Datastores.Postgres.MigratePassword, "paladin-worker-ddl", l)
		if err != nil {
			return fmt.Errorf("open partition (migrate) pool: %w", err)
		}
		deps.PartitionPool = pp
	}

	reaperDSN := cfg.Datastores.Postgres.ReaperDSN
	switch {
	case reaperDSN != "" && reaperDSN != migrateDSN:
		// Dedicated least-privilege reaper role.
		rp, err := newDispatcherPool(context.Background(), reaperDSN,
			cfg.Datastores.Postgres.ReaperPassword, "paladin-reaper", l)
		if err != nil {
			return fmt.Errorf("open reaper pool: %w", err)
		}
		deps.ReaperPool = rp
	case deps.PartitionPool != nil:
		// No distinct reaper role → share the migrate BYPASSRLS pool. Aliased,
		// not re-opened; OnStop closes it once (guarded below).
		deps.ReaperPool = deps.PartitionPool
	default:
		l.Warn("worker: neither reaper_dsn nor migrate_dsn is set; background " +
			"jobs fall back to the RLS runtime pool and will find ZERO rows " +
			"(no tenant GUC). Set datastores.postgres.migrate_dsn to a BYPASSRLS role.")
	}

	jobs := app.BuildBackgroundJobs(deps)
	if len(jobs) == 0 {
		l.Warn("no background jobs configured; worker pod will idle")
	}

	holderID := uuid.New()
	holderMeta := map[string]string{
		"hostname": hostname(),
		"version":  version,
		"commit":   commit,
	}

	// leaseJob pairs a job with its lease so OnStart can spawn without
	// re-deriving names or re-building leases.
	type leaseJob struct {
		job   app.BackgroundJob
		lease *lease.Lease
		name  string
	}
	leaseJobs := make([]leaseJob, 0, len(jobs))
	for i, job := range jobs {
		name := jobLeaseName(job, i)
		ll, err := lease.New(deps.Pool, lease.Config{
			Name:       name,
			HolderID:   holderID,
			HolderMeta: holderMeta,
			Logger:     l.Named("lease." + name),
		})
		if err != nil {
			return fmt.Errorf("build lease %q: %w", name, err)
		}
		leaseJobs = append(leaseJobs, leaseJob{job: job, lease: ll, name: name})
	}

	opsAddr := cfg.Worker.Ops.Addr
	if opsAddr == "" {
		opsAddr = ":8099"
	}
	opsMux, opsHealth := workerOpsMux(cfg.Runtime, deps, l)
	_ = opsHealth // exported for future subsystem-check registration
	opsSrv := &http.Server{
		Addr:              opsAddr,
		ReadHeaderTimeout: 5 * time.Second,
		Handler:           opsMux,
	}

	// workCtx bounds the lease goroutines; cancelled OnStop so lease.Run
	// returns and releases leadership.
	workCtx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	l.Info("starting worker",
		zap.String("version", version),
		zap.String("commit", commit),
		zap.Int("jobs", len(leaseJobs)),
	)

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			for _, lj := range leaseJobs {
				wg.Add(1)
				go func(lj leaseJob) {
					defer wg.Done()
					err := lj.lease.Run(workCtx, func(runCtx context.Context, generation int64) error {
						l.Info("running job under lease",
							zap.String("name", lj.name),
							zap.Int64("generation", generation),
						)
						return lj.job.Run(runCtx)
					})
					if err != nil && !errorsIsCancelled(err) {
						l.Error("worker exited", zap.String("name", lj.name), zap.Error(err))
					}
				}(lj)
			}
			go func() {
				l.Info("worker ops listener", zap.String("addr", opsAddr))
				if err := opsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					l.Error("worker ops listener exited", zap.Error(err))
				}
			}()
			return nil
		},
		OnStop: func(context.Context) error {
			l.Info("worker shutdown signal received")
			cancel() // release leases; lease.Run returns
			shutdownCtx, c := context.WithTimeout(context.Background(), defaultShutdownGrace)
			defer c()
			_ = opsSrv.Shutdown(shutdownCtx)
			wg.Wait()
			deps.StopWatchers() // release the Cedar LISTEN conn before pool close
			// Close ReaperPool only when it's a distinct pool — in the dev
			// fallback it's aliased to PartitionPool, closed just below.
			if deps.ReaperPool != nil && deps.ReaperPool != deps.PartitionPool {
				deps.ReaperPool.Close() // BYPASSRLS paladin_reaper DML pool
			}
			if deps.PartitionPool != nil {
				deps.PartitionPool.Close() // BYPASSRLS paladin_migrate DDL pool
			}
			db.Close()
			// flushOTel uses a fresh, bounded (5s) context — the fx OnStop
			// context carries the 90s StopTimeout, and a slow/unreachable OTLP
			// endpoint would otherwise block the whole teardown past the pod's
			// termination grace and get SIGKILLed mid-shutdown.
			flushOTel(otel)
			_ = l.Sync()
			return nil
		},
	})
	return nil
}

// jobLeaseName derives a stable lease name from the job's concrete type. Same
// logical worker class must produce the same name across releases or upgrades
// will see two leases racing during rolling restart.
func jobLeaseName(j app.BackgroundJob, idx int) string {
	t := fmt.Sprintf("%T", j)
	for i := len(t) - 1; i >= 0; i-- {
		if t[i] == '.' {
			t = t[i+1:]
			break
		}
	}
	if t == "" {
		t = fmt.Sprintf("job-%d", idx)
	}
	return "paladin." + t
}

// workerOpsMux assembles the worker pod's ops surface — /livez, /readyz,
// /startupz, /system/health.json — off a real *health.Handler so the
// per-component contract matches every other role. Worker probe set: Postgres
// only; lease state is surfaced through metrics, not /readyz.
func workerOpsMux(cfg config.Runtime, deps *app.SharedDeps, l *zap.Logger) (http.Handler, *health.Handler) {
	healthH := app.NewHealthHandler(deps.DB, cfg, l).WithRole("worker")
	mux := http.NewServeMux()
	healthH.Register(mux)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/livez"
		mux.ServeHTTP(w, r2)
	})
	// Cross-tenant census of every RLS'd table (objects, quotas,
	// capability records, API tokens, event subscriptions) for the
	// console's /stats page. Computed HERE, not in the admin pod, because
	// migration 023 puts those tables behind row-level security and only
	// this pod holds a BYPASSRLS pool. The admin plane's
	// SystemService.GetPlatformStats proxies this endpoint behind its
	// platform-admin gate; the ops listener itself stays cluster-internal,
	// same trust posture as /system/health.json. Mirrors the dispatcher's
	// /system/dispatcher-stats.json.
	mux.HandleFunc("GET /system/rls-census.json", func(w http.ResponseWriter, r *http.Request) {
		// No BYPASSRLS pool means the deployment never set migrate_dsn /
		// reaper_dsn. Answering off deps.Pool would return zero rows (no
		// tenant GUC) and read as "the fleet is empty" — a 503 makes the
		// console say "unavailable" instead of lying.
		if deps.ReaperPool == nil {
			l.Warn("RLS census requested but no BYPASSRLS pool is configured")
			http.Error(w, "no bypassrls pool", http.StatusServiceUnavailable)
			return
		}
		census, err := platformstats.CollectRLS(r.Context(), deps.ReaperPool)
		if err != nil {
			l.Warn("RLS census failed", zap.Error(err))
			http.Error(w, "census unavailable", http.StatusInternalServerError)
			return
		}
		census.CollectedAt = time.Now().UTC().Format(time.RFC3339)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(census)
	})
	return mux, healthH
}

func hostname() string {
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "unknown"
}

// errorsIsCancelled centralises the canonical context-cancellation classes
// worker code legitimately ignores.
func errorsIsCancelled(err error) bool {
	if err == nil {
		return true
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
