package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	authstore "github.com/oleg-tkachuk/paladin/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/internal/storage/s3adapter"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/worker"
	"github.com/oleg-tkachuk/paladin/internal/worker/operations"
)

// BuildBackgroundJobs assembles the worker fan that the `serve worker`
// subcommand runs. Each entry is a long-lived goroutine satisfying the
// BackgroundJob shape; the caller wraps each with a Lease so only one
// pod runs the job at a time.
//
// Reconciler is conditionally appended depending on cfg.Worker.Jobs.Reconciler;
// the same pattern applies to lifecycle, replication, and audit-log purger.
// Empty slice in dev when no housekeeping is configured.
func BuildBackgroundJobs(deps *SharedDeps) []BackgroundJob {
	cfg := deps.Cfg
	db := deps.DB
	l := deps.Logger

	out := []BackgroundJob{
		&worker.RefreshTokenPurger{
			Repo:     adapters.NewRefreshTokenRepo(db.Queries),
			Interval: cfg.Worker.Jobs.RefreshTokenReap.Interval,
			Logger:   l.Named("refresh-purger"),
		},
		&worker.ApiKeyExpirer{
			Repo:     &apiKeyExpirerAdapter{r: adapters.NewApiKeyRepo(db.Queries)},
			Interval: cfg.Worker.Jobs.ApiKeyReap.Interval,
			Logger:   l.Named("api-key-expirer"),
		},
	}

	if cfg.Worker.Jobs.Lifecycle.Enabled {
		out = append(out, &worker.LifecycleWorker{
			Buckets:      adapters.NewLifecycleSource(db.Queries),
			Objects:      adapters.NewLifecycleObjectIter(db.Queries),
			SoftDeleter:  deps.SM,
			CELEvaluator: deps.CELEval,
			Interval:     cfg.Worker.Jobs.Lifecycle.Interval,
			Logger:       l.Named("lifecycle"),
		})
	}

	// Replication worker — dry-run until the StorageReplicator implementation
	// lands. Walks objects in replicated buckets and logs intent without
	// actually copying. Operators flip to live mode by injecting a real
	// replicator from internal/storage in the slice that lands replication.
	if cfg.Worker.Jobs.Replication.Enabled {
		out = append(out, &worker.ReplicationWorker{
			Buckets:        adapters.NewLifecycleSource(db.Queries),
			Objects:        adapters.NewLifecycleObjectIter(db.Queries),
			Replicator:     nil, // dry-run
			Watermarks:     adapters.NewReplicationWatermarkRepo(db.Queries),
			Interval:       cfg.Worker.Jobs.Replication.Interval,
			LookbackWindow: cfg.Worker.Jobs.Replication.LookbackWindow,
			Logger:         l.Named("replication"),
		})
	}

	if cfg.Worker.Jobs.Reconciler.Interval > 0 {
		// Reuse the SharedDeps S3 client; same default backend as listeners.
		out = append(out, worker.NewReconcilerV2(
			deps.SM,
			adapters.NewReconcilerProbe(db.Queries, s3adapter.NewObjectRouter(deps.Registry)),
			worker.ReconcilerV2Config{
				PollInterval:    cfg.Worker.Jobs.Reconciler.Interval,
				PendingGraceTTL: cfg.Worker.Jobs.Reconciler.MinObjectAge,
				BatchSize:       cfg.Worker.Jobs.Reconciler.BatchSize,
			},
			l.Named("reconciler"),
		))
		// Bucket-provision outbox worker — drives the second half of
		// CreateBucket(provision_on_backend=true). Routed by backend id: each
		// pending row names its backend, and a dedicated bucket may live on a
		// non-default backend, so the reconciler must provision on the row's
		// own backend rather than the default client (prerequisite for the
		// per-tenant dedicated-bucket layout, ADR-0011 Phase 1).
		bucketRec := worker.NewBucketReconciler(
			adapters.NewBucketRepoV2(db.Queries, deps.Pool),
			s3adapter.NewProvisionerRouter(deps.Registry),
			worker.BucketReconcilerConfig{
				Interval:  cfg.Worker.Jobs.Reconciler.Interval,
				BatchSize: int32(cfg.Worker.Jobs.Reconciler.BatchSize),
			},
			l.Named("bucket-reconciler"),
		)
		// Producer-only dispatcher (ADR-0003): on the terminal row removal
		// the reconciler enqueues paladin.bucket.deleted on the same tx. Bound to
		// the worker pool; the dispatcher pod drains event_deliveries, so no
		// NATS pool here (enqueue never opens a socket).
		bucketRec.SetEventProducer(&worker.Dispatcher{
			Store:       worker.NewRepoSubscriptionStore(adapters.NewEventSubscriptionRepoV2(db.Queries)),
			Outbox:      worker.PgxOutboxWriter{Pool: deps.Pool},
			Logger:      l.Named("bucket-reconciler-events"),
			MaxAttempts: 3,
		})
		out = append(out, bucketRec)
	}

	if cfg.Worker.Jobs.Housekeeping.AuditLogTTL > 0 {
		out = append(out, &worker.AuditLogPurger{
			Purger:   adapters.NewAuditRepoV2(db.Queries),
			TTL:      cfg.Worker.Jobs.Housekeeping.AuditLogTTL,
			Interval: cfg.Worker.Jobs.Housekeeping.Interval,
			Logger:   l.Named("audit-purger"),
		})
	}
	if cfg.Worker.Jobs.Housekeeping.OperationsTTL > 0 {
		out = append(out, &worker.OperationsReaper{
			Repo:     adapters.NewOperationRepo(db.Queries, deps.Pool),
			TTL:      cfg.Worker.Jobs.Housekeeping.OperationsTTL,
			Interval: cfg.Worker.Jobs.Housekeeping.Interval,
			Logger:   l.Named("operations-reaper"),
		})
	}

	// Idempotency-key purger — always on (the table grows on every
	// idempotent Create regardless of other housekeeping toggles). Rows
	// self-expire via expires_at; this reclaims them so the unique index
	// stays lean.
	out = append(out, &worker.IdempotencyKeyPurger{
		Purger:   db.Queries,
		Interval: cfg.Worker.Jobs.Housekeeping.Interval,
		Logger:   l.Named("idempotency-purger"),
	})

	// Partition maintainer — pre-creates upcoming partitions and DROPs whole
	// partitions past retention for the RANGE-partitioned tables (migrations
	// 041 audit_log monthly, 042 idempotency_keys daily). This is the
	// DROP-PARTITION payoff; the *Purger DELETEs above stay as the backstop
	// for the DEFAULT partition. deps.Pool satisfies worker.PartitionDB.
	//
	// audit_log retention follows AuditLogTTL: a negative Retention disables
	// dropping (TTL=0 means keep forever) while still keeping partitions
	// pre-created ahead. idempotency_keys uses Retention=0 — an elapsed day's
	// keys are all expired, so its partition is safe to drop immediately.
	auditRetention := cfg.Worker.Jobs.Housekeeping.AuditLogTTL
	if auditRetention <= 0 {
		auditRetention = -1 // keep-forever: create-ahead, never drop
	}
	out = append(out, &worker.PartitionMaintainer{
		DB: deps.Pool,
		Specs: []worker.PartitionSpec{
			{Table: "audit_log", Period: worker.PeriodMonthly, Retention: auditRetention, Ahead: 3},
			{Table: "idempotency_keys", Period: worker.PeriodDaily, Retention: 0, Ahead: 8},
		},
		Interval: cfg.Worker.Jobs.Housekeeping.Interval,
		Logger:   l.Named("partition-maintainer"),
	})

	// Abandoned-multipart reaper — aborts S3 multipart sessions whose
	// client never Completed/Aborted (otherwise part bytes are billed
	// forever). 0 disables it.
	if cfg.Worker.Jobs.Housekeeping.MultipartTTL > 0 {
		out = append(out, &worker.MultipartReaper{
			Q: db.Queries,
			// Routed: ListStaleMultipartUploads returns each session's backend,
			// so the abort targets the object's own backend.
			Storage:   s3adapter.NewMultipartRouter(deps.Registry),
			TTL:       cfg.Worker.Jobs.Housekeeping.MultipartTTL,
			Interval:  cfg.Worker.Jobs.Housekeeping.Interval,
			BatchSize: cfg.Worker.Jobs.Housekeeping.HardDeleteBatchSize,
			Logger:    l.Named("multipart-reaper"),
		})
	}

	// Lifecycle hard-deleter — closes the loop on soft-delete by
	// reclaiming the S3 bytes after the cooling-off window. 0 keeps
	// rows DELETED forever (audit-friendly, dev default); a non-zero
	// duration enables the cascade. Routed: ListHardDeletable returns each
	// object's backend, so the DELETE reclaims bytes on the object's own
	// backend rather than the default.
	if cfg.Worker.Jobs.Housekeeping.HardDeleteAfter > 0 {
		out = append(out, &worker.LifecycleHardDeleter{
			Q:         db.Queries,
			Storage:   s3adapter.NewObjectRouter(deps.Registry),
			TTL:       cfg.Worker.Jobs.Housekeeping.HardDeleteAfter,
			Interval:  cfg.Worker.Jobs.Housekeeping.Interval,
			BatchSize: cfg.Worker.Jobs.Housekeeping.HardDeleteBatchSize,
			Logger:    l.Named("hard-deleter"),
		})
	}

	// Capability revocation purger — only when the capability subsystem
	// is wired (deps.Capability != nil) AND the worker config carries a
	// non-zero interval. Keeps the denylist bounded; verifier correctness
	// is unaffected (an expired token can never verify, so dropping its
	// revocation row is safe).
	if deps.Capability != nil && cfg.Worker.Jobs.Capability.Interval > 0 {
		out = append(out, &worker.CapabilityPurger{
			Store:      deps.Capability.Store,
			Usage:      deps.Capability.Usage,
			Interval:   cfg.Worker.Jobs.Capability.Interval,
			ExpiredFor: cfg.Worker.Jobs.Capability.ExpiredFor,
			Logger:     l.Named("capability-purger"),
		})
	}

	// API-token purger — same shape as capability purger. Bounds the
	// api_tokens table size; expired rows can never satisfy the time
	// gate so dropping them is safe.
	if deps.APIToken != nil && cfg.Worker.Jobs.APIToken.Interval > 0 {
		out = append(out, &worker.APITokenPurger{
			Store:      deps.APIToken.Store,
			Limiter:    deps.APIToken.Limiter,
			Interval:   cfg.Worker.Jobs.APIToken.Interval,
			ExpiredFor: cfg.Worker.Jobs.APIToken.ExpiredFor,
			Logger:     l.Named("api-token-purger"),
		})
	}

	// Operations runner — dequeues PENDING rows from the operations
	// table and dispatches to per-type Executors. Without this every
	// BatchXxx RPC stages a row that never reaches a terminal state.
	// Disabled by setting interval to 0.
	if cfg.Worker.Jobs.Operations.Interval > 0 {
		opRepo := adapters.NewOperationRepo(db.Queries, deps.Pool)
		executors := map[string]operations.Executor{
			"BatchDelete": &operations.BatchDeleteExecutor{
				Objects:     deps.Repos.Object,
				Transitions: deps.SM,
			},
			"BatchCopy": &operations.BatchCopyExecutor{
				Objects: deps.Repos.Object,
				// Routed: BatchCopy resolves (backend, bucket) per object_key
				// and builds Locations carrying BackendID, so the router
				// dispatches each copy to the right backend.
				Storage:           s3adapter.NewObjectRouter(deps.Registry),
				Transitions:       deps.SM,
				PresignDefaultTTL: cfg.Limits.Presign.DefaultTTL,
			},
			"BatchUpdateTags": &operations.BatchUpdateTagsExecutor{
				Objects: deps.Repos.Object,
			},
			"BatchRestoreObjects": &operations.BatchRestoreExecutor{
				Objects:     deps.Repos.Object,
				Transitions: deps.SM,
			},
		}
		out = append(out, &operations.Runner{
			Repo:      opRepo,
			Executors: executors,
			Interval:  cfg.Worker.Jobs.Operations.Interval,
			Logger:    l.Named("operations-runner"),
		})
	}

	// ADR-0011 Phase 3: shared->dedicated storage migration copy job. Always
	// registered — it is idle unless a tenant_storage_migrations row is active
	// (the admin MigrateTenantStorageLayout RPC creates one), so it needs no
	// config toggle. Slice 1 is same-backend server-side copy.
	migCopier := storageCopier{s: deps.Storage.Object}
	out = append(out, &worker.StorageMigrationWorker{
		Repo:    adapters.NewStorageMigrationRepo(db.Queries, deps.Pool),
		Copier:  migCopier,
		Deleter: migCopier, // retention-gated source cleanup (slice 2)
		Logger:  l.Named("storage-migration"),
	})

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
