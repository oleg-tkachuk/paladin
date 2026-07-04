package adapters

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/internal/worker"
)

// StorageMigrationRepo adapts the sqlc queries to worker.MigrationRepo — the
// persistence seam for the ADR-0011 Phase 3 copy job. The raw pool is needed
// for the transactional rebind (all object_keys + storage_layout flipped
// atomically under the DEFERRABLE FK).
type StorageMigrationRepo struct {
	q    *sqlc.Queries
	pool *pgxpool.Pool
}

func NewStorageMigrationRepo(q *sqlc.Queries, pool *pgxpool.Pool) *StorageMigrationRepo {
	return &StorageMigrationRepo{q: q, pool: pool}
}

var _ worker.MigrationRepo = (*StorageMigrationRepo)(nil)

func migFromSQLC(m sqlc.TenantStorageMigration) worker.StorageMigration {
	return worker.StorageMigration{
		TenantID:         uuidFrom(m.TenantID),
		SourceBackendID:  m.SourceBackendID,
		SourceBucketName: m.SourceBucketName,
		TargetBackendID:  m.TargetBackendID,
		TargetBucketName: m.TargetBucketName,
		State:            m.State,
		ObjectsTotal:     m.ObjectsTotal,
		ObjectsCopied:    m.ObjectsCopied,
		CursorObjectKey:  m.CursorObjectKey,
		CursorKey:        m.CursorKey,
	}
}

func (r *StorageMigrationRepo) ListActive(ctx context.Context, limit int) ([]worker.StorageMigration, error) {
	rows, err := r.q.ListActiveStorageMigrations(ctx, int32(limit))
	if err != nil {
		return nil, err
	}
	out := make([]worker.StorageMigration, 0, len(rows))
	for _, m := range rows {
		out = append(out, migFromSQLC(m))
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

func (r *StorageMigrationRepo) CountObjects(ctx context.Context, tenantID uuid.UUID) (int64, error) {
	return r.q.MigrationCountTenantObjects(ctx, pgUUID(tenantID))
}

func (r *StorageMigrationRepo) SetCopying(ctx context.Context, tenantID uuid.UUID, total int64) error {
	_, err := r.q.SetStorageMigrationTotal(ctx, pgUUID(tenantID), total)
	return err
}

func (r *StorageMigrationRepo) ListObjects(ctx context.Context, tenantID uuid.UUID, afterObjectKey, afterKey string, limit int) ([]worker.ObjectRef, error) {
	rows, err := r.q.MigrationListTenantObjects(ctx, pgUUID(tenantID), afterObjectKey, afterKey, int32(limit))
	if err != nil {
		return nil, err
	}
	out := make([]worker.ObjectRef, 0, len(rows))
	for _, o := range rows {
		out = append(out, worker.ObjectRef{ObjectKey: o.ObjectKey, Key: o.Key})
	}
	return out, nil
}

func (r *StorageMigrationRepo) AdvanceCopy(ctx context.Context, tenantID uuid.UUID, copied int64, cursorObjectKey, cursorKey string) error {
	_, err := r.q.AdvanceStorageMigrationCopy(ctx, pgUUID(tenantID), copied, cursorObjectKey, cursorKey)
	return err
}

func (r *StorageMigrationRepo) SetState(ctx context.Context, tenantID uuid.UUID, state string) error {
	_, err := r.q.SetStorageMigrationState(ctx, pgUUID(tenantID), state)
	return err
}

// RebindTenant repoints every object_key at the dedicated bucket and flips the
// tenant's storage_layout to 'dedicated' in ONE transaction. The object_keys→
// buckets FK is DEFERRABLE INITIALLY DEFERRED, and the target bucket already
// exists (provision_state='ready'), so the tenancy trigger and FK both pass.
func (r *StorageMigrationRepo) RebindTenant(ctx context.Context, tenantID uuid.UUID, targetBackendID, targetBucketName string) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := r.q.WithTx(tx)

	keys, err := qtx.MigrationListTenantObjectKeys(ctx, pgUUID(tenantID))
	if err != nil {
		return fmt.Errorf("list object_keys: %w", err)
	}
	for _, ok := range keys {
		// expected_version 0 = skip optimistic-lock check; the migration owns
		// the whole tenant's binding during rebind.
		if _, err := qtx.BindObjectKeyToBucket(ctx, pgUUID(tenantID), ok, targetBackendID, targetBucketName, 0); err != nil {
			return fmt.Errorf("rebind %q: %w", ok, err)
		}
	}
	if _, err := qtx.SetTenantStorageLayout(ctx, pgUUID(tenantID), "dedicated"); err != nil {
		return fmt.Errorf("set storage_layout: %w", err)
	}
	// Point the default binding at the dedicated bucket too, so bare-name
	// object_keys created after the migration land there rather than failing.
	if err := qtx.SetTenantDefaultBinding(ctx, pgUUID(tenantID), targetBackendID, targetBucketName, "storage-migration"); err != nil {
		return fmt.Errorf("set default binding: %w", err)
	}
	return tx.Commit(ctx)
}

func (r *StorageMigrationRepo) Complete(ctx context.Context, tenantID uuid.UUID) error {
	_, err := r.q.CompleteStorageMigration(ctx, pgUUID(tenantID))
	return err
}

func (r *StorageMigrationRepo) Fail(ctx context.Context, tenantID uuid.UUID, reason string) error {
	_, err := r.q.FailStorageMigration(ctx, pgUUID(tenantID), reason)
	return err
}
