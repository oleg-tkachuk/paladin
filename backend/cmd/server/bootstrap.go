package main

import (
	"context"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	bootstrappkg "github.com/oleg-tkachuk/paladin/internal/bootstrap"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
)

// bootstrapCmd seeds the platform-admin user (when bootstrap.admin.enabled
// is true) and mirrors yaml `storage.backends.*` into the storage_backends
// table so subsequent BucketService.CreateBucket calls have a valid FK
// target.
//
// Both steps are idempotent — restarts don't churn rows when the YAML
// hasn't changed. Helm runs this as a Job after the migrate Job and
// before any serve Deployment becomes ready.
var bootstrapCmd = &cobra.Command{
	Use:   "bootstrap",
	Short: "Seed admin user and mirror storage backends into the DB",
	Run: func(cmd *cobra.Command, args []string) {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		cfg, l, db, otelShutdown := boot(ctx)
		defer func() { _ = db.Close }()
		defer flushOTel(otelShutdown)

		if err := bootstrappkg.EnsureAdmin(ctx, cfg.Bootstrap.Admin, bootstrappkg.Deps{
			Tenants: db.Queries,
			Users:   adapters.NewUserRepo(db.Queries),
			Audit:   adapters.NewAuditRepoV2(db.Queries),
			Logger:  l.Named("bootstrap"),
			Mode:    cfg.Runtime.Mode,
		}); err != nil {
			l.Fatal("bootstrap admin failed", zap.Error(err))
		}

		if err := bootstrappkg.EnsureBackends(ctx, cfg.Storage, bootstrappkg.BackendDeps{
			// nil pool: bootstrap only Upserts backends, never the RunInTx
			// tx seam, so the pool is never dereferenced on this path.
			Backends: adapters.NewBackendRepoV2(db.Queries, nil),
			Audit:    adapters.NewAuditRepoV2(db.Queries),
			Logger:   l.Named("bootstrap"),
		}); err != nil {
			l.Fatal("bootstrap backends failed", zap.Error(err))
		}

		l.Info("bootstrap complete")
	},
}
