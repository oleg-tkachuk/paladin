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

	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/safecast"

	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// ReconcilerV2Config governs poll cadence and per-tick ceilings.
// ReconcilerV2Config defaults, for a field left zero.
const (
	DefaultReconcilerPollInterval    = 30 * time.Second
	DefaultReconcilerPendingGraceTTL = 2 * time.Hour
	// DefaultReconcilerSettleGrace matches the schema's
	// worker.jobs.reconciler.write_settle_grace, which says why.
	DefaultReconcilerSettleGrace = 5 * time.Minute
	DefaultReconcilerBatchSize   = 100
)

type ReconcilerV2Config struct {
	PollInterval time.Duration
	// PendingGraceTTL is how long past its URL's expiry a PENDING upload is
	// left to its client and the storage events before it is promoted or
	// discarded (worker.jobs.reconciler.min_object_age).
	PendingGraceTTL time.Duration
	// SettleGrace is how long past its URL's expiry a promoted object's
	// anchor is settled (worker.jobs.reconciler.write_settle_grace): the time
	// a write the store accepted before the URL expired may take to land.
	SettleGrace time.Duration
	BatchSize   int
}

// StorageProbe HEADs the backend to materialize authoritative etag/size. A
// narrow interface so unit tests don't need a full S3 client.
type StorageProbe interface {
	// HeadByObjectID reports the object's bytes, and placed, the bucket they
	// were found in, which the promote checks the collection is still bound to.
	HeadByObjectID(ctx context.Context, objectID uuid.UUID) (etag string, sizeBytes int64, checksum, sequencer string, placed statemachine.Placement, found bool, err error)
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
	if cfg.SettleGrace == 0 {
		cfg.SettleGrace = DefaultReconcilerSettleGrace
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = DefaultReconcilerBatchSize
	}
	return &ReconcilerV2{sm: sm, probe: probe, cfg: cfg, log: log}
}

// Run blocks until ctx is cancelled. Each tick:
//
//  1. scans the objects whose upload URL expired by the grace while their
//     row is still anchored to a bucket (objects.upload_bucket_id)
//  2. HEADs the anchor; a PENDING object is promoted or discarded, a
//     promoted one settled (settleAnchor)
//
// Concurrent reconciler replicas both try to advance the same rows — both
// are safe because the underlying SQL uses state guards.
func (r *ReconcilerV2) Run(ctx context.Context) error {
	return RunTicker(ctx, "reconciler", r.cfg.PollInterval, r.tick)
}

// RunOnce reconciles a single batch and returns its first failure. Run calls
// the same tick on a schedule; exposed for tests and any on-demand trigger.
func (r *ReconcilerV2) RunOnce(ctx context.Context) error { return r.tick(ctx) }

// tick reconciles one batch, carrying on past an object that fails, and
// returns the first failure — the scan's, an object's, or the overdue sample's.
// Only the objects in buckets the gate admits as a read are reconciled
// (anchorBucket); a refused bucket's objects stay as they are, logged once
// per tick, and the scan is taken again without them (bucket_gate.go).
func (r *ReconcilerV2) tick(ctx context.Context) (first error) {
	// After the batch, so the gauges show what the tick could not settle.
	defer func() { keepFirst(&first, r.sampleOverdue(ctx)) }()
	gate := newBucketDeferrals(r.log, bucketgate.Read)
	_, err := claimAdmitted(ctx, gate, safecast.Int32(r.cfg.BatchSize),
		func(limit int32, deferred []pgtype.UUID) ([]statemachine.ExpiredAnchor, error) {
			return r.sm.ScanExpiredAnchors(ctx, statemachine.AnchorGraces{
				Pending: r.cfg.PendingGraceTTL, Settle: r.cfg.SettleGrace,
			}, int(limit), deferred)
		},
		anchorBucket,
		func(p statemachine.ExpiredAnchor) error {
			if p.State == statemachine.StatePending {
				keepFirst(&first, r.reconcile(ctx, p.ObjectID))
			} else {
				keepFirst(&first, r.settleAnchor(ctx, p))
			}
			return nil
		})
	if err != nil {
		keepFirst(&first, tickCutShort(ctx, r.log, "failed to scan expired upload anchors", err))
	}
	return first
}

