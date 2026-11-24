package adapters

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/migstate"
	"github.com/oleg-tkachuk/paladin/backend/internal/safecast"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgerr"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// StorageMigrationRepo adapts the sqlc queries to worker.MigrationRepo — the
// persistence seam for the ADR-0015 Phase 3 copy job. The raw pool is needed
// for the transactional rebind (all collections + storage_layout flipped
// atomically under the DEFERRABLE FK).
type StorageMigrationRepo struct {
	q    *sqlc.Queries
	pool *pgxpool.Pool
}

func NewStorageMigrationRepo(q *sqlc.Queries, pool *pgxpool.Pool) *StorageMigrationRepo {
	return &StorageMigrationRepo{q: q, pool: pool}
}

var _ worker.MigrationRepo = (*StorageMigrationRepo)(nil)

// Names come from the joins, not the row: the row stores bucket ids, and the
// worker calls S3, which takes names.
func migFromSQLC(m sqlc.TenantStorageMigration,
	srcBackend, srcBucket, tgtBackend, tgtBucket string,
) worker.StorageMigration {
	return worker.StorageMigration{
		TenantID:         uuidFrom(m.TenantID),
		SourceBackendID:  srcBackend,
		SourceBucketName: srcBucket,
		TargetBackendID:  tgtBackend,
		TargetBucketName: tgtBucket,
		State:            m.State,
		ObjectsTotal:     m.ObjectsTotal,
		ObjectsCopied:    m.ObjectsCopied,
		CursorCollection: m.CursorCollection,
		CursorKey:        m.CursorPath,
		CleanupAfter:     m.CleanupAfter.Time, // zero when NULL (CleanupAfter.Valid == false)
		Attempts:         m.Attempts,
		ReboundAt:        timeFrom(m.ReboundAt),
		CopyStartedAt:    copyStartedAt(m),
	}
}

