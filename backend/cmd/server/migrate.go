package main

import (
	"context"
	"errors"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/migrations"
)

// migrateCmd applies the embedded SQL migration set against the configured
// Postgres. Helm runs it as a Job pre-install / pre-upgrade so live api/
// worker pods never run DDL — they only ever see a schema that's already
// at the desired version.
//
// Idempotent: goose tracks applied versions; reruns are no-ops once the
// schema is up to date. RunMigrationsWith uses cfg.Datastores.Postgres
// migrate_dsn when set (DDL-capable role) and the runtime DSN otherwise.
var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Apply database migrations",
	Run: func(cmd *cobra.Command, args []string) {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		cfg, l, db, otelShutdown := boot(ctx)
		defer db.Close()
		defer flushOTel(otelShutdown)

		if err := db.RunMigrationsWith(ctx, migrations.FS, cfg.Datastores.Postgres); err != nil && !errors.Is(err, context.Canceled) {
			l.Fatal("failed to apply migrations", zap.Error(err))
		}
		l.Info("migrations applied")
	},
}
