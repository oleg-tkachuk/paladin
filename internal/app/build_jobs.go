package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	authstore "github.com/oleg-tkachuk/paladin/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/worker"
)

// BuildBackgroundJobs assembles the worker fan that the `serve worker`
// subcommand runs. Each entry is a long-lived goroutine satisfying the
// BackgroundJob shape; the caller wraps each with a Lease so only one
// pod runs the job at a time.
//
// Reconciler is conditionally appended depending on cfg.Workers.Reconciler;
// the same pattern applies to lifecycle, replication, and audit-log purger.
// Empty slice in dev when no housekeeping is configured.
func BuildBackgroundJobs(deps *SharedDeps) []BackgroundJob {
	cfg := deps.Cfg
	db := deps.DB
	l := deps.Logger

	out := []BackgroundJob{
		&worker.RefreshTokenPurger{
			Repo:     adapters.NewRefreshTokenRepo(db.Queries),
			Interval: cfg.Workers.RefreshTokenReap.Interval,
			Logger:   l.Named("refresh-purger"),
		},
		&worker.ApiKeyExpirer{
			Repo:     &apiKeyExpirerAdapter{r: adapters.NewApiKeyRepo(db.Queries)},
			Interval: cfg.Workers.ApiKeyReap.Interval,
			Logger:   l.Named("api-key-expirer"),
		},
	}

	if cfg.Workers.Lifecycle.Enabled {
		out = append(out, &worker.LifecycleWorker{
			Buckets:      adapters.NewLifecycleSource(db.Queries),
			Objects:      adapters.NewLifecycleObjectIter(db.Queries),
			SoftDeleter:  deps.SM,
			CELEvaluator: deps.CELEval,
			Interval:     cfg.Workers.Lifecycle.Interval,
			Logger:       l.Named("lifecycle"),
		})
	}

	// Replication worker — dry-run until the StorageReplicator implementation
	// lands. Walks objects in replicated buckets and logs intent without
	// actually copying. Operators flip to live mode by injecting a real
	// replicator from internal/storage in the slice that lands replication.
	if cfg.Workers.Replication.Enabled {
		out = append(out, &worker.ReplicationWorker{
			Buckets:        adapters.NewLifecycleSource(db.Queries),
			Objects:        adapters.NewLifecycleObjectIter(db.Queries),
			Replicator:     nil, // dry-run
			Watermarks:     adapters.NewReplicationWatermarkRepo(db.Queries),
			Interval:       cfg.Workers.Replication.Interval,
			LookbackWindow: cfg.Workers.Replication.LookbackWindow,
			Logger:         l.Named("replication"),
		})
	}

	if cfg.Workers.Reconciler.Interval > 0 {
		// Reuse the SharedDeps S3 client; same default backend as listeners.
		out = append(out, worker.NewReconcilerV2(
			deps.SM,
			adapters.NewReconcilerProbe(db.Queries, deps.S3),
			worker.ReconcilerV2Config{
				PollInterval:    cfg.Workers.Reconciler.Interval,
				PendingGraceTTL: cfg.Workers.Reconciler.MinObjectAge,
				BatchSize:       cfg.Workers.Reconciler.BatchSize,
			},
			l.Named("reconciler"),
		))
		// Bucket-provision outbox worker — drives the second half of
		// CreateBucket(provision_on_backend=true). Same S3 client + cadence.
		out = append(out, worker.NewBucketReconciler(
			adapters.NewBucketRepoV2(db.Queries),
			deps.S3,
			worker.BucketReconcilerConfig{
				Interval:  cfg.Workers.Reconciler.Interval,
				BatchSize: int32(cfg.Workers.Reconciler.BatchSize),
			},
			l.Named("bucket-reconciler"),
		))
	}

	if cfg.Workers.Housekeeping.AuditLogTTL > 0 {
		out = append(out, &worker.AuditLogPurger{
			Purger:   adapters.NewAuditRepoV2(db.Queries),
			TTL:      cfg.Workers.Housekeeping.AuditLogTTL,
			Interval: cfg.Workers.Housekeeping.Interval,
			Logger:   l.Named("audit-purger"),
		})
	}
	if cfg.Workers.Housekeeping.OperationsTTL > 0 {
		out = append(out, &worker.OperationsReaper{
			Repo:     adapters.NewOperationRepo(db.Queries),
			TTL:      cfg.Workers.Housekeeping.OperationsTTL,
			Interval: cfg.Workers.Housekeeping.Interval,
			Logger:   l.Named("operations-reaper"),
		})
	}
	return out
}

// apiKeyExpirerAdapter narrows *adapters.ApiKeyRepo down to the
// worker.ApiKeyExpirerRepo two-method seam. Keeps the worker package free
// of a heavyweight import.
type apiKeyExpirerAdapter struct {
	r *adapters.ApiKeyRepo
}

func (a *apiKeyExpirerAdapter) ListExpired(ctx context.Context, at time.Time, limit int32) ([]authstore.ApiKey, error) {
	return a.r.ListExpired(ctx, at, limit)
}

func (a *apiKeyExpirerAdapter) Revoke(ctx context.Context, id uuid.UUID) error {
	return a.r.Revoke(ctx, id)
}

// silence unused — kept for explicit re-export so worker subcommand can
// instantiate adapters without re-importing the auth/store package.
var _ authstore.ApiKey