// copyStartedAt is when the migration's copy started; for a row from before
// migration 116, which has none, its creation, earlier than any copy of it.
func copyStartedAt(m sqlc.TenantStorageMigration) time.Time {
	if m.CopyStartedAt.Valid {
		return m.CopyStartedAt.Time
	}
	return m.CreatedAt.Time
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
// the copy silently no-ops while the rebind orphans the data.
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

// SetCopying holds the migration row, then counts the objects to copy and
// moves provisioning -> copying with that total, on one transaction (see
// worker.MigrationRepo): the count is taken under the lock a purge's
// transaction also takes (package purgedebt), so it sees every purge that
// got there first, and every later one finds 'copying'.
func (r *StorageMigrationRepo) SetCopying(ctx context.Context, at worker.MigrationRead) (int64, error) {
	var total int64
	err := r.withTenantTx(ctx, at.TenantID, func(q *sqlc.Queries) error {
		if err := migrationMoved(q.LockStorageMigrationForCount(ctx, pgUUID(at.TenantID), at.State, at.Attempts)); err != nil {
			return err
		}
		// This attempt copies everything again; what an earlier one caught
		// up says nothing about it.
		if err := q.ForgetStorageMigrationCaughtUp(ctx, pgUUID(at.TenantID)); err != nil {
			return fmt.Errorf("forget the last attempt's catch-up: %w", err)
		}
		if err := q.ForgetStorageMigrationSourceETags(ctx, pgUUID(at.TenantID)); err != nil {
			return fmt.Errorf("forget what the last attempt read of the source: %w", err)
		}
		n, err := q.MigrationCountTenantObjects(ctx, pgUUID(at.TenantID))
		if err != nil {
			return fmt.Errorf("count objects: %w", err)
		}
		total = n
		return migrationMoved(q.SetStorageMigrationTotal(ctx, n, worker.MigStateCopying, pgUUID(at.TenantID), at.State, at.Attempts))
	})
	return total, err
}

func (r *StorageMigrationRepo) ListObjects(ctx context.Context, tenantID uuid.UUID, afterCollection, afterKey string, limit int) ([]worker.ObjectRef, error) {
	var out []worker.ObjectRef
	err := r.withTenantTx(ctx, tenantID, func(q *sqlc.Queries) error {
		rows, e := q.MigrationListTenantObjects(ctx, pgUUID(tenantID), afterCollection, afterKey, safecast.Int32(limit))
		if e != nil {
			return e
		}
		out = make([]worker.ObjectRef, 0, len(rows))
		for _, o := range rows {
			out = append(out, worker.ObjectRef{Collection: o.CollectionName, Key: o.Path, SizeBytes: o.SizeBytes, Trashed: o.Trashed,
				BoundBackendID: o.BoundBackendName, BoundBucket: o.BoundBucketName,
				WrittenAt: timeFrom(o.WrittenAt), CaughtUp: caughtUpFrom(o.CaughtUpWrittenAt, o.CaughtUpEtag),
				SourceETag: o.SourceEtag})
		}
		return nil
	})
	return out, err
}

// caughtUpFrom is what the catch-up recorded of an object; nil when it
// recorded nothing.
func caughtUpFrom(writtenAt pgtype.Timestamptz, etag *string) *worker.CaughtUp {
	if !writtenAt.Valid {
		return nil
	}
	return &worker.CaughtUp{WrittenAt: writtenAt.Time, ETag: etag}
}

// ListUncaughtUp lists the objects the catch-up still has to copy (see
// worker.MigrationRepo).
func (r *StorageMigrationRepo) ListUncaughtUp(ctx context.Context, at worker.MigrationRead, limit int) ([]worker.WrittenObject, error) {
	var out []worker.WrittenObject
	err := r.withTenantTx(ctx, at.TenantID, func(q *sqlc.Queries) error {
		rows, e := q.MigrationListUncaughtUp(ctx, pgUUID(at.TenantID), at.State, at.Attempts, safecast.Int32(limit))
		if e != nil {
			return e
		}
		out = make([]worker.WrittenObject, 0, len(rows))
		for _, o := range rows {
			out = append(out, worker.WrittenObject{
				ObjectRef: worker.ObjectRef{Collection: o.CollectionName, Key: o.Path, SizeBytes: o.SizeBytes, Trashed: o.Trashed},
				ID:        uuidFrom(o.ID),
				WrittenAt: o.WrittenAt.Time,
			})
		}
		return nil
	})
	return out, err
}

// CatchUpObject holds o's row for fn, the catch-up's copy, and records on the
// same transaction the write it copied (see worker.MigrationRepo). The hold
// is FOR KEY SHARE, which a promote's FOR UPDATE waits on: the write read
// under it is the one whose bytes fn copies, or an older one, which leaves
// the object listed again. The transaction is idle while fn copies, so it
// lifts the idle-in-transaction timeout for itself, as HoldObject does.
func (r *StorageMigrationRepo) CatchUpObject(ctx context.Context, at worker.MigrationRead, o worker.WrittenObject, fn func(context.Context) (worker.CopyIdentity, error)) (bool, error) {
	var held bool
	err := r.withTenantTx(ctx, at.TenantID, func(q *sqlc.Queries) error {
		if _, err := q.SetIdleInTransactionTimeout(ctx, idleInTransactionUnbounded); err != nil {
			return fmt.Errorf("lift the idle-in-transaction timeout: %w", err)
		}
		writtenAt, err := q.MigrationHoldObjectForCatchUp(ctx, pgUUID(at.TenantID), pgUUID(o.ID))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("hold object: %w", err)
		}
		copied, err := fn(ctx)
		if err != nil {
			return err
		}
		held = true
		if err := recordSourceETag(ctx, q, at.TenantID, o.ObjectRef, copied.Source); err != nil {
			return err
		}
		return migrationMoved(q.RecordStorageMigrationCaughtUp(ctx, pgUUID(at.TenantID), pgUUID(o.ID), writtenAt, copied.Target,
			at.State, at.Attempts))
	})
	if err != nil {
		return false, err
	}
	return held, nil
}

func (r *StorageMigrationRepo) AdvanceCopy(ctx context.Context, at worker.MigrationRead, copied int64, cursorCollection, cursorKey string) error {
	return migrationMoved(r.q.AdvanceStorageMigrationCopy(ctx, copied, cursorCollection, cursorKey, pgUUID(at.TenantID), at.State, at.Attempts))
}