// anchorBucket is the bucket an object's bytes are HEADed in: the one its
// upload URL was signed for.
//
// The reconciler asks the gate as a read. Its HEAD reads the bytes, and what
// follows writes only the object's row: a promote of bytes the client already
// put, a discard of a row whose upload never came, or the record of a write
// the URL made. So a disabled backend defers its objects, which stay as they
// are until it is enabled, and a draining or provisioning one does not,
// though CompleteObject, which asks for the same promote, is a write of
// bytes: that RPC is the client's next step on an upload, the reconciler only
// settles one the client left. Deleting bytes that broke their registration
// is a write of bytes, gated on its own (ReconcilerProbe.DeleteByObjectID).
func anchorBucket(p statemachine.ExpiredAnchor) bucketGateRow {
	return bucketGateRow{
		ID: p.BucketID, BackendName: p.BackendName, BucketName: p.BucketName,
		Enabled: p.Enabled, ReadOnly: p.ReadOnly, ProvisionState: p.ProvisionState,
	}
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

// reconcile settles one PENDING object and returns the failure it logged. A
// content mismatch is not one in itself: discardMismatched settles it.
func (r *ReconcilerV2) reconcile(ctx context.Context, objectID uuid.UUID) error {
	etag, size, checksum, seq, placed, found, err := r.probe.HeadByObjectID(ctx, objectID)
	if err != nil {
		r.log.Warn("failed to HEAD object", zap.String("object_id", objectID.String()), zap.Error(err))
		return err
	}
	if found {
		_, err := r.sm.PromoteToAvailable(ctx, objectID, &placed, etag, size, checksum, seq, statemachine.SourceReconciler)
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

// settleAnchor settles a promoted object whose upload URL has expired by the
// grace (ADR-0051): no URL of it can write any more, so its anchor holds the
// last write. A HEAD there that finds the recorded ETag clears the anchor;
// another ETag is a write no storage event recorded, which the row now
// records — the last promoted wins; nothing there clears the anchor and is
// logged at Error. A storage migration moves only settled objects.
func (r *ReconcilerV2) settleAnchor(ctx context.Context, p statemachine.ExpiredAnchor) error {
	log := r.log.With(zap.String("object_id", p.ObjectID.String()),
		zap.String("backend", p.BackendName), zap.String("bucket", p.BucketName))
	etag, size, checksum, _, placed, found, err := r.probe.HeadByObjectID(ctx, p.ObjectID)
	if err != nil {
		log.Warn("failed to HEAD the object's upload anchor", zap.Error(err))
		return err
	}
	outcome, err := r.sm.SettleAnchorInTx(ctx, p.ObjectID, placed, statemachine.StoredWrite{
		Found: found, ETag: etag, SizeBytes: size, Checksum: checksum,
	}, nil)
	if err != nil {
		log.Warn("failed to settle the object's upload anchor", zap.Error(err))
		return err
	}
	switch outcome {
	case statemachine.SettleRecorded:
		log.Warn("upload anchor settled: its URL wrote after the object's last promote and no storage event reported it; the object now records those bytes",
			zap.String("etag", etag), zap.Int64("size_bytes", size))
	case statemachine.SettleMissing:
		log.Error("upload anchor settled: the bucket holds no bytes under the object's key")
	case statemachine.SettleUnchanged:
		log.Debug("upload anchor settled: the bucket holds the bytes the object records")
	case statemachine.SettleSkipped:
		log.Debug("upload anchor changed since the scan; nothing to settle")
	}
	return nil
}

// discardMismatched settles an object whose stored bytes broke its
// registration: the bytes are deleted, then the row discarded. A failed delete
// leaves the row PENDING, so the next tick tries again rather than failing a
// row over bytes nothing will remove.
//
// A delete the bucket gate refuses — a draining or disabled backend — is
// deferred the same way, and is not a failure: the row stays PENDING until a
// tick whose gate takes the delete.
func (r *ReconcilerV2) discardMismatched(ctx context.Context, objectID uuid.UUID, cause error) error {
	log := r.log.With(zap.String("object_id", objectID.String()), zap.NamedError("mismatch", cause))
	err := r.probe.DeleteByObjectID(ctx, objectID)
	if bucketgate.Refused(err) {
		log.Warn("the bucket gate refuses deleting the mismatched bytes; the object stays pending until it admits it",
			zap.String("reason", err.Error()))
		return nil
	}
	if err != nil {
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
