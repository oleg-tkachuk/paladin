package main

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/app"
)

// serveAdminCmd runs only the admin Connect listener. Sized for low
// replica counts (1–2) and intended to sit behind a separate ingress
// with mTLS and tighter NetworkPolicy than data/iam.
var serveAdminCmd = &cobra.Command{
	Use:   "admin",
	Short: "Run admin Connect listener",
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

		listener, healthH, err := app.BuildAdminListener(ctx, deps, meta)
		if err != nil {
			l.Fatal("failed to build admin listener", zap.Error(err))
		}

		runListeners(ctx, deps, []app.HTTPListener{listener}, healthH)
	},
}