// idleInTransactionUnbounded is idle_in_transaction_session_timeout's "no
// timeout" (Postgres reads 0 as disabled), for the transaction HoldObject
// keeps open across a copy.
const idleInTransactionUnbounded = "0"

// HoldObject holds o's rows against a delete (FOR KEY SHARE) on a
// transaction scoped to the tenant, runs fn while they are held and records,
// on the same transaction, the source ETag fn returns and, with progress,
// the copy past o (see
// worker.MigrationRepo). No row runs nothing and reports false.
//
// The transaction is idle while fn copies, for as long as the object takes
// to copy, so the pool's idle-in-transaction timeout — which ends a session
// that leaves a transaction open with no work — is lifted for it: under it a
// copy longer than the timeout could never commit, and the migration would
// retry it forever. A purge waiting for the row is bounded by its own
// lock_timeout and retried; a dead worker's session ends with its connection.
func (r *StorageMigrationRepo) HoldObject(ctx context.Context, tenantID uuid.UUID, o worker.ObjectRef, progress *worker.CopyProgress, fn func(context.Context) (*string, error)) (bool, error) {
	var held bool
	err := r.withTenantTx(ctx, tenantID, func(q *sqlc.Queries) error {
		if _, err := q.SetIdleInTransactionTimeout(ctx, idleInTransactionUnbounded); err != nil {
			return fmt.Errorf("lift the idle-in-transaction timeout: %w", err)
		}
		ids, err := q.MigrationHoldObject(ctx, pgUUID(tenantID), o.Collection, o.Key)
		if err != nil {
			return fmt.Errorf("hold object: %w", err)
		}
		if len(ids) == 0 {
			return nil
		}
		sourceETag, err := fn(ctx)
		if err != nil {
			return err
		}
		held = true
		if err := recordSourceETag(ctx, q, tenantID, o, sourceETag); err != nil {
			return err
		}
		if progress == nil {
			return nil
		}
		return migrationMoved(q.AdvanceStorageMigrationCopy(ctx, progress.Copied, o.Collection, o.Key,
			pgUUID(tenantID), progress.At.State, progress.At.Attempts))
	})
	if err != nil {
		return false, err
	}
	return held, nil
}

// recordSourceETag records what a copy read of o's key in the source, on the
// transaction that holds o's rows; nil records nothing.
func recordSourceETag(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, o worker.ObjectRef, etag *string) error {
	if etag == nil {
		return nil
	}
	if err := q.RecordStorageMigrationSourceETag(ctx, o.Key, *etag, pgUUID(tenantID), o.Collection); err != nil {
		return fmt.Errorf("record the source's etag: %w", err)
	}
	return nil
}

