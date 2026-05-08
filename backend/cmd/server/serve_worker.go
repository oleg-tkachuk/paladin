package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/app"
	"github.com/oleg-tkachuk/paladin/internal/worker/lease"
)

// serveWorkerCmd runs the background-job fan with per-job leader election
// via internal/worker/lease. Two replicas with anti-affinity → each lease
// is held by exactly one pod at a time; the loser sleeps and races to
// claim on the winner's death (or stuck-process renew failure).
//
// An ops listener on cfg.Server.AdminHTTP.Addr (when set; otherwise a
// fixed default) exposes /healthz and /readyz so kube-proxy keeps the
// pod in its endpoint slice until SIGTERM. We do NOT reuse the data /
// iam handlers here — workers don't speak Connect.
var serveWorkerCmd = &cobra.Command{
	Use:   "worker",
	Short: "Run background workers with co-operative leader election",
	Run: func(cmd *cobra.Command, args []string) {
		ctx, stop := signalCtx()
		defer stop()

		cfg, l, db := boot(ctx)
		defer func() { _ = db.Close }()

		deps, err := app.BuildSharedDeps(ctx, cfg, db, l)
		if err != nil {
			l.Fatal("failed to build shared deps", zap.Error(err))
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

		// Spawn one lease + goroutine per job. The lease's Run blocks
		// until the parent ctx is cancelled, retrying claims if it
		// loses leadership transiently.
		var wg sync.WaitGroup
		for i, job := range jobs {
			name := jobLeaseName(job, i)
			ll, err := lease.New(deps.Pool, lease.Config{
				Name:       name,
				HolderID:   holderID,
				HolderMeta: holderMeta,
				Logger:     l.Named("lease." + name),
			})
			if err != nil {
				l.Fatal("failed to build lease", zap.String("name", name), zap.Error(err))
			}
			wg.Add(1)
			go func(j app.BackgroundJob, ll *lease.Lease, name string) {
				defer wg.Done()
				err := ll.Run(ctx, func(workCtx context.Context, generation int64) error {
					l.Info("running job under lease",
						zap.String("name", name),
						zap.Int64("generation", generation),
					)
					return j.Run(workCtx)
				})
				if err != nil && !errorsIsCancelled(err) {
					l.Error("worker exited", zap.String("name", name), zap.Error(err))
				}
			}(job, ll, name)
		}

		// Ops listener: separate from data/admin/iam. Bind to the worker
		// ops port — the chart's worker Deployment exposes it as a named
		// port for kube-proxy probes. Default 8099 keeps it out of the
		// way of the Connect listeners.
		opsAddr := cfg.Server.AdminHTTP.Addr
		if opsAddr == "" {
			opsAddr = ":8099"
		}
		opsSrv := &http.Server{
			Addr:              opsAddr,
			ReadHeaderTimeout: 5 * time.Second,
			Handler:           workerOpsMux(deps),
		}
		go func() {
			l.Info("worker ops listener", zap.String("addr", opsAddr))
			if err := opsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				l.Error("worker ops listener exited", zap.Error(err))
			}
		}()

		<-ctx.Done()
		l.Info("worker shutdown signal received")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownGrace)
		defer cancel()
		_ = opsSrv.Shutdown(shutdownCtx)
		wg.Wait()
	},
}

// jobLeaseName derives a stable lease name from the job's concrete type.
// Same logical worker class must produce the same name across releases or
// upgrades will see two leases racing during rolling restart. Using the
// type name (via fmt %T) is good enough for the small set of worker types
// the BACKLOG keeps explicit.
func jobLeaseName(j app.BackgroundJob, idx int) string {
	t := fmt.Sprintf("%T", j)
	// Sanitise: strip the leading package path; keep "*worker.X" → "X".
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

// workerOpsMux assembles the minimal ops surface the worker pod exposes:
// /healthz (liveness) and /readyz (we report ready iff the DB ping
// succeeds; lease state is a separate axis the worker keeps trying on).
func workerOpsMux(deps *app.SharedDeps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := deps.DB.Ping(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("db: " + err.Error()))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

func hostname() string {
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "unknown"
}

// errorsIsCancelled wraps the canonical context cancellation errors. We
// avoid pulling in errors.Is at every call site for readability — the
// helper inlines the two classes worker code legitimately ignores.
func errorsIsCancelled(err error) bool {
	if err == nil {
		return true
	}
	return err == context.Canceled || err == context.DeadlineExceeded
}
