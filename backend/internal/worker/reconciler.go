// Reconciler v2 is the self-healing loop behind Paladin's idempotent state
// machine. It covers three gaps:
//
//   1. S3 event pipeline outage — promotes PENDING rows whose object is
//      actually uploaded (HEAD succeeds).
//   2. Expired presigns with no uploads — marks PENDING → FAILED.
//   3. Orphan cleanup — flags long-gone PENDING rows for removal by the
//      retention policy (not deleted here; audit/forensics wants them).
//
// Resilient by design: every action is idempotent, so running multiple
// replicas concurrently is safe — the sequencer/state guards on the SQL
// updates serialize outcomes.

package worker

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// ReconcilerV2Config governs poll cadence and per-tick ceilings.
type ReconcilerV2Config struct {
	PollInterval    time.Duration
	PendingGraceTTL time.Duration
	BatchSize       int
}

// StorageProbe HEADs the backend to materialize authoritative etag/size. A
// narrow interface so unit tests don't need a full S3 client.
type StorageProbe interface {
	HeadByObjectID(ctx context.Context, objectID uuid.UUID) (etag string, sizeBytes int64, checksum, sequencer string, found bool, err error)
}

type ReconcilerV2 struct {
	sm    *statemachine.Transitioner
	probe StorageProbe
	cfg   ReconcilerV2Config
	log   *zap.Logger
}

func NewReconcilerV2(sm *statemachine.Transitioner, probe StorageProbe, cfg ReconcilerV2Config, log *zap.Logger) *ReconcilerV2 {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 30 * time.Second
	}
	if cfg.PendingGraceTTL == 0 {
		cfg.PendingGraceTTL = 2 * time.Hour
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 100
	}
	return &ReconcilerV2{sm: sm, probe: probe, cfg: cfg, log: log}
}

// Run blocks until ctx is cancelled. Each tick:
//
//  1. scans PENDING-expired rows
//  2. HEADs the backend; if present → PromoteToAvailable, else → MarkFailed
//
// Concurrent reconciler replicas both try to advance the same rows — both
// are safe because the underlying SQL uses state guards.
func (r *ReconcilerV2) Run(ctx context.Context) error {
	return RunTicker(ctx, "reconciler", r.cfg.PollInterval, func(ctx context.Context) error {
		r.tick(ctx)
		return nil
	})
}

func (r *ReconcilerV2) tick(ctx context.Context) {
	ids, err := r.sm.ScanPendingExpired(ctx, r.cfg.PendingGraceTTL, r.cfg.BatchSize)
	if err != nil {
		r.log.Warn("failed to scan pending objects", zap.Error(err))
		return
	}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return
		}
		r.reconcile(ctx, id)
	}
}

func (r *ReconcilerV2) reconcile(ctx context.Context, objectID uuid.UUID) {
	etag, size, checksum, seq, found, err := r.probe.HeadByObjectID(ctx, objectID)
	if err != nil {
		r.log.Warn("failed to HEAD object", zap.String("object_id", objectID.String()), zap.Error(err))
		return
	}
	if found {
		if _, err := r.sm.PromoteToAvailable(ctx, objectID, etag, size, checksum, seq, statemachine.SourceReconciler); err != nil {
			r.log.Warn("failed to promote object", zap.String("object_id", objectID.String()), zap.Error(err))
		}
		return
	}
	if err := r.sm.MarkFailed(ctx, objectID, "presign-expired"); err != nil {
		r.log.Warn("failed to mark object as failed", zap.String("object_id", objectID.String()), zap.Error(err))
	}
}
