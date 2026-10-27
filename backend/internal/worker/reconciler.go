// Reconciler v2 is the self-healing loop behind Paladin's idempotent state
// machine. It covers three gaps:
//
//   1. S3 event pipeline outage — promotes PENDING rows whose object is
//      actually uploaded (HEAD succeeds).
//   2. Expired presigns with no uploads — discards the PENDING row, logging
//      and counting why (statemachine.DiscardFailed).
//
// Resilient by design: every action is idempotent, so running multiple
// replicas concurrently is safe — the sequencer/state guards on the SQL
// updates serialize outcomes.

package worker

import (
	"context"
	"errors"
	"time"
	"uuid"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// ReconcilerV2Config governs poll cadence and per-tick ceilings.
// ReconcilerV2Config defaults, for a field left zero.
const (
	DefaultReconcilerPollInterval    = 30 * time.Second
	DefaultReconcilerPendingGraceTTL = 2 * time.Hour
	DefaultReconcilerBatchSize       = 100
)

type ReconcilerV2Config struct {
	PollInterval    time.Duration
	PendingGraceTTL time.Duration
	BatchSize       int
}

// StorageProbe HEADs the backend to materialize authoritative etag/size. A
// narrow interface so unit tests don't need a full S3 client.
type StorageProbe interface {
	HeadByObjectID(ctx context.Context, objectID uuid.UUID) (etag string, sizeBytes int64, checksum, sequencer string, found bool, err error)
	// DeleteByObjectID removes the stored bytes of an object that broke its
	// registration.
	DeleteByObjectID(ctx context.Context, objectID uuid.UUID) error
}

type ReconcilerV2 struct {
	sm    *statemachine.Transitioner
	probe StorageProbe
	cfg   ReconcilerV2Config
	log   *zap.Logger
}

func NewReconcilerV2(sm *statemachine.Transitioner, probe StorageProbe, cfg ReconcilerV2Config, log *zap.Logger) *ReconcilerV2 {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = DefaultReconcilerPollInterval
	}
	if cfg.PendingGraceTTL == 0 {
		cfg.PendingGraceTTL = DefaultReconcilerPendingGraceTTL
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = DefaultReconcilerBatchSize
	}
	return &ReconcilerV2{sm: sm, probe: probe, cfg: cfg, log: log}
}

// Run blocks until ctx is cancelled. Each tick:
//
//  1. scans PENDING-expired rows
//  2. HEADs the backend; if present → PromoteToAvailable, else → DiscardFailed
//
// Concurrent reconciler replicas both try to advance the same rows — both
// are safe because the underlying SQL uses state guards.
func (r *ReconcilerV2) Run(ctx context.Context) error {
	return RunTicker(ctx, "reconciler", r.cfg.PollInterval, r.tick)
}

// tick reconciles one batch, carrying on past an object that fails, and
// returns the first failure — the scan's, an object's, or the overdue sample's.
func (r *ReconcilerV2) tick(ctx context.Context) (first error) {
	// After the batch, so the gauges show what the tick could not settle.
	defer func() { keepFirst(&first, r.sampleOverdue(ctx)) }()
	ids, err := r.sm.ScanPendingExpired(ctx, r.cfg.PendingGraceTTL, r.cfg.BatchSize)
	if err != nil {
		r.log.Warn("failed to scan pending objects", zap.Error(err))
		return err
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return first
		}
		keepFirst(&first, r.reconcile(ctx, id))
	}
	return first
}

// sampleOverdue publishes how many PENDING objects are still past their
// deadline and how far past it the oldest is.
func (r *ReconcilerV2) sampleOverdue(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}
	count, oldest, err := r.sm.PendingOverdue(ctx, r.cfg.PendingGraceTTL)
	if err != nil {
		r.log.Warn("failed to sample overdue pending objects", zap.Error(err))
		return err
	}
	recordPendingOverdue(ctx, count, oldest)
	return nil
}

// reconcile settles one object and returns the failure it logged. A content
// mismatch is not one in itself: discardMismatched settles it.
func (r *ReconcilerV2) reconcile(ctx context.Context, objectID uuid.UUID) error {
	etag, size, checksum, seq, found, err := r.probe.HeadByObjectID(ctx, objectID)
	if err != nil {
		r.log.Warn("failed to HEAD object", zap.String("object_id", objectID.String()), zap.Error(err))
		return err
	}
	if found {
		_, err := r.sm.PromoteToAvailable(ctx, objectID, etag, size, checksum, seq, statemachine.SourceReconciler)
		if errors.Is(err, statemachine.ErrContentMismatch) {
			return r.discardMismatched(ctx, objectID, err)
		}
		if err != nil {
			r.log.Warn("failed to promote object", zap.String("object_id", objectID.String()), zap.Error(err))
		}
		return err
	}
	if err := r.sm.DiscardFailed(ctx, objectID, statemachine.FailedPresignExpired); err != nil {
		r.log.Warn("failed to discard object", zap.String("object_id", objectID.String()), zap.Error(err))
		return err
	}
	return nil
}

// discardMismatched settles an object whose stored bytes broke its
// registration: the bytes are deleted, then the row discarded. A failed delete
// leaves the row PENDING, so the next tick tries again rather than failing a
// row over bytes nothing will remove.
func (r *ReconcilerV2) discardMismatched(ctx context.Context, objectID uuid.UUID, cause error) error {
	log := r.log.With(zap.String("object_id", objectID.String()), zap.NamedError("mismatch", cause))
	if err := r.probe.DeleteByObjectID(ctx, objectID); err != nil {
		log.Warn("failed to delete mismatched object bytes", zap.Error(err))
		return err
	}
	if err := r.sm.DiscardFailed(ctx, objectID, statemachine.FailedContentMismatch); err != nil {
		log.Warn("failed to discard mismatched object", zap.Error(err))
		return err
	}
	// Settled: the Warn reports bad data a client sent, not a failed tick.
	log.Warn("discarded object whose stored bytes broke its registration")
	return nil
}
