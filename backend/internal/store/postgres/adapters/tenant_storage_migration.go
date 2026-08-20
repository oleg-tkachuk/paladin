package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/tenant"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// Names are supplied by the caller, which has them from the join or from the
// arguments it just used to create the row.
func storageMigrationToDomain(m sqlc.TenantStorageMigration,
	srcBackend, srcBucket, tgtBackend, tgtBucket string,
) tenant.StorageMigration {
	return tenant.StorageMigration{
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
// tenant-owned) and inserts the migration row in one transaction. The bucket
// row may already exist from a prior attempt — that is tolerated; the migration
// row's PK (tenant_id) is the guard against double-starting.
func (r *TenantRepo) StartStorageMigration(ctx context.Context, args tenant.StartStorageMigrationArgs) (tenant.StorageMigration, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return tenant.StorageMigration{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := r.q.WithTx(tx)

	if err := qtx.CreateBucketV2(ctx,
		args.TargetBackendName, args.TargetBucketName,
		"", "", []byte("{}"), // display_name, region, labels
		pgUUID(args.TenantID), // owner_tenant_id
		"", []byte("{}"),      // cedar_policy, constraints
		"pending",
	); err != nil && !isUniqueViolation(err) {
		return tenant.StorageMigration{}, fmt.Errorf("provision dedicated bucket: %w", err)
	}

	m, err := qtx.CreateStorageMigration(ctx, pgUUID(args.TenantID),
		args.SourceBackendName, args.SourceBucketName, args.TargetBackendName, args.TargetBucketName,
		args.CleanupRetentionSeconds)
	if err != nil {
		if isUniqueViolation(err) {
			return tenant.StorageMigration{}, tenant.ErrStorageMigrationExists
		}
		return tenant.StorageMigration{}, fmt.Errorf("create storage migration: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return tenant.StorageMigration{}, err
	}
	return storageMigrationToDomain(m, args.SourceBackendName, args.SourceBucketName,
		args.TargetBackendName, args.TargetBucketName), nil
}

// TenantSourceBucket returns the single (backend, bucket) the tenant's
// collections currently bind to. Shared tenants normally have exactly one;
// zero means no collections (nothing to migrate from), more than one means the
// tenant spans buckets (unsupported in slice 1).
func (r *TenantRepo) TenantSourceBucket(ctx context.Context, tenantID uuid.UUID) (string, string, error) {
	rows, err := r.q.TenantCollectionBuckets(ctx, pgUUID(tenantID))
	if err != nil {
		return "", "", err
	}
	switch len(rows) {
	case 0:
		return "", "", tenant.ErrNotFound
	case 1:
		return rows[0].BackendName, rows[0].BucketName, nil
	default:
		return "", "", tenant.ErrSourceBucketAmbiguous
	}
}

func (r *TenantRepo) GetStorageMigration(ctx context.Context, tenantID uuid.UUID) (tenant.StorageMigration, error) {
	m, err := r.q.GetStorageMigration(ctx, pgUUID(tenantID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return tenant.StorageMigration{}, tenant.ErrNotFound
		}
		return tenant.StorageMigration{}, err
	}
	return storageMigrationToDomain(m.TenantStorageMigration, m.SourceBackendName, m.SourceBucketName, m.TargetBackendName, m.TargetBucketName), nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
