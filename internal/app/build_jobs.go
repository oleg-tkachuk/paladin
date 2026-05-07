package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	authstore "github.com/oleg-tkachuk/paladin/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/worker"
	"github.com/oleg-tkachuk/paladin/internal/worker/operations"
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
			Repo:     adapters.NewOperationRepo(db.Queries, deps.Pool),
			TTL:      cfg.Workers.Housekeeping.OperationsTTL,
			Interval: cfg.Workers.Housekeeping.Interval,
			Logger:   l.Named("operations-reaper"),
		})
	}

	// Capability revocation purger — only when the capability subsystem
	// is wired (deps.Capability != nil) AND the worker config carries a
	// non-zero interval. Keeps the denylist bounded; verifier correctness
	// is unaffected (an expired token can never verify, so dropping its
	// revocation row is safe).
	if deps.Capability != nil && cfg.Workers.Capability.Interval > 0 {
		out = append(out, &worker.CapabilityPurger{
			Store:      deps.Capability.Store,
			Interval:   cfg.Workers.Capability.Interval,
			ExpiredFor: cfg.Workers.Capability.ExpiredFor,
			Logger:     l.Named("capability-purger"),
		})
	}

	// API-token purger — same shape as capability purger. Bounds the
	// api_tokens table size; expired rows can never satisfy the time
	// gate so dropping them is safe.
	if deps.APIToken != nil && cfg.Workers.APIToken.Interval > 0 {
		out = append(out, &worker.APITokenPurger{
			Store:      deps.APIToken.Store,
			Limiter:    deps.APIToken.Limiter,
			Interval:   cfg.Workers.APIToken.Interval,
			ExpiredFor: cfg.Workers.APIToken.ExpiredFor,
			Logger:     l.Named("api-token-purger"),
		})
	}

	// Operations runner — dequeues PENDING rows from the operations
	// table and dispatches to per-type Executors. Without this every
	// BatchXxx RPC stages a row that never reaches a terminal state.
	// Disabled by setting interval to 0.
	if cfg.Workers.Operations.Interval > 0 {
		opRepo := adapters.NewOperationRepo(db.Queries, deps.Pool)
		executors := map[string]operations.Executor{
			"BatchDelete": &operations.BatchDeleteExecutor{
				Objects:     deps.Repos.Object,
				Transitions: deps.SM,
			},
			// BatchCopy / BatchUpdateTags / BatchRestoreObjects
			// executors are intentionally not registered — handlers
			// that enqueue those types will see the runner mark the
			// row FAILED with code=UNKNOWN_TYPE on the first claim,
			// surfacing the gap to clients instead of silently
			// hanging. Track in BACKLOG for follow-up.
		}
		out = append(out, &operations.Runner{
			Repo:      opRepo,
			Executors: executors,
			Interval:  cfg.Workers.Operations.Interval,
			Logger:    l.Named("operations-runner"),
		})
	}

	// Summarization worker — first caller of the LLM Registry. Runs
	// only when LLM is wired (deps.LLM != nil) AND a tick interval is
	// configured. Without LLM the worker has no provider to call; it
	// would skip every row but still burn CPU. Empty allow-list also
	// short-circuits — operators must opt content types in explicitly.
	if deps.LLM != nil && cfg.Workers.Summarization.Interval > 0 && len(cfg.Workers.Summarization.AllowedTypes) > 0 {
		out = append(out, &worker.SummarizationWorker{
			Source:       db.Queries,
			Fetcher:      deps.S3,
			Registry:     deps.LLM.Registry,
			Interval:     cfg.Workers.Summarization.Interval,
			BatchSize:    cfg.Workers.Summarization.BatchSize,
			MaxBytes:     cfg.Workers.Summarization.MaxObjectBytes,
			AllowedTypes: stringSet(cfg.Workers.Summarization.AllowedTypes),
			Logger:       l.Named("summarize"),
		})
	}

	// Embedding worker — first caller of the Vector Indexer. Rides on
	// the summary text that SummarizationWorker stamps; gates on both
	// LLM (need a provider for RoleEmbeddings) and Vector (need a
	// backend to write to).
	if deps.LLM != nil && deps.Vector != nil && cfg.Workers.Embedding.Interval > 0 {
		out = append(out, &worker.EmbeddingWorker{
			Source:    db.Queries,
			Indexer:   deps.Vector.Indexer,
			Registry:  deps.LLM.Registry,
			Interval:  cfg.Workers.Embedding.Interval,
			BatchSize: cfg.Workers.Embedding.BatchSize,
			Logger:    l.Named("embed"),
		})
	}

	return out
}

// stringSet builds a lookup set from a config-supplied list. Cheap
// helper kept here because no other call site needs it.
func stringSet(in []string) map[string]struct{} {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(in))
	for _, s := range in {
		out[s] = struct{}{}
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
