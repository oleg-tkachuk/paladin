package main

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/app"
)

// serveAPICmd runs the data + iam Connect listeners. No workers.
//
// Two listeners share one *health.Handler so SIGTERM flips both /readyz
// to 503 simultaneously, giving kube-proxy a single window to drain
// in-flight traffic.
var serveAPICmd = &cobra.Command{
	Use:   "api",
	Short: "Run data + iam Connect listeners",
	Run: func(cmd *cobra.Command, args []string) {
		ctx, stop := signalCtx()
		defer stop()

		cfg, l, db, otelShutdown := boot(ctx)
		defer db.Close()
		defer flushOTel(otelShutdown)

		deps, err := app.BuildSharedDeps(ctx, cfg, db, l)
		if err != nil {
			l.Fatal("failed to build shared deps", zap.Error(err))
		}

		meta := app.BuildMeta{
			Version:    version,
			Commit:     commit,
			BuildTime:  buildTime,
			ConfigPath: configPath,
		}

		listeners, healthH, err := app.BuildAPIListeners(ctx, deps, meta)
		if err != nil {
			l.Fatal("failed to build api listeners", zap.Error(err))
		}

		runListeners(ctx, deps, listeners, healthH)
	},
}
