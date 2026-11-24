package adapters

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/safecast"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgerr"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// StorageMigrationRepo adapts the sqlc queries to worker.MigrationRepo — the
// persistence seam for the storage migration (ADR-0015, ADR-0051).
type StorageMigrationRepo struct {
	q    *sqlc.Queries
	pool *pgxpool.Pool
}

func NewStorageMigrationRepo(q *sqlc.Queries, pool *pgxpool.Pool) *StorageMigrationRepo {
	return &StorageMigrationRepo{q: q, pool: pool}
}

var _ worker.MigrationRepo = (*StorageMigrationRepo)(nil)

// The setters a migration's change of a tenant's default binding records.
const (
	setByMigration         = "storage-migration"
	setByMigrationRollback = "storage-migration-rollback"
)

// Names come from the joins, not the row: the row stores bucket ids, and the
// worker calls S3, which takes names.
func migFromSQLC(m sqlc.TenantStorageMigration,
	srcBackend, srcBucket, tgtBackend, tgtBucket string,
) worker.StorageMigration {
	return worker.StorageMigration{
		TenantID:         uuidFrom(m.TenantID),
		Source:           worker.MigrationBucket{ID: uuidFrom(m.SourceBucketID), BackendID: srcBackend, Bucket: srcBucket},
		Target:           worker.MigrationBucket{ID: uuidFrom(m.TargetBucketID), BackendID: tgtBackend, Bucket: tgtBucket},
		State:            m.State,
		ObjectsTotal:     m.ObjectsTotal,
		ObjectsCopied:    m.ObjectsCopied,
		CursorCollection: m.CursorCollection,
		CursorKey:        m.CursorPath,
		CleanupAfter:     m.CleanupAfter.Time, // zero when NULL (CleanupAfter.Valid == false)
		SettleAfter:      m.SettleAfter.Time,  // zero when NULL
		Attempts:         m.Attempts,
	}
}

func (r *StorageMigrationRepo) ListActive(ctx context.Context, limit int) ([]worker.StorageMigration, error) {
	rows, err := r.q.ListActiveStorageMigrations(ctx, safecast.Int32(limit))
	if err != nil {
		return nil, err
	}
	out := make([]worker.StorageMigration, 0, len(rows))
	for _, m := range rows {
		out = append(out, migFromSQLC(m.TenantStorageMigration, m.SourceBackendName, m.SourceBucketName, m.TargetBackendName, m.TargetBucketName))
	}
	return out, nil
}

func (r *StorageMigrationRepo) BucketProvisionState(ctx context.Context, backendID, bucketName string) (string, error) {
	b, err := r.q.GetBucketV2(ctx, backendID, bucketName)
	if err != nil {
		return "", err
	}
	return b.ProvisionState, nil
}

