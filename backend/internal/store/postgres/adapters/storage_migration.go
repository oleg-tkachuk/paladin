package adapters

import (
	"context"
	"fmt"
	"time"
	"uuid"

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
		RollbackCursorAt: timeFrom(m.RollbackCursorAt),
		RollbackCursorID: uuidFrom(m.RollbackCursorID),
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

func (r *StorageMigrationRepo) CountObjects(ctx context.Context, tenantID uuid.UUID) (int64, error) {
	var n int64
	err := r.withTenantTx(ctx, tenantID, func(q *sqlc.Queries) error {
		var e error
		n, e = q.MigrationCountTenantObjects(ctx, pgUUID(tenantID))
		return e
	})
	return n, err
}

func (r *StorageMigrationRepo) SetCopying(ctx context.Context, at worker.MigrationRead, total int64) error {
	return migrationMoved(r.q.SetStorageMigrationTotal(ctx, total, worker.MigStateCopying, pgUUID(at.TenantID), at.State, at.Attempts))
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
			out = append(out, worker.ObjectRef{Collection: o.CollectionName, Key: o.Path, SizeBytes: o.SizeBytes})
		}
		return nil
	})
	return out, err
}

func (r *StorageMigrationRepo) AdvanceCopy(ctx context.Context, at worker.MigrationRead, copied int64, cursorCollection, cursorKey string) error {
	return migrationMoved(r.q.AdvanceStorageMigrationCopy(ctx, copied, cursorCollection, cursorKey, pgUUID(at.TenantID), at.State, at.Attempts))
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
	// The rollback copies back what was written from here on.
	if err := migrationMoved(qtx.MarkStorageMigrationRebound(ctx, pgUUID(tenantID), at.State, at.Attempts)); err != nil {
		return fmt.Errorf("record rebound_at: %w", err)
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
				return fmt.Errorf("rebind %q: %w: %w", ok, worker.ErrRebindRefused, err)
			}
			return fmt.Errorf("rebind %q: %w", ok, err)
		}
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

func (r *StorageMigrationRepo) Complete(ctx context.Context, at worker.MigrationRead) error {
	return migrationMoved(r.q.CompleteStorageMigration(ctx, worker.MigStateCompleted, pgUUID(at.TenantID), at.State, at.Attempts))
}

func (r *StorageMigrationRepo) MarkCleaned(ctx context.Context, at worker.MigrationRead) error {
	return migrationMoved(r.q.MarkStorageMigrationCleaned(ctx, worker.MigStateCleaned, pgUUID(at.TenantID), at.State, at.Attempts))
}

func (r *StorageMigrationRepo) Fail(ctx context.Context, at worker.MigrationRead, reason string) error {
	return migrationMoved(r.q.FailStorageMigration(ctx, worker.MigStateFailed, &reason, pgUUID(at.TenantID), at.State, at.Attempts))
}

func (r *StorageMigrationRepo) StartRollback(ctx context.Context, at worker.MigrationRead, reason string) error {
	return migrationMoved(r.q.StartStorageMigrationRollback(ctx, worker.MigStateRollingBack, &reason,
		pgUUID(at.TenantID), at.State, at.Attempts))
}

// cursorTS and cursorID pass a rollback cursor as a value, never NULL: the
// cursor starts at (rebound_at, the nil uuid), and pgTS / pgUUID map a zero
// time and the nil uuid to NULL, against which every row comparison is NULL —
// an empty list, and a rollback that rebinds back without copying anything.
func cursorTS(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
func cursorID(u uuid.UUID) pgtype.UUID        { return pgtype.UUID{Bytes: u, Valid: true} }

func (r *StorageMigrationRepo) ListWrittenSince(ctx context.Context, tenantID uuid.UUID, afterAt time.Time, afterID uuid.UUID, limit int) ([]worker.WrittenObject, error) {
	var out []worker.WrittenObject
	err := r.withTenantTx(ctx, tenantID, func(q *sqlc.Queries) error {
		rows, e := q.MigrationListObjectsWrittenSince(ctx, pgUUID(tenantID), cursorTS(afterAt), cursorID(afterID), safecast.Int32(limit))
		if e != nil {
			return e
		}
		out = make([]worker.WrittenObject, 0, len(rows))
		for _, o := range rows {
			out = append(out, worker.WrittenObject{
				ObjectRef: worker.ObjectRef{Collection: o.CollectionName, Key: o.Path, SizeBytes: o.SizeBytes},
				ID:        uuidFrom(o.ID),
				UpdatedAt: o.UpdatedAt.Time,
			})
		}
		return nil
	})
	return out, err
}

func (r *StorageMigrationRepo) AdvanceRollback(ctx context.Context, at worker.MigrationRead, cursorAt time.Time, id uuid.UUID) error {
	return migrationMoved(r.q.AdvanceStorageMigrationRollback(ctx, cursorTS(cursorAt), cursorID(id),
		pgUUID(at.TenantID), at.State, at.Attempts))
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
// Before any of that it asks whether an object was written past the cursor
// since the worker's last batch: such an object is only in the target, and
// rebinding now would lose it. The answer is then worker.ErrRollbackBehind
// and nothing changes; the worker copies it first.
func (r *StorageMigrationRepo) RebindBack(ctx context.Context, at worker.MigrationRead, cursorAt time.Time, afterID uuid.UUID, sourceBackendID, sourceBucketName string) error {
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
	behind, err := qtx.MigrationObjectsWrittenSinceExist(ctx, pgUUID(tenantID), cursorTS(cursorAt), cursorID(afterID))
	if err != nil {
		return fmt.Errorf("check for objects past the rollback cursor: %w", err)
	}
	if behind {
		return worker.ErrRollbackBehind
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
	return tx.Commit(ctx)
}
