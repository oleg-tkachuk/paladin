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
	return quotaFromSQLC(row), nil
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
	return bucketQuotaFromSQLC(row.BucketQuota, row.BackendName, row.BucketName, uuidFrom(row.OwnerTenantID)), nil
}

func (r *QuotaRepoV2) IncrementUsage(ctx context.Context, quotaID uuid.UUID, deltaBytes, deltaCount int64) error {
	return r.q.IncrementQuotaUsage(ctx, pgUUID(quotaID), deltaBytes, deltaCount)
}

// GetByID reads a quota of either scope by its id. The two tables draw ids
// from one uuid space (044_bucket_quotas.sql kept each moved row's id), so the
// tenant table is tried first and the bucket table on a miss.
func (r *QuotaRepoV2) GetByID(ctx context.Context, quotaID uuid.UUID) (admindomain.Quota, error) {
	row, err := r.q.GetQuotaByID(ctx, pgUUID(quotaID))
	if err == nil {
		return quotaFromSQLC(row), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return admindomain.Quota{}, err
	}
	brow, err := r.q.GetBucketQuotaByID(ctx, pgUUID(quotaID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return admindomain.Quota{}, admindomain.ErrNotFound
		}
		return admindomain.Quota{}, err
	}
	return bucketQuotaFromSQLC(brow.BucketQuota, brow.BackendName, brow.BucketName, uuidFrom(brow.OwnerTenantID)), nil
}

// ResetDaily clears the per-day counters. ErrNotFound when no row was
// updated: RLS filters a row outside the session's tenant instead of refusing
// the write, so a silent no-op would otherwise read as success.
func (r *QuotaRepoV2) ResetDaily(ctx context.Context, quotaID uuid.UUID, at time.Time) error {
	n, err := r.q.ResetQuotaDaily(ctx, pgUUID(quotaID), pgTS(at))
	if err != nil {
		return err
	}
	if n == 0 {
		return admindomain.ErrNotFound
	}
	return nil
}

// ResetBucketDaily clears a bucket quota's per-day counters; ErrNotFound when
// no such quota exists.
func (r *QuotaRepoV2) ResetBucketDaily(ctx context.Context, quotaID uuid.UUID, at time.Time) error {
	n, err := r.q.ResetBucketQuotaDaily(ctx, pgUUID(quotaID), pgTS(at))
	if err != nil {
		return err
	}
	if n == 0 {
		return admindomain.ErrNotFound
	}
	return nil
}

// OnObjectPromoted charges one object of sizeBytes to the tenant's quota and
// to the quota of the bucket the object lives in. Either may be absent —
// quotas are opt-in — and an absent one is skipped. Both are attempted; the
// errors are joined and returned, and the handlers treat them as non-fatal
// (touchQuota suppresses) because the reconciler corrects the totals.
func (r *QuotaRepoV2) OnObjectPromoted(ctx context.Context, tenantID, objectID uuid.UUID, sizeBytes int64) error {
	var tenantErr error
	q, err := r.GetTenant(ctx, tenantID)
	switch {
	case errors.Is(err, admindomain.ErrNotFound):
	case err != nil:
		tenantErr = err
	default:
		tenantErr = r.IncrementUsage(ctx, q.QuotaID, sizeBytes, 1)
	}
	bucketErr := r.q.IncrementBucketQuotaUsageForObject(ctx, pgUUID(objectID), sizeBytes, 1)
	return errors.Join(tenantErr, bucketErr)
}

func quotaFromSQLC(q sqlc.Quota) admindomain.Quota {
	return admindomain.Quota{
		QuotaID:           uuidFrom(q.ID),
		TenantID:          uuidFrom(q.TenantID),
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

// bucketQuotaFromSQLC maps a bucket_quotas row. TenantID stays uuid.Nil: that
// is how admindomain.Quota marks the bucket scope.
func bucketQuotaFromSQLC(q sqlc.BucketQuota, backendName, bucketName string, owner uuid.UUID) admindomain.Quota {
	return admindomain.Quota{
		QuotaID:           uuidFrom(q.ID),
		BackendID:         backendName,
		BucketName:        bucketName,
		OwnerTenantID:     owner,
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