func (r *StorageMigrationRepo) SetState(ctx context.Context, at worker.MigrationRead, to string) error {
	return migrationMoved(r.q.SetStorageMigrationState(ctx, to, pgUUID(at.TenantID), at.State, at.Attempts))
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

// RebindTenant repoints every private collection at the dedicated bucket and
// flips the tenant's storage_layout to 'dedicated' in ONE transaction. The
// collections→buckets FK is DEFERRABLE INITIALLY DEFERRED, and the target
// bucket already exists (provision_state='ready'), so the tenancy trigger and
// FK both pass. Public collections are not listed, so not moved (ADR-0027);
// should a bucket rule still refuse a collection, the check violation comes
// back as worker.ErrRebindRefused, which no retry would change.
//
// The migration row is held for the transaction, and only while it is still
// as the worker read it; otherwise nothing is rebound and the answer is
// worker.ErrMigrationMoved. A stale worker must not point the tenant at a
// bucket a newer attempt has not finished filling.
func (r *StorageMigrationRepo) RebindTenant(ctx context.Context, at worker.MigrationRead, targetBackendID, targetBucketName string) error {
	tenantID := at.TenantID
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Scope RLS to this tenant so the collections list isn't silently empty.
	if _, err := tx.Exec(ctx, `SELECT set_config('paladin.tenant_id', $1, true)`, tenantID.String()); err != nil {
		return err
	}
	qtx := r.q.WithTx(tx)

	if err := migrationMoved(qtx.LockStorageMigrationForRebind(ctx, pgUUID(tenantID), at.State, at.Attempts)); err != nil {
		return err
	}
	// Every promote holds the row while it runs, so the lock above waited for
	// the ones in flight and holds back the rest: this count sees every write
	// that will ever land in the source while the collections point there.
	behind, err := qtx.MigrationCountUncaughtUp(ctx, pgUUID(tenantID), at.State, at.Attempts)
	if err != nil {
		return fmt.Errorf("count objects written since the copy started: %w", err)
	}
	if behind > 0 {
		return &worker.CopyBehindError{Objects: behind}
	}
	// The rollback copies back what was written from here on.
	if err := migrationMoved(qtx.MarkStorageMigrationRebound(ctx, pgUUID(tenantID), at.State, at.Attempts)); err != nil {
		return fmt.Errorf("record rebound_at: %w", err)
	}
	if err := lockTenantBindings(ctx, qtx, tenantID); err != nil {
		return err
	}
	keys, err := qtx.MigrationListTenantCollections(ctx, pgUUID(tenantID))
	if err != nil {
		return fmt.Errorf("list collections: %w", err)
	}
	for _, ok := range keys {
		// expected_version 0 = skip optimistic-lock check; the migration owns
		// the whole tenant's binding during rebind.
		if _, err := qtx.BindCollectionToBucket(ctx, pgUUID(tenantID), ok, targetBackendID, targetBucketName, 0); err != nil {
			if pgerr.Is(err, pgerr.CheckViolation) {
				return worker.RebindRefused(ok, err)
			}
			return fmt.Errorf("rebind %q: %w", ok, err)
		}
	}
	if _, err := qtx.MigrationPlaceObjectsInTarget(ctx, pgUUID(tenantID)); err != nil {
		return fmt.Errorf("place the objects in the target: %w", err)
	}
	if _, err := qtx.SetTenantStorageLayout(ctx, pgUUID(tenantID), "dedicated"); err != nil {
		return fmt.Errorf("set storage_layout: %w", err)
	}
	// Point the default binding at the dedicated bucket too, so bare-name
	// collections created after the migration land there rather than failing.
	n, err := qtx.SetTenantDefaultBinding(ctx, pgUUID(tenantID), targetBackendID, targetBucketName, "storage-migration")
	if err != nil {
		return fmt.Errorf("set default binding: %w", err)
	}
	if n == 0 {
		// Rebinding to a bucket that does not exist would leave the tenant
		// pointing at its old storage after the data has moved.
		return fmt.Errorf("set default binding: bucket %q/%q not found",
			targetBackendID, targetBucketName)
	}
	return tx.Commit(ctx)
}

// lockTenantBindings holds the tenant's bindings lock for the rest of the
// move's transaction (migstate.LockSpaceBindings), before the move lists the
// tenant's collections: it waits for every collection being created, so the
// list holds it, and a creation after it sees the binding the move left
// (CollectionRepo.CreateTx).
func lockTenantBindings(ctx context.Context, qtx *sqlc.Queries, tenantID uuid.UUID) error {
	if err := qtx.LockTenantBindings(ctx, migstate.LockSpaceBindings, migstate.TenantLockKey(tenantID)); err != nil {
		return fmt.Errorf("wait for the collections being created: %w", err)
	}
	return nil
}

// Complete moves verifying -> completed and drops what the catch-up
// recorded, which only the verify reads.
func (r *StorageMigrationRepo) Complete(ctx context.Context, at worker.MigrationRead) error {
	return r.withTenantTx(ctx, at.TenantID, func(q *sqlc.Queries) error {
		if err := migrationMoved(q.CompleteStorageMigration(ctx, worker.MigStateCompleted, pgUUID(at.TenantID), at.State, at.Attempts)); err != nil {
			return err
		}
		return q.ForgetStorageMigrationCaughtUp(ctx, pgUUID(at.TenantID))
	})
}

// MarkCleaned moves completed -> cleaned and drops what the copies read of
// the source, which only the cleanup reads.
func (r *StorageMigrationRepo) MarkCleaned(ctx context.Context, at worker.MigrationRead) error {
	return r.withTenantTx(ctx, at.TenantID, func(q *sqlc.Queries) error {
		if err := migrationMoved(q.MarkStorageMigrationCleaned(ctx, worker.MigStateCleaned, pgUUID(at.TenantID), at.State, at.Attempts)); err != nil {
			return err
		}
		return q.ForgetStorageMigrationSourceETags(ctx, pgUUID(at.TenantID))
	})
}

// CountUploadsAnchoredTo counts the tenant's PENDING uploads whose URL names
// the bucket (objects.upload_bucket_id, migration 113).
func (r *StorageMigrationRepo) CountUploadsAnchoredTo(ctx context.Context, tenantID uuid.UUID, backendID, bucketName string) (int64, error) {
	var n int64
	err := r.withTenantTx(ctx, tenantID, func(q *sqlc.Queries) error {
		var e error
		n, e = q.MigrationCountUploadsAnchoredTo(ctx, pgUUID(tenantID), backendID, bucketName)
		return e
	})
	return n, err
}

func (r *StorageMigrationRepo) Fail(ctx context.Context, at worker.MigrationRead, reason string) error {
	return migrationMoved(r.q.FailStorageMigration(ctx, worker.MigStateFailed, &reason, pgUUID(at.TenantID), at.State, at.Attempts))
}

// StartRollback moves verifying -> rolling_back and drops what an earlier
// rollback of the tenant recorded it copied back: this one copies from
// rebound_at again.
func (r *StorageMigrationRepo) StartRollback(ctx context.Context, at worker.MigrationRead, reason string) error {
	return r.withTenantTx(ctx, at.TenantID, func(q *sqlc.Queries) error {
		if err := migrationMoved(q.StartStorageMigrationRollback(ctx, worker.MigStateRollingBack, &reason,
			pgUUID(at.TenantID), at.State, at.Attempts)); err != nil {
			return err
		}
		return q.ForgetStorageMigrationCopiedBack(ctx, pgUUID(at.TenantID))
	})
}

// ListUncopiedBack lists the objects the rollback still has to copy back
// (see worker.MigrationRepo).
func (r *StorageMigrationRepo) ListUncopiedBack(ctx context.Context, at worker.MigrationRead, limit int) ([]worker.WrittenObject, error) {
	var out []worker.WrittenObject
	err := r.withTenantTx(ctx, at.TenantID, func(q *sqlc.Queries) error {
		rows, e := q.MigrationListUncopiedBack(ctx, pgUUID(at.TenantID), at.State, at.Attempts, safecast.Int32(limit))
		if e != nil {
			return e
		}
		out = make([]worker.WrittenObject, 0, len(rows))
		for _, o := range rows {
			out = append(out, worker.WrittenObject{
				ObjectRef: worker.ObjectRef{Collection: o.CollectionName, Key: o.Path, SizeBytes: o.SizeBytes},
				ID:        uuidFrom(o.ID),
				WrittenAt: o.WrittenAt.Time,
			})
		}
		return nil
	})
	return out, err
}

// CopyBackObject holds o's row (FOR KEY SHARE, which a promote's FOR UPDATE
// waits on) and then the migration row as read (FOR SHARE, the order a
// promote takes them in), runs fn — the copy back — and records on the same
// transaction the write it took (see worker.MigrationRepo). The migration
// row is held before the copy, not checked after it: once the tenant is on
// its source again, a stale worker's copy from the target would put older
// bytes over what was written there since. The transaction is idle while fn
// copies, so it lifts the idle-in-transaction timeout for itself, as
// HoldObject does.
func (r *StorageMigrationRepo) CopyBackObject(ctx context.Context, at worker.MigrationRead, o worker.WrittenObject, fn func(context.Context) error) (bool, error) {
	var held bool
	err := r.withTenantTx(ctx, at.TenantID, func(q *sqlc.Queries) error {
		if _, err := q.SetIdleInTransactionTimeout(ctx, idleInTransactionUnbounded); err != nil {
			return fmt.Errorf("lift the idle-in-transaction timeout: %w", err)
		}
		writtenAt, err := q.MigrationHoldObjectForCopyBack(ctx, pgUUID(at.TenantID), pgUUID(o.ID))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("hold object: %w", err)
		}
		if err := migrationMoved(q.HoldStorageMigrationForCopyBack(ctx, pgUUID(at.TenantID), at.State, at.Attempts)); err != nil {
			return err
		}
		if err := fn(ctx); err != nil {
			return err
		}
		held = true
		return q.RecordStorageMigrationCopiedBack(ctx, pgUUID(at.TenantID), pgUUID(o.ID), writtenAt)
	})
	if err != nil {
		return false, err
	}
	return held, nil
}

