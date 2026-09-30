package adapters

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

type QuotaRepoV2 struct {
	q    *sqlc.Queries
	pool *pgxpool.Pool
}

func NewQuotaRepoV2(q *sqlc.Queries, pool *pgxpool.Pool) *QuotaRepoV2 {
	return &QuotaRepoV2{q: q, pool: pool}
}

var _ admindomain.QuotaRepository = (*QuotaRepoV2)(nil)

// RunInTx runs fn in one transaction — the ADR-0003 seam the quota handler
// uses to write the upsert and its paladin.quota.set outbox rows atomically.
func (r *QuotaRepoV2) RunInTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("quota: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("quota: commit tx: %w", err)
	}
	return nil
}

func (r *QuotaRepoV2) UpsertTenant(ctx context.Context, q admindomain.Quota) error {
	return upsertTenantQuota(ctx, r.q, q)
}

// UpsertTenantTx runs UpsertTenant on the caller's tx (ADR-0003).
func (r *QuotaRepoV2) UpsertTenantTx(ctx context.Context, tx pgx.Tx, q admindomain.Quota) error {
	return upsertTenantQuota(ctx, r.q.WithTx(tx), q)
}

func upsertTenantQuota(ctx context.Context, qq *sqlc.Queries, q admindomain.Quota) error {
	if q.QuotaID == uuid.Nil {
		q.QuotaID = uuid.Must(uuid.NewV7())
	}
	_, err := qq.UpsertTenantQuota(ctx,
		pgUUID(q.QuotaID),
		pgUUID(q.TenantID),
		q.MaxTotalBytes,
		q.MaxObjectCount,
		q.MaxBytesPerDay,
		q.MaxObjectsPerDay,
		q.ResourceVersion,
	)
	return mapQuotaOCCErr(err)
}

func (r *QuotaRepoV2) UpsertBucket(ctx context.Context, q admindomain.Quota) error {
	return upsertBucketQuota(ctx, r.q, q)
}

// UpsertBucketTx runs UpsertBucket on the caller's tx (ADR-0003).
func (r *QuotaRepoV2) UpsertBucketTx(ctx context.Context, tx pgx.Tx, q admindomain.Quota) error {
	return upsertBucketQuota(ctx, r.q.WithTx(tx), q)
}

func upsertBucketQuota(ctx context.Context, qq *sqlc.Queries, q admindomain.Quota) error {
	if q.QuotaID == uuid.Nil {
		q.QuotaID = uuid.Must(uuid.NewV7())
	}
	_, err := qq.UpsertBucketQuota(ctx,
		pgUUID(q.QuotaID),
		q.BackendID,
		q.BucketName,
		q.MaxTotalBytes,
		q.MaxObjectCount,
		q.MaxBytesPerDay,
		q.MaxObjectsPerDay,
		q.ResourceVersion,
	)
	return mapQuotaOCCErr(err)
}

// mapQuotaOCCErr turns "no row came back" into the OCC conflict it means.
//
// The upsert RETURNINGs a row only when the INSERT fired or the DO UPDATE's
// version guard held. No row means the row exists and its resource_version is
// not what the caller passed — a concurrent writer got there first, or the
// caller sent 0 for a row that already exists.
func mapQuotaOCCErr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return admindomain.ErrVersionMismatch
	}
	return err
}

func (r *QuotaRepoV2) GetTenant(ctx context.Context, tenantID uuid.UUID) (admindomain.Quota, error) {
	row, err := r.q.GetTenantQuota(ctx, pgUUID(tenantID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return admindomain.Quota{}, admindomain.ErrNotFound
		}
		return admindomain.Quota{}, err
	}
	return quotaFromSQLC(row.Quota, row.BackendName, row.BucketName), nil
}

func (r *QuotaRepoV2) GetBucket(ctx context.Context, backendID, bucketName string) (admindomain.Quota, error) {
	return getBucketQuota(ctx, r.q, backendID, bucketName)
}

// GetBucketTx reads the bucket quota on the caller's tx so the handler can
// resolve the owner tenant_id (the fan-out target) inside the upsert tx.
func (r *QuotaRepoV2) GetBucketTx(ctx context.Context, tx pgx.Tx, backendID, bucketName string) (admindomain.Quota, error) {
	return getBucketQuota(ctx, r.q.WithTx(tx), backendID, bucketName)
}

func getBucketQuota(ctx context.Context, qq *sqlc.Queries, backendID, bucketName string) (admindomain.Quota, error) {
	row, err := qq.GetBucketQuota(ctx, backendID, bucketName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return admindomain.Quota{}, admindomain.ErrNotFound
		}
		return admindomain.Quota{}, err
	}
	return quotaFromSQLC(row.Quota, row.BackendName, row.BucketName), nil
}

func (r *QuotaRepoV2) IncrementUsage(ctx context.Context, quotaID uuid.UUID, deltaBytes, deltaCount int64) error {
	return r.q.IncrementQuotaUsage(ctx, pgUUID(quotaID), deltaBytes, deltaCount)
}

func (r *QuotaRepoV2) ResetDaily(ctx context.Context, quotaID uuid.UUID, at time.Time) error {
	return r.q.ResetQuotaDaily(ctx, pgUUID(quotaID), pgTS(at))
}

// OnObjectPromoted increments the tenant-scope usage counters by one
// object plus its byte size. No-op when the tenant has no quota row —
// quotas are opt-in. Errors are returned to the caller; they're treated
// as non-fatal at the handler level (touchQuota suppresses).
func (r *QuotaRepoV2) OnObjectPromoted(ctx context.Context, tenantID uuid.UUID, sizeBytes int64) error {
	q, err := r.GetTenant(ctx, tenantID)
	if err != nil {
		if errors.Is(err, admindomain.ErrNotFound) {
			return nil
		}
		return err
	}
	return r.IncrementUsage(ctx, q.QuotaID, sizeBytes, 1)
}

func quotaFromSQLC(q sqlc.Quota, backendName, bucketName string) admindomain.Quota {
	return admindomain.Quota{
		QuotaID:           uuidFrom(q.ID),
		TenantID:          uuidFrom(q.TenantID),
		BackendID:         backendName,
		BucketName:        bucketName,
		MaxTotalBytes:     q.MaxTotalBytes,
		MaxObjectCount:    q.MaxObjectCount,
		MaxBytesPerDay:    q.MaxBytesPerDay,
		MaxObjectsPerDay:  q.MaxObjectsPerDay,
		UsageTotalBytes:   q.UsageTotalBytes,
		UsageObjectCount:  q.UsageObjectCount,
		UsageBytesToday:   q.UsageBytesToday,
		UsageObjectsToday: q.UsageObjectsToday,
		LastResetAt:       timePtr(q.LastResetAt),
		ResourceVersion:   q.ResourceVersion,
		UpdatedAt:         timeFrom(q.UpdatedAt),
	}
}
