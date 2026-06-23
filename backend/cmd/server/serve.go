package main

import (
	"context"
	"os/signal"
	"sync/atomic"
	"syscall"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/app"
	"github.com/oleg-tkachuk/paladin/internal/health"
)

// serveCmd is the parent for every long-running PALADIN role. Children:
// `api`, `admin`, `worker`, `mcp`. The collapsed "all-in-one" mode that
// used to live in rootCmd is intentionally absent — Helm deploys one
// Deployment per child.
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run an PALADIN plane or worker",
}

func init() {
	serveCmd.AddCommand(serveAPICmd, serveAdminCmd, serveWorkerCmd, serveMCPCmd, serveIngestCmd, serveDispatcherCmd)
}

// runListeners is the shared run-loop for serve api / admin. It builds an
// App container with the supplied listeners and blocks until either ctx
// is cancelled or the container reports a fatal error. Workers use a
// dedicated runWorker function in serve_worker.go because their lifecycle
// is more involved (per-job lease wrapping).
func runListeners(
	ctx context.Context,
	deps *app.SharedDeps,
	listeners []app.HTTPListener,
	healthH *health.Handler,
) {
	cfg := deps.Cfg
	l := deps.Logger
	db := deps.DB

	started := &atomic.Bool{}
	// deps.BackgroundJobs carries listener-side goroutines. Empty today
	// (the audit writer is synchronous + durable — ADR-0004); kept wired
	// so future listener-scoped jobs flow through the same slot.
	container := app.NewContainer(version, commit, buildTime, cfg, l, listeners, db, nil, deps.BackgroundJobs, started).
		WithHealth(healthH)

	l.Info("starting",
		zap.String("version", version),
		zap.String("commit", commit),
		zap.String("build_time", buildTime),
	)

	runErr := make(chan error, 1)
	go func() { runErr <- container.Run() }()

	select {
	case <-ctx.Done():
		l.Info("shutdown signal received")
	case err := <-runErr:
		if err != nil {
			l.Error("server run failed", zap.Error(err))
		}
	}
	container.Shutdown()
}

// signalCtx wires SIGINT/SIGTERM into a parent context the subcommand can
// pass into builders and the run loop. Returned cancel must be deferred.
func signalCtx() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}
