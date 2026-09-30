package adapters

import (
	"context"
	"fmt"

	"github.com/oleg-tkachuk/paladin/internal/safecast"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/internal/worker"
)

// StorageMigrationRepo adapts the sqlc queries to worker.MigrationRepo — the
// persistence seam for the ADR-0011 Phase 3 copy job. The raw pool is needed
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

func (r *StorageMigrationRepo) SetCopying(ctx context.Context, tenantID uuid.UUID, total int64) error {
	_, err := r.q.SetStorageMigrationTotal(ctx, pgUUID(tenantID), total)
	return err
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

func (r *StorageMigrationRepo) AdvanceCopy(ctx context.Context, tenantID uuid.UUID, copied int64, cursorCollection, cursorKey string) error {
	_, err := r.q.AdvanceStorageMigrationCopy(ctx, pgUUID(tenantID), copied, cursorCollection, cursorKey)
	return err
}

func (r *StorageMigrationRepo) SetState(ctx context.Context, tenantID uuid.UUID, state string) error {
	_, err := r.q.SetStorageMigrationState(ctx, pgUUID(tenantID), state)
	return err
}

// RebindTenant repoints every collection at the dedicated bucket and flips the
// tenant's storage_layout to 'dedicated' in ONE transaction. The collections→
// buckets FK is DEFERRABLE INITIALLY DEFERRED, and the target bucket already
// exists (provision_state='ready'), so the tenancy trigger and FK both pass.
func (r *StorageMigrationRepo) RebindTenant(ctx context.Context, tenantID uuid.UUID, targetBackendID, targetBucketName string) error {
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

	keys, err := qtx.MigrationListTenantCollections(ctx, pgUUID(tenantID))
	if err != nil {
		return fmt.Errorf("list collections: %w", err)
	}
	for _, ok := range keys {
		// expected_version 0 = skip optimistic-lock check; the migration owns
		// the whole tenant's binding during rebind.
		if _, err := qtx.BindCollectionToBucket(ctx, pgUUID(tenantID), ok, targetBackendID, targetBucketName, 0); err != nil {
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

func (r *StorageMigrationRepo) Complete(ctx context.Context, tenantID uuid.UUID) error {
	_, err := r.q.CompleteStorageMigration(ctx, pgUUID(tenantID))
	return err
}

func (r *StorageMigrationRepo) MarkCleaned(ctx context.Context, tenantID uuid.UUID) error {
	_, err := r.q.MarkStorageMigrationCleaned(ctx, pgUUID(tenantID))
	return err
}

func (r *StorageMigrationRepo) Fail(ctx context.Context, tenantID uuid.UUID, reason string) error {
	_, err := r.q.FailStorageMigration(ctx, pgUUID(tenantID), &reason)
	return err
}
