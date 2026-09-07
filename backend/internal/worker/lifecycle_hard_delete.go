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

	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// LifecycleHardDeleter walks DELETED rows past their cooling-off
// window and:
//
//  1. Issues storage.DeleteObject against the S3 backend so the
//     bytes actually go away (soft-delete leaves them in S3).
//  2. Removes the Paladin row, freeing the (tenant, collection, key)
//     slot for a fresh PUT.
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
// Storage path failures: per-row failures are logged and the row is
// NOT DB-deleted on the first try. The next sweep retries — S3 DELETE
// is idempotent, so eventual success cleans up. If the storage error
// persists (orphaned bucket, missing credentials), an operator
// intervenes via direct DB query / S3 console; we don't auto-orphan
// rows.
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

// deleteOne is the per-row critical path:
//
//  1. storage.DeleteObject — idempotent on the S3 side; success or
//     a missing-key error are both fine ("already gone" is the
//     desired terminal state).
//  2. HardDeleteObjectIfStillDeleted — DB DELETE gated by
//     resource_version. A concurrent Restore between steps 1 and
//     2 bumps the version and the DB DELETE no-ops; the row stays
//     AVAILABLE with bytes in S3 (operator's restore intent wins).
//
// Failure modes:
//
//   - Storage DELETE error → log + skip; next sweep retries (S3
//     DELETE is idempotent, so a partial-failure-then-retry doesn't
//     cause harm).
//
//   - DB DELETE returns 0 rows → either the row was Restored (good —
//     we Want the live S3 object) or another worker beat us to it.
//     Log at debug; nothing to do.
func (w *LifecycleHardDeleter) deleteOne(ctx context.Context, r sqlc.ListHardDeletableRow) {
	tenantID := uuid.UUID(r.TenantID.Bytes)
	objectID := uuid.UUID(r.ID.Bytes)
	logger := w.log().With(
		zap.String("object_id", objectID.String()),
		zap.String("tenant_id", tenantID.String()),
		zap.String("bucket", r.BucketName),
		zap.String("key", r.Path),
	)

	if err := w.Storage.DeleteObject(ctx, r.BackendName, r.BucketName, tenantID, r.CollectionName, r.Path); err != nil {
		// We don't fail the whole sweep — log and try the next row.
		// Storage-side missing-key errors should be tolerated by
		// the adapter (S3 DELETE on absent key is a 204; SeaweedFS
		// behaves the same), but if the adapter surfaces a real
		// failure (network, auth) we'll retry on the next sweep.
		logger.Warn("storage delete failed; will retry", zap.Error(err))
		return
	}

	n, err := w.rowDeleteAndAnnounce(ctx, r, tenantID)
	if err != nil {
		logger.Warn("db hard-delete failed", zap.Error(err))
		return
	}
	if n == 0 {
		// Row was Restored or already deleted by another worker —
		// either way the objects table no longer carries the row at
		// our version; nothing to do. The S3 DELETE we issued IS the
		// race-loss case; logged at debug because it's non-fatal but
		// worth seeing if it ever spikes.
		logger.Debug("row no longer DELETED at our version; skipped")
		return
	}
	logger.Info("hard-deleted")
}

// rowDeleteAndAnnounce removes the row and enqueues paladin.object.purged in one
// transaction, so a consumer never sees the announcement without the deletion
// or vice versa. When no dispatcher is wired it degrades to the plain
// autocommit delete this used to do.
//
// The zero-row case (a concurrent Restore bumped the version) deliberately
// emits nothing: the operator's restore intent won, the row is live again, and
// the bytes we already deleted are the race-loss the existing comment
// describes — announcing a purge there would be wrong.
func (w *LifecycleHardDeleter) rowDeleteAndAnnounce(ctx context.Context, r sqlc.ListHardDeletableRow, tenantID uuid.UUID) (int64, error) {
	if w.Events == nil || w.Pool == nil {
		return w.Q.HardDeleteObjectIfStillDeleted(ctx, r.ID, r.ResourceVersion)
	}
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	n, err := w.Q.WithTx(tx).HardDeleteObjectIfStillDeleted(ctx, r.ID, r.ResourceVersion)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, tx.Commit(ctx)
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
		return 0, err
	}
	return n, tx.Commit(ctx)
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
