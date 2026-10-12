package main

import (
	"context"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/backendh"
	"github.com/oleg-tkachuk/paladin/backend/internal/app"
	bootstrappkg "github.com/oleg-tkachuk/paladin/backend/internal/bootstrap"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
)

// bootstrapCmd seeds the platform-admin user (when bootstrap.admin.enabled
// is true) and mirrors yaml `storage.backends.*` into the storage_backends
// table so subsequent BucketService.CreateBucket calls have a valid FK
// target, probing each backend that has never been probed (ADR-0026).
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
		defer db.Close()
		defer flushOTel(otelShutdown)

		if err := bootstrappkg.EnsureAdmin(ctx, cfg.Bootstrap.Admin, bootstrappkg.Deps{
			Tenants: db.Queries,
			Users:   adapters.NewUserRepo(db.Queries),
			// nil pool: bootstrap only Insert-s audit rows (autocommit),
			// never InsertWithOutbox.
			Audit:  adapters.NewAuditRepoV2(db.Queries, nil),
			Logger: l.Named("bootstrap"),
			Mode:   cfg.Runtime.Mode,
		}); err != nil {
			l.Fatal("bootstrap admin failed", zap.Error(err))
		}

		// The pool backs RunInTx, which recording a feature probe uses.
		pool, ok := db.Pool.(*pgxpool.Pool)
		if !ok {
			l.Fatal("bootstrap backends failed: DB.Pool is not *pgxpool.Pool")
		}
		// The same prober the admin plane's TestBackend uses. A backend it
		// cannot build a client for is a config error serve admin reports;
		// here it only means no probe, never a failed bootstrap.
		var prober backendh.BackendProber
		if p, err := app.BuildBackendProber(ctx, cfg.Storage, l); err != nil {
			l.Warn("backend prober unavailable, skipping the feature probe", zap.Error(err))
		} else {
			prober = p
		}
		if err := bootstrappkg.EnsureBackends(ctx, cfg.Storage, bootstrappkg.BackendDeps{
			Backends: adapters.NewBackendRepoV2(db.Queries, pool),
			Audit:    adapters.NewAuditRepoV2(db.Queries, nil),
			Logger:   l.Named("bootstrap"),
			Prober:   prober,
		}); err != nil {
			l.Fatal("bootstrap backends failed", zap.Error(err))
		}

		l.Info("bootstrap complete")
	},
}
