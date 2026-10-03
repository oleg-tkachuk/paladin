package app

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/safecast"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/s3adapter"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker/operations"
	"go.uber.org/zap"
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
	l := deps.Logger

	// Background jobs are cross-tenant and run with no request principal, so they
	// must NOT read/write through the RLS-scoped runtime pool (paladin_app): with no
	// paladin.tenant_id GUC set, RLS on objects / multipart_uploads / event_deliveries
	// / api_tokens (the schema baseline (001_initial_schema.sql)) returns zero rows and every reaper silently
	// no-ops. Bind them to the BYPASSRLS reaper pool (serve_worker opens it from
	// ReaperDSN / MigrateDSN). deps.ReaperPool is nil only in the un-wired
	// fallback → degrade to deps.Pool (RLS-gated, as before; serve_worker warns).
	reaperPool := deps.Pool
	if deps.ReaperPool != nil {
		reaperPool = deps.ReaperPool
	}
	reaperQ := sqlc.New(reaperPool)

	// Object state transitions for the same fan, on the same pool and for the
	// same reason. deps.SM is bound to the RLS-scoped runtime pool, so a job
	// that transitions objects through it reads zero rows and silently does
	// nothing — the hazard above, reached through the state machine rather
	// than a repo. ReconcilerV2 sat in exactly that state: lease healthy, tick
	// every 30s, ScanPendingExpired returning nothing while pending-expired
	// objects piled up for weeks. Nothing logged it, because finding nothing
	// is its success path.
	smReaper := statemachine.New(reaperPool)

	// partitionPool is the DDL-capable pool for PartitionMaintainer (the only
	// background job that runs CREATE/ATTACH/DROP PARTITION). It needs the
	// migrate role's table ownership, which the least-privilege reaper role
	// lacks — so prefer deps.PartitionPool, degrading to the reaper pool (dev
	// parity, where both are the same paladin_migrate BYPASSRLS pool).
	partitionPool := reaperPool
	if deps.PartitionPool != nil {
		partitionPool = deps.PartitionPool
	}

	out := []BackgroundJob{
		&worker.RefreshTokenPurger{
			Repo:     adapters.NewRefreshTokenRepo(reaperQ),
			Interval: cfg.Worker.Jobs.RefreshTokenReap.Interval,
			Logger:   l.Named("refresh-purger"),
		},
	}

	if cfg.Worker.Jobs.Lifecycle.Enabled {
		out = append(out, &worker.LifecycleWorker{
			Buckets:      adapters.NewLifecycleSource(reaperQ),
			Objects:      adapters.NewLifecycleObjectIter(reaperQ),
			SoftDeleter:  smReaper,
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
			Buckets:        adapters.NewLifecycleSource(reaperQ),
			Objects:        adapters.NewLifecycleObjectIter(reaperQ),
			Replicator:     nil, // dry-run
			Watermarks:     adapters.NewReplicationWatermarkRepo(reaperQ),
			Interval:       cfg.Worker.Jobs.Replication.Interval,
			LookbackWindow: cfg.Worker.Jobs.Replication.LookbackWindow,
			Logger:         l.Named("replication"),
		})
	}

	if cfg.Worker.Jobs.Reconciler.Interval > 0 {
		// Reuse the SharedDeps S3 client; same default backend as listeners.
		out = append(out, worker.NewReconcilerV2(
			smReaper,
			adapters.NewReconcilerProbe(reaperQ, s3adapter.NewObjectRouter(deps.Registry)),
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
		// per-tenant dedicated-bucket layout, ADR-0015 Phase 1).
		bucketRec := worker.NewBucketReconciler(
			adapters.NewBucketRepoV2(reaperQ, reaperPool),
			s3adapter.NewProvisionerRouter(deps.Registry),
			worker.BucketReconcilerConfig{
				Interval:  cfg.Worker.Jobs.Reconciler.Interval,
				BatchSize: safecast.Int32(cfg.Worker.Jobs.Reconciler.BatchSize),
			},
			l.Named("bucket-reconciler"),
		)
		// Producer-only dispatcher (ADR-0003): on the terminal row removal
		// the reconciler enqueues paladin.bucket.deleted on the same tx. Bound to
		// the worker pool; the dispatcher pod drains event_deliveries, so no
		// NATS pool here (enqueue never opens a socket).
		bucketRec.SetEventProducer(&worker.Dispatcher{
			Store:       worker.NewRepoSubscriptionStore(adapters.NewEventSubscriptionRepoV2(reaperQ)),
			Outbox:      worker.PgxOutboxWriter{Pool: reaperPool},
			Logger:      l.Named("bucket-reconciler-events"),
			MaxAttempts: 3,
		})
		out = append(out, bucketRec)
	}

	if cfg.Worker.Jobs.Housekeeping.AuditLogTTL > 0 {
		out = append(out, &worker.AuditLogPurger{
			// Purger only calls PurgeOlderThan (autocommit) — no
			// InsertWithOutbox — so no pool is needed here.
			Purger:   adapters.NewAuditRepoV2(reaperQ, nil),
			TTL:      cfg.Worker.Jobs.Housekeeping.AuditLogTTL,
			Interval: cfg.Worker.Jobs.Housekeeping.Interval,
			Logger:   l.Named("audit-purger"),
		})
	}
	if cfg.Worker.Jobs.Housekeeping.EventDeliveriesTTL > 0 {
		out = append(out, &worker.EventDeliveryPurger{
			Repo:     adapters.NewEventDeliveryRepo(reaperQ),
			TTL:      cfg.Worker.Jobs.Housekeeping.EventDeliveriesTTL,
			Interval: cfg.Worker.Jobs.Housekeeping.Interval,
			Logger:   l.Named("event-delivery-purger"),
		})
	}
	if cfg.Worker.Jobs.Housekeeping.OperationsTTL > 0 {
		out = append(out, &worker.OperationsReaper{
			Repo:     adapters.NewOperationRepo(reaperQ, reaperPool),
			TTL:      cfg.Worker.Jobs.Housekeeping.OperationsTTL,
			Interval: cfg.Worker.Jobs.Housekeeping.Interval,
			Logger:   l.Named("operations-reaper"),
		})
	}

	// Tenant rate-bucket sweeper — always on, for the same reason the
	// idempotency purger is: the limiter writes a row per active tenant per
	// minute whatever else is toggled, and reads back only the last two.
	out = append(out, &worker.TenantRateBucketSweeper{
		// reaperQ, not the app queries: the sweep deletes across every
		// tenant, and tenant_rate_buckets carries an RLS policy (011). On the
		// app role with no tenant in context the policy would match nothing
		// and the sweep would silently reclaim zero rows.
		Store:    adapters.NewTenantRateStore(reaperQ),
		Interval: cfg.Worker.Jobs.Housekeeping.Interval,
		Logger:   l.Named("tenant-rate-sweeper"),
	})

	// Idempotency-key purger — always on (the table grows on every
	// idempotent Create regardless of other housekeeping toggles). Rows
	// self-expire via expires_at; this reclaims them so the unique index
	// stays lean.
	out = append(out, &worker.IdempotencyKeyPurger{
		Purger:   reaperQ,
		Interval: cfg.Worker.Jobs.Housekeeping.Interval,
		Logger:   l.Named("idempotency-purger"),
	})

	// Partition maintainer — pre-creates upcoming partitions and DROPs whole
	// partitions past retention for the RANGE-partitioned tables (migrations
	// 041 audit_log monthly, 042 idempotency_keys daily). This is the
	// DROP-PARTITION payoff; the *Purger DELETEs above stay as the backstop
	// for the DEFAULT partition. Runs on partitionPool (the DDL-capable migrate
	// role) — its CREATE/ATTACH/DROP PARTITION need table ownership the reaper
	// role lacks. Its DEFAULT-overlap recovery also relocates rows, so BYPASSRLS
	// is required to see every tenant's rows.
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
		DB: partitionPool,
		Specs: []worker.PartitionSpec{
			{Table: "audit_log", Period: worker.PeriodMonthly, Retention: auditRetention, Ahead: 3, PartitionKey: "at"},
			{Table: "idempotency_keys", Period: worker.PeriodDaily, Retention: 0, Ahead: 8, PartitionKey: "expires_at"},
		},
		Interval: cfg.Worker.Jobs.Housekeeping.Interval,
		Logger:   l.Named("partition-maintainer"),
	})

	// Abandoned-multipart reaper — aborts S3 multipart sessions whose
	// client never Completed/Aborted (otherwise part bytes are billed
	// forever). 0 disables it.
	if cfg.Worker.Jobs.Housekeeping.MultipartTTL > 0 {
		// Pays the abort debt the trigger records when a cascade — from an
		// object's permanent delete or a tenant's hard delete — removes a
		// session row without anyone telling S3. Runs alongside the reaper,
		// which handles the other half: sessions the client simply abandoned.
		out = append(out, &worker.MultipartAbortDrainer{
			Pool:       reaperPool,
			Q:          reaperQ,
			Storage:    s3adapter.NewMultipartRouter(deps.Registry),
			Interval:   cfg.Worker.Jobs.PurgeDrain.Interval,
			BatchSize:  safecast.Int32(cfg.Worker.Jobs.PurgeDrain.BatchSize),
			MaxBackoff: cfg.Worker.Jobs.PurgeDrain.MaxBackoff,
			Logger:     l.Named("multipart-abort-drainer"),
		})

		out = append(out, &worker.MultipartReaper{
			Q:        reaperQ,
			Sessions: adapters.NewMultipartRepo(reaperQ, reaperPool),
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
	// purgeEvents is the producer-only dispatcher the two byte-reclaiming
	// workers share. Bound to the worker pool; the dispatcher pod drains
	// event_deliveries, so enqueueing here never opens a socket. Same shape
	// as the bucket reconciler's above.
	purgeEvents := &worker.Dispatcher{
		Store:       worker.NewRepoSubscriptionStore(adapters.NewEventSubscriptionRepoV2(reaperQ)),
		Outbox:      worker.PgxOutboxWriter{Pool: reaperPool},
		Logger:      l.Named("purge-events"),
		MaxAttempts: 3,
	}

	if cfg.Worker.Jobs.Housekeeping.HardDeleteAfter > 0 {
		out = append(out, &worker.LifecycleHardDeleter{
			Q:         reaperQ,
			Pool:      reaperPool,
			Storage:   s3adapter.NewObjectRouter(deps.Registry),
			Events:    purgeEvents,
			TTL:       cfg.Worker.Jobs.Housekeeping.HardDeleteAfter,
			Interval:  cfg.Worker.Jobs.Housekeeping.Interval,
			BatchSize: cfg.Worker.Jobs.Housekeeping.HardDeleteBatchSize,
			Logger:    l.Named("hard-deleter"),
		})
	}

	// Purge drainer — the retry half of the byte-reclaim outbox. Unlike the
	// hard-deleter above this is NOT gated on a cooling-off window: the debt
	// it drains describes objects whose row is already gone, so there is
	// nothing left to restore and no reason to wait.
	if cfg.Worker.Jobs.PurgeDrain.Interval > 0 {
		out = append(out, &worker.PurgeDrainer{
			Pool:       reaperPool,
			Q:          reaperQ,
			Storage:    s3adapter.NewObjectRouter(deps.Registry),
			Events:     purgeEvents,
			Interval:   cfg.Worker.Jobs.PurgeDrain.Interval,
			BatchSize:  safecast.Int32(cfg.Worker.Jobs.PurgeDrain.BatchSize),
			MaxBackoff: cfg.Worker.Jobs.PurgeDrain.MaxBackoff,
			Logger:     l.Named("purge-drainer"),
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

	// Quota reconciler — recomputes quotas.usage_* from live objects and
	// rolls the per-day counters at the UTC day boundary. The upload path
	// only ever increments those columns (best-effort, never decremented
	// on delete) and middleware.QuotaSoftCheck rejects uploads against
	// them, so without this job a tenant that deletes its objects stays
	// counted and eventually cannot write. Runs on the BYPASSRLS pool
	// because both `quotas` and `objects` are RLS'd — on the runtime pool
	// the reconcile statement would match zero rows and quietly no-op,
	// which is exactly the failure it is here to prevent.
	if cfg.Worker.Jobs.QuotaReconcile.Interval > 0 {
		out = append(out, &worker.QuotaReconciler{
			Store:    adapters.NewQuotaReconcileRepo(reaperPool),
			Interval: cfg.Worker.Jobs.QuotaReconcile.Interval,
			Logger:   l.Named("quota-reconciler"),
		})
	}

	// Operations runner — dequeues PENDING rows from the operations
	// table and dispatches to per-type Executors. Without this every
	// BatchXxx RPC stages a row that never reaches a terminal state.
	// Disabled by setting interval to 0.
	if cfg.Worker.Jobs.Operations.Interval > 0 {
		opRepo := adapters.NewOperationRepo(reaperQ, reaperPool)
		executors := map[string]operations.Executor{
			"BatchDelete": &operations.BatchDeleteExecutor{
				Objects:     deps.Repos.Object,
				Transitions: smReaper,
				// The hard-delete path, shared with the DeleteObject RPC. The
				// handler is built with no policy engine and no CEL filter on
				// purpose: out here there is no principal to authorize, and
				// the executor reaches it only through the narrow
				// operations.PermanentDeleter interface, which exposes
				// nothing that would need one. Events are wired because a
				// permanent delete emits paladin.object.deleted and
				// paladin.object.purged, and silence would be a regression
				// against the RPC path.
				Permanent: newPermanentDeleter(deps, reaperQ, reaperPool, smReaper, l),
			},
			"BatchCopy": &operations.BatchCopyExecutor{
				Objects: deps.Repos.Object,
				// Routed: BatchCopy resolves (backend, bucket) per collection
				// and builds Locations carrying BackendID, so the router
				// dispatches each copy to the right backend.
				Storage:     s3adapter.NewObjectRouter(deps.Registry),
				Transitions: smReaper,
				PendingTTL:  cfg.Limits.Presign.PutTTL,
				Limits:      cfg.Limits.UploadLimits(),
			},
			"BatchUpdateTags": &operations.BatchUpdateTagsExecutor{
				Objects: deps.Repos.Object,
			},
			"BatchRestoreObjects": &operations.BatchRestoreExecutor{
				Objects:     deps.Repos.Object,
				Transitions: smReaper,
			},
		}
		out = append(out, &operations.Runner{
			Repo:      opRepo,
			Executors: executors,
			Interval:  cfg.Worker.Jobs.Operations.Interval,
			Logger:    l.Named("operations-runner"),
			// Heartbeat so StaleOperationReclaimer can tell a long batch from a
			// worker that died holding one.
			Toucher: opRepo,
		})

		// Reclaim operations abandoned by a worker that stopped between the
		// claim and the terminal write. Runs alongside the runner rather than
		// with the other housekeeping jobs because it is part of the queue's
		// own correctness, not retention.
		out = append(out, &worker.StaleOperationReclaimer{
			Repo:       opRepo,
			Interval:   cfg.Worker.Jobs.Housekeeping.Interval,
			StaleAfter: cfg.Worker.Jobs.Operations.StaleAfter,
			Logger:     l.Named("stale-operation-reclaimer"),
		})
	}

	// ADR-0015 Phase 3: shared->dedicated storage migration copy job. Always
	// registered — it is idle unless a tenant_storage_migrations row is active
	// (the admin MigrateTenantStorageLayout RPC creates one), so it needs no
	// config toggle. Slice 1 is same-backend server-side copy.
	migCopier := storageCopier{s: deps.Storage.Object}
	out = append(out, &worker.StorageMigrationWorker{
		Repo:    adapters.NewStorageMigrationRepo(reaperQ, reaperPool),
		Copier:  migCopier,
		Deleter: migCopier, // retention-gated source cleanup (slice 2)
		Header:  migCopier, // physical (HEAD size) verify
		Logger:  l.Named("storage-migration"),
	})

	return out
}

// newPermanentDeleter builds the object handler the BatchDelete executor uses
// for hard deletes. It is deliberately partial: no policy engine and no CEL
// filter, because the worker has no principal to authorize and the executor
// only ever reaches it through operations.PermanentDeleter, whose single
// method needs neither. The event producer is real — a permanent delete emits
// paladin.object.deleted inside the removal transaction and
// paladin.object.purged once the bytes are reclaimed, and a batch that went
// quiet where the RPC speaks would be a regression, not a shortcut.
func newPermanentDeleter(
	deps *SharedDeps,
	reaperQ *sqlc.Queries,
	reaperPool *pgxpool.Pool,
	sm *statemachine.Transitioner,
	l *zap.Logger,
) *objecth.Handler {
	h := objecth.NewHandler(
		deps.Repos.Object,
		s3adapter.NewObjectRouter(deps.Registry),
		nil, // policy: nothing to authorize without a caller
		nil, // filter: PermanentDelete does not list
		sm,
		objecth.PresignConfig{},
	)
	h.SetEventProducer(&worker.Dispatcher{
		Store:       worker.NewRepoSubscriptionStore(adapters.NewEventSubscriptionRepoV2(reaperQ)),
		Outbox:      worker.PgxOutboxWriter{Pool: reaperPool},
		Logger:      l.Named("batch-delete-events"),
		MaxAttempts: 3,
	})
	h.SetLogger(l.Named("batch-delete"))
	return h
}