// RebindBack is RebindTenant's inverse, and the end of a rollback: in ONE
// transaction, holding the migration row only while it is still as the
// worker read it (rolling_back, same attempt), it repoints every private
// collection at the source bucket, flips storage_layout to 'shared', points
// the default binding at the source bucket and moves the migration to
// 'failed', keeping the verify error. Public collections never moved
// (ADR-0027) and are left alone. Any error rolls all of it back, so the
// tenant is never half rebound; the worker retries next tick.
//
// Before any of that, under the migration row's lock — which every promote
// and every copy back holds while it runs — it counts the objects
// ListUncopiedBack would still list: such an object is only in the target,
// and rebinding now would lose it. Any is worker.ErrRollbackBehind, and
// nothing changes; the worker copies it first. What the rollback recorded is
// dropped once the tenant is back.
func (r *StorageMigrationRepo) RebindBack(ctx context.Context, at worker.MigrationRead, sourceBackendID, sourceBucketName string) error {
	tenantID := at.TenantID
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('paladin.tenant_id', $1, true)`, tenantID.String()); err != nil {
		return err
	}
	qtx := r.q.WithTx(tx)

	if err := migrationMoved(qtx.LockStorageMigrationForRebind(ctx, pgUUID(tenantID), at.State, at.Attempts)); err != nil {
		return err
	}
	behind, err := qtx.MigrationCountUncopiedBack(ctx, pgUUID(tenantID), at.State, at.Attempts)
	if err != nil {
		return fmt.Errorf("count objects not copied back: %w", err)
	}
	if behind > 0 {
		return fmt.Errorf("%w: %d objects", worker.ErrRollbackBehind, behind)
	}
	if err := lockTenantBindings(ctx, qtx, tenantID); err != nil {
		return err
	}
	keys, err := qtx.MigrationListTenantCollections(ctx, pgUUID(tenantID))
	if err != nil {
		return fmt.Errorf("list collections: %w", err)
	}
	for _, ok := range keys {
		if _, err := qtx.BindCollectionToBucket(ctx, pgUUID(tenantID), ok, sourceBackendID, sourceBucketName, 0); err != nil {
			return fmt.Errorf("rebind %q back: %w", ok, err)
		}
	}
	if _, err := qtx.MigrationPlaceObjectsInSource(ctx, pgUUID(tenantID)); err != nil {
		return fmt.Errorf("place the objects in the source: %w", err)
	}
	if _, err := qtx.SetTenantStorageLayout(ctx, pgUUID(tenantID), "shared"); err != nil {
		return fmt.Errorf("set storage_layout: %w", err)
	}
	// The migration pointed the default binding at the target; bare-name
	// collections created from here on must land in the source again.
	n, err := qtx.SetTenantDefaultBinding(ctx, pgUUID(tenantID), sourceBackendID, sourceBucketName, "storage-migration-rollback")
	if err != nil {
		return fmt.Errorf("set default binding: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("set default binding: bucket %q/%q not found", sourceBackendID, sourceBucketName)
	}
	if err := migrationMoved(qtx.FinishStorageMigrationRollback(ctx, worker.MigStateFailed, pgUUID(tenantID), at.State, at.Attempts)); err != nil {
		return err
	}
	if err := qtx.ForgetStorageMigrationCopiedBack(ctx, pgUUID(tenantID)); err != nil {
		return fmt.Errorf("forget what the rollback copied back: %w", err)
	}
	if err := qtx.ForgetStorageMigrationSourceETags(ctx, pgUUID(tenantID)); err != nil {
		return fmt.Errorf("forget what the copies read of the source: %w", err)
	}
	return tx.Commit(ctx)
}