// withTenantTx runs fn inside a transaction whose `paladin.tenant_id` GUC is set to
// tenantID, so RLS-gated reads (objects, collections) see that tenant's rows.
// The worker pool wipes the GUC on every acquisition (no auth context), so the
// migration worker MUST set it explicitly — otherwise RLS returns nothing and
// the passes silently move nothing.
func (r *StorageMigrationRepo) withTenantTx(ctx context.Context, tenantID uuid.UUID, fn func(q *sqlc.Queries) error) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// set_config(..., true) = transaction-local; overrides the pool's per-acquire wipe.
	if _, err := tx.Exec(ctx, `SELECT set_config('paladin.tenant_id', $1, true)`, tenantID.String()); err != nil {
		return err
	}
	if err := fn(r.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// migrationMoved turns a transition that matched no row into
// worker.ErrMigrationMoved.
func migrationMoved(rows int64, err error) error {
	if err != nil {
		return err
	}
	if rows == 0 {
		return worker.ErrMigrationMoved
	}
	return nil
}

func (r *StorageMigrationRepo) EnterMoving(ctx context.Context, at worker.MigrationRead) error {
	return migrationMoved(r.q.SetStorageMigrationState(ctx, worker.MigStateMoving, pgUUID(at.TenantID), at.State, at.Attempts))
}

// Repoint points the tenant's uploads at to, under the migration row's lock,
// in one transaction (see worker.MigrationRepo). A collection the schema
// refuses to move comes back as a *worker.RebindRefusedError.
func (r *StorageMigrationRepo) Repoint(ctx context.Context, at worker.MigrationRead, from, to worker.MigrationBucket, layout string, writeWindow time.Duration) error {
	setBy := setByMigration
	if worker.Reverses(at.State) {
		setBy = setByMigrationRollback
	}
	return r.withTenantTx(ctx, at.TenantID, func(q *sqlc.Queries) error {
		if err := migrationMoved(q.LockStorageMigrationAsRead(ctx, pgUUID(at.TenantID), at.State, at.Attempts)); err != nil {
			return err
		}
		names, err := q.MigrationListBoundCollections(ctx, pgUUID(at.TenantID), pgUUID(from.ID))
		if err != nil {
			return fmt.Errorf("list collections bound to %s: %w", from.Bucket, err)
		}
		for _, name := range names {
			// expected_version 0 = skip the optimistic check: the migration
			// owns the tenant's bindings to the bucket it empties.
			if _, err := q.BindCollectionToBucket(ctx, pgUUID(at.TenantID), name, to.BackendID, to.Bucket, 0); err != nil {
				if pgerr.Is(err, pgerr.CheckViolation) {
					return worker.RebindRefused(name, err)
				}
				return fmt.Errorf("rebind %q: %w", name, err)
			}
		}
		if _, err := q.MigrationRepointDefaultBinding(ctx, pgUUID(at.TenantID), pgUUID(to.ID), setBy, pgUUID(from.ID)); err != nil {
			return fmt.Errorf("point the default binding at %s: %w", to.Bucket, err)
		}
		if _, err := q.SetTenantStorageLayout(ctx, pgUUID(at.TenantID), sqlc.TenantStorageLayout(layout)); err != nil {
			return fmt.Errorf("set storage_layout: %w", err)
		}
		return migrationMoved(q.MigrationCloseWrites(ctx, intervalOf(writeWindow), pgUUID(at.TenantID), at.State, at.Attempts))
	})
}

// intervalOf is d as Postgres reads an interval.
func intervalOf(d time.Duration) pgtype.Interval {
	return pgtype.Interval{Microseconds: d.Microseconds(), Valid: true}
}

func (r *StorageMigrationRepo) ListMovable(ctx context.Context, at worker.MigrationRead, from worker.MigrationBucket, after uuid.UUID, limit int) ([]worker.MoveCandidate, error) {
	var out []worker.MoveCandidate
	err := r.withTenantTx(ctx, at.TenantID, func(q *sqlc.Queries) error {
		rows, err := q.MigrationListMovable(ctx, pgUUID(at.TenantID), pgUUID(from.ID), pgUUID(after), safecast.Int32(limit))
		if err != nil {
			return err
		}
		out = make([]worker.MoveCandidate, 0, len(rows))
		for _, row := range rows {
			out = append(out, worker.MoveCandidate{
				ID: uuidFrom(row.ID), Collection: row.CollectionName, Key: row.Path,
				SizeBytes: row.SizeBytes, ETag: row.Etag, Trashed: row.Trashed,
				WrittenAt: timePtr(row.BytesWrittenAt),
			})
		}
		return nil
	})
	return out, err
}

// idleInTransactionUnbounded lifts idle_in_transaction_session_timeout for
// one transaction.
const idleInTransactionUnbounded = "0"

// MoveObject moves one object (see worker.MigrationRepo), on a transaction
// scoped to the tenant.
//
// The transaction is idle while fn copies, for as long as the object takes
// to copy, so the pool's idle-in-transaction timeout — which ends a session
// that leaves a transaction open with no work — is lifted for it: under it a
// copy longer than the timeout could never commit, and the migration would
// retry it forever. A purge waiting for the row is bounded by its own
// lock_timeout and retried; a dead worker's session ends with its connection.
//
// Locks: the object's row, then the migration's — the order a purge takes
// them in — so a transition, which takes the migration row FOR UPDATE, waits
// for the moves in flight.
func (r *StorageMigrationRepo) MoveObject(ctx context.Context, at worker.MigrationRead, from, to worker.MigrationBucket, c worker.MoveCandidate,
	fn func(context.Context, worker.MoveCandidate) (string, error),
) (bool, error) {
	var moved bool
	err := r.withTenantTx(ctx, at.TenantID, func(q *sqlc.Queries) error {
		if _, err := q.SetIdleInTransactionTimeout(ctx, idleInTransactionUnbounded); err != nil {
			return fmt.Errorf("lift the idle-in-transaction timeout: %w", err)
		}
		held, err := q.MigrationHoldMovable(ctx, pgUUID(c.ID), pgUUID(at.TenantID), pgUUID(from.ID))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // purged, moved or anchored again since it was listed
		}
		if err != nil {
			return fmt.Errorf("hold object: %w", err)
		}
		if err := migrationMoved(q.HoldStorageMigrationAsRead(ctx, pgUUID(at.TenantID), at.State, at.Attempts)); err != nil {
			return err
		}
		c.SizeBytes, c.ETag, c.Trashed, c.WrittenAt = held.SizeBytes, held.Etag, held.Trashed, timePtr(held.BytesWrittenAt)
		etag, err := fn(ctx, c)
		if err != nil {
			return err
		}
		n, err := q.MigrationPlaceMoved(ctx, pgUUID(to.ID), etag, pgUUID(c.ID), pgUUID(at.TenantID), pgUUID(from.ID), held.BytesWrittenAt)
		if err != nil {
			return fmt.Errorf("place the object in %s: %w", to.Bucket, err)
		}
		if n == 0 {
			return nil
		}
		moved = true
		return migrationMoved(q.CountStorageMigrationMove(ctx, pgUUID(at.TenantID), at.State, at.Attempts))
	})
	if err != nil {
		return false, err
	}
	return moved, nil
}

