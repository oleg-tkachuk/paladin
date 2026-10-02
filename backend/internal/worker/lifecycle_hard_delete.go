package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// LifecycleHardDeleter walks DELETED rows past their cooling-off
// window and:
//
//  1. Removes the Paladin row, freeing the (tenant, collection, key)
//     slot for a fresh PUT, and records the bytes as a purge debt.
//  2. Deletes the bytes from the S3 backend (soft-delete leaves them
//     there) and settles the debt; PurgeDrainer retries a failure.
//
// Why the cooling-off matters:
//
//   - Restore-after-delete UX: ops sometimes mass-delete in error;
//     a 7-day window means the row is restorable until then.
//
//   - Race-with-PUT: the partial unique index `ux_objects_live_key`
//     prevents two non-DELETED rows at the same key, so a fresh PUT
//     post-delete creates a new row with a NEW object_id but the SAME
//     storage key (tenant/collection/key). The hard-delete path would
//     race the new PUT for that S3 path. Cooling-off makes that race
//     vanishingly improbable in practice; the OCC guard in
//     HardDeleteObjectIfStillDeleted catches the case where the row
//     was Restored mid-flight.
//
// Storage path failures: the row is already gone, and its bytes stay
// recorded in pending_purges, which PurgeDrainer retries with backoff.
//
// Disabled when TTL (the cooling-off period) is 0; the typical pattern
// is to leave hard-delete off in dev, on with a 7d window in prod.
type LifecycleHardDeleter struct {
	Q       *sqlc.Queries
	Pool    *pgxpool.Pool
	Storage StorageDeleter
	// Events is optional; when set, a confirmed byte-removal emits
	// paladin.object.purged. This is the other place in the system where bytes
	// actually disappear, so it carries the same obligation as PurgeDrainer:
	// paladin.object.deleted announces a state transition, paladin.object.purged
	// announces that the bytes are gone, and only the latter is emitted from
	// a path that has observed the storage delete succeed.
	Events    *Dispatcher
	TTL       time.Duration
	Interval  time.Duration
	BatchSize int32
	Logger    *zap.Logger
}

// StorageDeleter is the slice of the storage adapter the worker uses.
// Concrete impl is *s3adapter.Client; this seam keeps tests free of the
// AWS SDK.
type StorageDeleter interface {
	DeleteObject(
		ctx context.Context,
		backendID, bucket string,
		tenantID uuid.UUID,
		collection, key string,
	) error
}

// Run blocks until ctx is cancelled. Disabled (no-op) when TTL <= 0.
func (w *LifecycleHardDeleter) Run(ctx context.Context) error {
	if w.TTL <= 0 {
		return nil
	}
	if w.Interval <= 0 {
		w.Interval = 6 * time.Hour
	}
	if w.BatchSize <= 0 {
		w.BatchSize = 100
	}
	return RunTicker(ctx, "lifecycle_hard_delete", w.Interval, func(ctx context.Context) error {
		w.Sweep(ctx)
		return nil
	})
}

// Sweep drains as many hard-deletable rows as possible per tick, up
// to the configured cap. Loops while ListHardDeletable keeps returning
// rows so a backlog burns down without waiting for the next tick.
//
// Exported so integration tests can drive a single tick without
// running the full Run loop with its time.Ticker.
func (w *LifecycleHardDeleter) Sweep(ctx context.Context) {
	cutoff := pgtype.Timestamptz{Time: time.Now().Add(-w.TTL), Valid: true}
	for {
		if ctx.Err() != nil {
			return
		}
		rows, err := w.Q.ListHardDeletable(ctx, cutoff, w.BatchSize)
		if err != nil {
			w.log().Warn("list hard-deletable", zap.Error(err))
			return
		}
		if len(rows) == 0 {
			return
		}
		for _, r := range rows {
			if ctx.Err() != nil {
				return
			}
			w.deleteOne(ctx, r)
		}
		// If we pulled fewer than the batch, the queue is drained;
		// don't issue another no-op SELECT.
		if len(rows) < int(w.BatchSize) {
			return
		}
	}
}

// Reclaim hard-deletes one listed row. Exported so integration tests can drive
// a row they listed before changing it, which is the race Sweep cannot stage.
func (w *LifecycleHardDeleter) Reclaim(ctx context.Context, r sqlc.ListHardDeletableRow) {
	w.deleteOne(ctx, r)
}

