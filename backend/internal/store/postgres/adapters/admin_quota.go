package adapters

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

type QuotaRepoV2 struct {
	q *sqlc.Queries
}

func NewQuotaRepoV2(q *sqlc.Queries) *QuotaRepoV2 { return &QuotaRepoV2{q: q} }

var _ admindomain.QuotaRepository = (*QuotaRepoV2)(nil)

func (r *QuotaRepoV2) UpsertTenant(ctx context.Context, q admindomain.Quota) error {
	if q.QuotaID == uuid.Nil {
		q.QuotaID = uuid.Must(uuid.NewV7())
	}
	return r.q.UpsertTenantQuota(ctx,
		pgUUID(q.QuotaID),
		pgUUID(q.TenantID),
		q.MaxTotalBytes,
		q.MaxObjectCount,
		q.MaxBytesPerDay,
		q.MaxObjectsPerDay,
	)
}

func (r *QuotaRepoV2) UpsertBucket(ctx context.Context, q admindomain.Quota) error {
	if q.QuotaID == uuid.Nil {
		q.QuotaID = uuid.Must(uuid.NewV7())
	}
	return r.q.UpsertBucketQuota(ctx,
		pgUUID(q.QuotaID),
		strPtr(q.BackendID),
		strPtr(q.BucketName),
		q.MaxTotalBytes,
		q.MaxObjectCount,
		q.MaxBytesPerDay,
		q.MaxObjectsPerDay,
	)
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
	row, err := r.q.GetBucketQuota(ctx, &backendID, &bucketName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return admindomain.Quota{}, admindomain.ErrNotFound
		}
		return admindomain.Quota{}, err
	}
	return quotaFromSQLC(row), nil
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

func quotaFromSQLC(q sqlc.Quota) admindomain.Quota {
	return admindomain.Quota{
		QuotaID:           uuidFrom(q.QuotaID),
		TenantID:          uuidFrom(q.TenantID),
		BackendID:         derefStr(q.BackendID),
		BucketName:        derefStr(q.BucketName),
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
