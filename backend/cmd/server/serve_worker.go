package main

import (
	"context"
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

	"github.com/oleg-tkachuk/paladin/internal/app"
	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/health"
	"github.com/oleg-tkachuk/paladin/internal/observability"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres"
	"github.com/oleg-tkachuk/paladin/internal/worker/lease"
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
