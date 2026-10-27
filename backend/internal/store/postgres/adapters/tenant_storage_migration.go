package adapters

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// Names are supplied by the caller, which has them from the join or from the
// arguments it just used to create the row.
func storageMigrationToDomain(m sqlc.TenantStorageMigration,
	srcBackend, srcBucket, tgtBackend, tgtBucket string,
) tenanth.StorageMigration {
	return tenanth.StorageMigration{
		TenantID:          uuidFrom(m.TenantID),
		SourceBackendName: srcBackend,
		SourceBucketName:  srcBucket,
		TargetBackendName: tgtBackend,
		TargetBucketName:  tgtBucket,
		State:             m.State,
		ObjectsTotal:      m.ObjectsTotal,
		ObjectsCopied:     m.ObjectsCopied,
		Error:             derefStr(m.Error),
	}
}

// StartStorageMigration provisions the tenant's dedicated bucket (pending,
// tenant-owned) and records the migration in one transaction — a new row, or,
// when the tenant's migration failed, that row reset to start over
// (tenanth.Handler.MigrateTenantStorageLayout says why that is the retry).
//
// Nothing in here may fail on purpose and carry on: a failed statement aborts
// the transaction, and every statement after it fails with 25P02. That is
// how a dedicated bucket left by an earlier attempt used to wedge every later
// start — its unique violation was "tolerated" and the insert after it
// failed. The bucket insert is ON CONFLICT DO NOTHING instead.
func (r *TenantRepo) StartStorageMigration(ctx context.Context, args tenanth.StartStorageMigrationArgs) (tenanth.StorageMigration, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return tenanth.StorageMigration{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := r.q.WithTx(tx)
	tenant := pgUUID(args.TenantID)

	// One start per tenant at a time: the schema has no unique key on
	// tenant_storage_migrations.tenant_id, so without this two concurrent
	// starts both insert, and two concurrent retries both reset. Under the
	// lock, the checks below see what the previous holder committed.
	layout, err := qtx.LockTenantForStorageMigration(ctx, tenant)
	if errors.Is(err, pgx.ErrNoRows) {
		return tenanth.StorageMigration{}, tenanth.ErrTenantNotShared
	} else if err != nil {
		return tenanth.StorageMigration{}, fmt.Errorf("lock tenant: %w", err)
	}
	if layout != sqlc.TenantStorageLayoutShared {
		// A migration rebound the tenant since the handler looked.
		return tenanth.StorageMigration{}, tenanth.ErrTenantNotShared
	}
	prev, err := qtx.LatestStorageMigration(ctx, tenant)
	retry := err == nil
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return tenanth.StorageMigration{}, fmt.Errorf("read storage migration: %w", err)
	case prev.State != tenanth.StorageMigrationFailed:
		return tenanth.StorageMigration{}, tenanth.ErrStorageMigrationExists
	}

	targetID, err := ensureMigrationTargetBucket(ctx, tx, qtx, args)
	if err != nil {
		return tenanth.StorageMigration{}, err
	}
	// A target whose provisioning failed — most likely why the attempt being
	// retried failed — gets a fresh attempt budget from the reconciler.
	if err := qtx.ResetFailedTargetBucketProvision(ctx, targetID); err != nil {
		return tenanth.StorageMigration{}, fmt.Errorf("reset dedicated bucket provisioning: %w", err)
	}

	var m sqlc.TenantStorageMigration
	if retry {
		m, err = qtx.RetryFailedStorageMigration(ctx, args.CleanupRetentionSeconds, prev.ID,
			args.SourceBackendName, args.SourceBucketName, args.TargetBackendName, args.TargetBucketName)
		if errors.Is(err, pgx.ErrNoRows) {
			// No longer 'failed' (or a bucket vanished): someone else's start won.
			return tenanth.StorageMigration{}, tenanth.ErrStorageMigrationExists
		}
	} else {
		m, err = qtx.CreateStorageMigration(ctx, tenant,
			args.SourceBackendName, args.SourceBucketName, args.TargetBackendName, args.TargetBucketName,
			args.CleanupRetentionSeconds)
	}
	if err != nil {
		return tenanth.StorageMigration{}, fmt.Errorf("record storage migration: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return tenanth.StorageMigration{}, err
	}
	return storageMigrationToDomain(m, args.SourceBackendName, args.SourceBucketName,
		args.TargetBackendName, args.TargetBucketName), nil
}

// ensureMigrationTargetBucket creates the tenant's dedicated bucket row, or
// finds the one an earlier attempt created, and returns its id. The name is
// derived from the tenant id, so it is predictable: a bucket by that name
// that this tenant does not exclusively own is refused, never adopted —
// taking it would put this tenant's objects where another tenant reads, or
// another tenant's storage under this one. "Exclusively" is checked across
// tenants, not trusted to the tenancy trigger: that trigger guards a
// collection's binding, but a bucket's owner can be changed after other
// tenants bound to it.
func ensureMigrationTargetBucket(ctx context.Context, tx pgx.Tx, qtx *sqlc.Queries, args tenanth.StartStorageMigrationArgs) (pgtype.UUID, error) {
	tenant := pgUUID(args.TenantID)
	if err := qtx.EnsureStorageMigrationTargetBucket(ctx, args.TargetBucketName, tenant, args.TargetBackendName); err != nil {
		return pgtype.UUID{}, fmt.Errorf("provision dedicated bucket: %w", err)
	}

	// The other tenants' collections and default bindings are what is being
	// looked for, and the connection is scoped to this tenant: widen the read
	// for this one statement (transaction-local, put back right after).
	// The policies admit the flag for reads only, never writes.
	if _, err := tx.Exec(ctx, `SELECT set_config('paladin.cross_tenant', 'on', true)`); err != nil {
		return pgtype.UUID{}, err
	}
	b, err := qtx.StorageMigrationTargetBucket(ctx, tenant, args.TargetBackendName, args.TargetBucketName)
	if _, resetErr := tx.Exec(ctx, `SELECT set_config('paladin.cross_tenant', '', true)`); resetErr != nil && err == nil {
		err = resetErr
	}
	if errors.Is(err, pgx.ErrNoRows) {
		// ON CONFLICT inserted nothing and there is nothing there: the
		// backend is unknown.
		return pgtype.UUID{}, fmt.Errorf("provision dedicated bucket: storage backend %q not found", args.TargetBackendName)
	} else if err != nil {
		return pgtype.UUID{}, fmt.Errorf("read dedicated bucket: %w", err)
	}

	switch {
	case !b.OwnerTenantID.Valid || b.OwnerTenantID != tenant:
		return pgtype.UUID{}, fmt.Errorf("%w: %s/%s is not owned by this tenant",
			tenanth.ErrTargetBucketUnusable, args.TargetBackendName, args.TargetBucketName)
	case b.UsedByOtherTenant:
		return pgtype.UUID{}, fmt.Errorf("%w: %s/%s is bound by another tenant",
			tenanth.ErrTargetBucketUnusable, args.TargetBackendName, args.TargetBucketName)
	case b.PublicRead:
		return pgtype.UUID{}, fmt.Errorf("%w: %s/%s is public",
			tenanth.ErrTargetBucketUnusable, args.TargetBackendName, args.TargetBucketName)
	case b.ProvisionState != "pending" && b.ProvisionState != "ready" && b.ProvisionState != "failed":
		return pgtype.UUID{}, fmt.Errorf("%w: %s/%s is %s",
			tenanth.ErrTargetBucketUnusable, args.TargetBackendName, args.TargetBucketName, b.ProvisionState)
	}
	return b.ID, nil
}

// TenantSourceBucket returns the single (backend, bucket) the tenant's
// private collections currently bind to. Shared tenants normally have exactly
// one; zero means no private collections (nothing to migrate from), more than
// one means the tenant spans buckets (unsupported in slice 1). Public
// collections are not counted: the migration leaves them in place.
func (r *TenantRepo) TenantSourceBucket(ctx context.Context, tenantID uuid.UUID) (string, string, error) {
	rows, err := r.q.TenantCollectionBuckets(ctx, pgUUID(tenantID))
	if err != nil {
		return "", "", err
	}
	switch len(rows) {
	case 0:
		return "", "", tenanth.ErrNoCollectionsToMigrate
	case 1:
		return rows[0].BackendName, rows[0].BucketName, nil
	default:
		return "", "", tenanth.ErrSourceBucketAmbiguous
	}
}

func (r *TenantRepo) GetStorageMigration(ctx context.Context, tenantID uuid.UUID) (tenanth.StorageMigration, error) {
	m, err := r.q.GetStorageMigration(ctx, pgUUID(tenantID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return tenanth.StorageMigration{}, tenanth.ErrNotFound
		}
		return tenanth.StorageMigration{}, err
	}
	return storageMigrationToDomain(m.TenantStorageMigration, m.SourceBackendName, m.SourceBucketName, m.TargetBackendName, m.TargetBucketName), nil
}

// RollBackStuckStorageMigration hands a failed migration's dedicated leftover
// to the worker's rollback (tenanth.Handler.rollBackStuck says which tenants
// those are, and why). Under the tenant lock StartStorageMigration takes, so
// it cannot interleave with a start or retry: the tenant must still be
// 'dedicated' and its latest migration 'failed', and the update itself
// requires the default binding to still name the migration's target bucket.
// The update also gives the row the rebound_at it predates (the query says
// how), from which the worker copies back.
func (r *TenantRepo) RollBackStuckStorageMigration(ctx context.Context, tenantID uuid.UUID) (tenanth.StorageMigration, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return tenanth.StorageMigration{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := r.q.WithTx(tx)
	tenant := pgUUID(tenantID)

	layout, err := qtx.LockTenantForStorageMigration(ctx, tenant)
	if errors.Is(err, pgx.ErrNoRows) {
		return tenanth.StorageMigration{}, tenanth.ErrStorageMigrationNotRollbackable
	} else if err != nil {
		return tenanth.StorageMigration{}, fmt.Errorf("lock tenant: %w", err)
	}
	if layout != sqlc.TenantStorageLayoutDedicated {
		return tenanth.StorageMigration{}, tenanth.ErrStorageMigrationNotRollbackable
	}
	prev, err := qtx.LatestStorageMigration(ctx, tenant)
	if errors.Is(err, pgx.ErrNoRows) {
		return tenanth.StorageMigration{}, tenanth.ErrStorageMigrationNotRollbackable
	} else if err != nil {
		return tenanth.StorageMigration{}, fmt.Errorf("read storage migration: %w", err)
	}
	if prev.State != tenanth.StorageMigrationFailed {
		return tenanth.StorageMigration{}, tenanth.ErrStorageMigrationNotRollbackable
	}
	if _, err := qtx.RollBackStuckStorageMigration(ctx, prev.ID); errors.Is(err, pgx.ErrNoRows) {
		// The default binding no longer names the target bucket.
		return tenanth.StorageMigration{}, tenanth.ErrStorageMigrationNotRollbackable
	} else if err != nil {
		return tenanth.StorageMigration{}, fmt.Errorf("start rollback: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return tenanth.StorageMigration{}, err
	}
	// Read back with the bucket names the response carries.
	return r.GetStorageMigration(ctx, tenantID)
}
