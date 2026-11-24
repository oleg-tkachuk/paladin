package app

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// The replication worker is built to copy, not to dry-run: a replicator, the
// per-object replica store, and the evaluator its filters compile with.
// Without the evaluator every bucket with a filter would be refused.
func TestBuildBackgroundJobsWiresTheReplicationWorkerToCopy(t *testing.T) {
	cfg := config.Config{}
	cfg.Worker.Jobs.Replication.Enabled = true
	deps := &SharedDeps{
		Cfg: cfg, Logger: zap.NewNop(),
		Pool: &pgxpool.Pool{}, ReaperPool: &pgxpool.Pool{},
		CELEval: cel.NewEvaluator(),
	}
	var w *worker.ReplicationWorker
	for _, j := range BuildBackgroundJobs(deps) {
		if r, ok := j.(*worker.ReplicationWorker); ok {
			w = r
		}
	}
	if w == nil {
		t.Fatal("no replication worker built with replication enabled")
	}
	if w.Replicator == nil {
		t.Error("the replication worker is wired without a replicator: it would only dry-run")
	}
	if w.Replicas == nil || w.Buckets == nil {
		t.Errorf("replicas = %v, buckets = %v; want both from the database", w.Replicas, w.Buckets)
	}
	if w.CELEvaluator != deps.CELEval {
		t.Error("the replication worker does not compile filters with the shared evaluator")
	}
}