func (r *StorageMigrationRepo) Holds(ctx context.Context, at worker.MigrationRead, from worker.MigrationBucket) (worker.MigrationHolds, error) {
	var h worker.MigrationHolds
	err := r.withTenantTx(ctx, at.TenantID, func(q *sqlc.Queries) error {
		row, err := q.MigrationHolds(ctx, pgUUID(at.TenantID), pgUUID(from.ID), at.State, at.Attempts)
		if errors.Is(err, pgx.ErrNoRows) {
			return worker.ErrMigrationMoved
		}
		if err != nil {
			return err
		}
		h = worker.MigrationHolds{Placed: row.Placed, Anchored: row.Anchored, Sessions: row.Sessions,
			Bound: row.Bound}
		return nil
	})
	return h, err
}

func (r *StorageMigrationRepo) RecordPass(ctx context.Context, at worker.MigrationRead, failed bool, remaining int64) (int32, error) {
	n, err := r.q.RecordStorageMigrationPass(ctx, failed, remaining, pgUUID(at.TenantID), at.State, at.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, worker.ErrMigrationMoved
	}
	return n, err
}

func (r *StorageMigrationRepo) Complete(ctx context.Context, at worker.MigrationRead, getWindow time.Duration) error {
	return migrationMoved(r.q.CompleteStorageMigration(ctx, worker.MigStateCompleted, intervalOf(getWindow), pgUUID(at.TenantID), at.State, at.Attempts))
}

func (r *StorageMigrationRepo) ListCleanable(ctx context.Context, at worker.MigrationRead, target worker.MigrationBucket, afterCollection, afterKey string, limit int) ([]worker.ObjectRef, error) {
	var out []worker.ObjectRef
	err := r.withTenantTx(ctx, at.TenantID, func(q *sqlc.Queries) error {
		rows, err := q.MigrationListCleanable(ctx, pgUUID(at.TenantID), pgUUID(target.ID), afterCollection, afterKey, safecast.Int32(limit))
		if err != nil {
			return err
		}
		out = make([]worker.ObjectRef, 0, len(rows))
		for _, row := range rows {
			out = append(out, worker.ObjectRef{Collection: row.CollectionName, Key: row.Path})
		}
		return nil
	})
	return out, err
}

// AdvanceCleanup deletes one source copy and records the cursor past it, on
// a transaction holding the migration row as read (see worker.MigrationRepo).
// The transaction is idle while del deletes, so the idle-in-transaction
// timeout is lifted for it, as for a move.
func (r *StorageMigrationRepo) AdvanceCleanup(ctx context.Context, at worker.MigrationRead, collection, key string, del func(context.Context) error) error {
	return r.withTenantTx(ctx, at.TenantID, func(q *sqlc.Queries) error {
		if _, err := q.SetIdleInTransactionTimeout(ctx, idleInTransactionUnbounded); err != nil {
			return fmt.Errorf("lift the idle-in-transaction timeout: %w", err)
		}
		if err := migrationMoved(q.HoldStorageMigrationAsRead(ctx, pgUUID(at.TenantID), at.State, at.Attempts)); err != nil {
			return err
		}
		if err := del(ctx); err != nil {
			return err
		}
		return migrationMoved(q.AdvanceStorageMigrationCleanup(ctx, collection, key, pgUUID(at.TenantID), at.State, at.Attempts))
	})
}

func (r *StorageMigrationRepo) MarkCleaned(ctx context.Context, at worker.MigrationRead) error {
	return migrationMoved(r.q.MarkStorageMigrationCleaned(ctx, worker.MigStateCleaned, pgUUID(at.TenantID), at.State, at.Attempts))
}

func (r *StorageMigrationRepo) StartRollback(ctx context.Context, at worker.MigrationRead, reason string) error {
	return migrationMoved(r.q.StartStorageMigrationRollback(ctx, worker.MigStateRollingBack, &reason, pgUUID(at.TenantID), at.State, at.Attempts))
}

func (r *StorageMigrationRepo) FinishRollback(ctx context.Context, at worker.MigrationRead, settleWindow time.Duration) error {
	return migrationMoved(r.q.FinishStorageMigrationRollback(ctx, worker.MigStateFailed, intervalOf(settleWindow), pgUUID(at.TenantID), at.State, at.Attempts))
}

func (r *StorageMigrationRepo) Settle(ctx context.Context, at worker.MigrationRead) error {
	return migrationMoved(r.q.SettleStorageMigration(ctx, pgUUID(at.TenantID), at.State, at.Attempts))
}

func (r *StorageMigrationRepo) Fail(ctx context.Context, at worker.MigrationRead, reason string) error {
	return migrationMoved(r.q.FailStorageMigration(ctx, worker.MigStateFailed, &reason, pgUUID(at.TenantID), at.State, at.Attempts))
}