// deleteOne removes the row before the bytes, never the other way round:
//
//  1. One transaction deletes the row — gated on state DELETED and the
//     resource_version that was listed — and records the bytes as a debt in
//     pending_purges. A Restore that won the race leaves the row at another
//     version: nothing is deleted and S3 is never asked.
//  2. Only then the bytes go. Success settles the debt and announces
//     paladin.object.purged on one transaction; failure leaves the debt for
//     PurgeDrainer, so the bytes are retried rather than forgotten.
//
// Deleting the bytes first, as this used to, stranded a Restore that landed
// between the two steps: an AVAILABLE row whose bytes were gone.
func (w *LifecycleHardDeleter) deleteOne(ctx context.Context, r sqlc.ListHardDeletableRow) {
	tenantID := uuid.UUID(r.TenantID.Bytes)
	objectID := uuid.UUID(r.ID.Bytes)
	logger := w.log().With(
		zap.String("object_id", objectID.String()),
		zap.String("tenant_id", tenantID.String()),
		zap.String("bucket", r.BucketName),
		zap.String("key", r.Path),
	)

	purgeID, err := uuid.NewV7()
	if err != nil {
		logger.Warn("purge id", zap.Error(err))
		return
	}
	deleted, err := w.deleteRowRecordingDebt(ctx, r, tenantID, purgeID)
	if err != nil {
		logger.Warn("db hard-delete failed", zap.Error(err))
		return
	}
	if !deleted {
		// Restored, or another worker got there first: the row is no
		// longer DELETED at the version we listed, and the bytes are
		// untouched.
		logger.Debug("row no longer DELETED at our version; skipped")
		return
	}

	if err := w.Storage.DeleteObject(ctx, r.BackendName, r.BucketName, tenantID, r.CollectionName, r.Path); err != nil {
		logger.Warn("storage delete failed; the purge drainer retries it", zap.Error(err))
		return
	}
	if err := w.settleAndAnnounce(ctx, r, tenantID, purgeID); err != nil {
		logger.Warn("settling the purge failed; the purge drainer retries it", zap.Error(err))
		return
	}
	logger.Info("hard-deleted")
}

// deleteRowRecordingDebt deletes the row and records its bytes in
// pending_purges on one transaction, so the bytes are never orphaned and never
// deleted under a row that is still live. Reports whether the row was deleted.
func (w *LifecycleHardDeleter) deleteRowRecordingDebt(ctx context.Context, r sqlc.ListHardDeletableRow, tenantID, purgeID uuid.UUID) (bool, error) {
	if w.Pool == nil {
		return false, errors.New("hard-deleter has no pool for its transaction")
	}
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := w.Q.WithTx(tx)
	n, err := q.HardDeleteObjectIfStillDeleted(ctx, r.ID, r.ResourceVersion)
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, tx.Commit(ctx)
	}
	if err := q.InsertPendingPurge(ctx,
		pgtype.UUID{Bytes: purgeID, Valid: true}, r.TenantID, r.ID,
		r.BackendName, r.BucketName, r.CollectionName, r.Path,
	); err != nil {
		return false, fmt.Errorf("record purge debt: %w", err)
	}
	return true, tx.Commit(ctx)
}

// settleAndAnnounce clears the debt the bytes' removal paid, and emits
// paladin.object.purged on the same transaction — but only if this call
// settled it: PurgeDrainer may have paid the same debt first and announced it.
func (w *LifecycleHardDeleter) settleAndAnnounce(ctx context.Context, r sqlc.ListHardDeletableRow, tenantID, purgeID uuid.UUID) error {
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	n, err := w.Q.WithTx(tx).DeletePendingPurge(ctx, pgtype.UUID{Bytes: purgeID, Valid: true})
	if err != nil {
		return err
	}
	if n == 0 || w.Events == nil {
		return tx.Commit(ctx)
	}
	objectID := uuid.UUID(r.ID.Bytes)
	if _, err := w.Events.DispatchTx(ctx, tx, tenantID.String(), Event{
		Type:     "paladin.object.purged",
		At:       time.Now().UTC(),
		TenantID: tenantID.String(),
		ResourceName: fmt.Sprintf("storageBackends/%s/buckets/%s/tenants/%s/collections/%s/objects-by-key/%s",
			r.BackendName, r.BucketName, tenantID, r.CollectionName, r.Path),
		Payload: map[string]any{
			"tenant_id":  tenantID.String(),
			"collection": r.CollectionName,
			"key":        r.Path,
			"object_id":  objectID.String(),
			"backend":    r.BackendName,
			"bucket":     r.BucketName,
			"reclaimed":  true,
			"source":     "lifecycle",
		},
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (w *LifecycleHardDeleter) log() *zap.Logger {
	if w.Logger == nil {
		return zap.NewNop()
	}
	return w.Logger
}

// Compile-time check: explicit so refactors of the storage adapter
// don't silently break the seam.
var _ = errors.New
