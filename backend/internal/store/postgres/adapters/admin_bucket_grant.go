package adapters

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgerr"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// BucketGrantRepoV2 keeps a bucket's grants (ADR-0032). The questions it asks
// about collections — whether a tenant still has one in the bucket — read a
// table under row-level security, so callers ask them under a cross-tenant
// read (auth.WithCrossTenantRead), as CountBucketReferences does.
type BucketGrantRepoV2 struct {
	q *sqlc.Queries
}

// NewBucketGrantRepoV2 returns the grant repository over q.
func NewBucketGrantRepoV2(q *sqlc.Queries) *BucketGrantRepoV2 {
	return &BucketGrantRepoV2{q: q}
}

var _ admindomain.BucketGrantRepository = (*BucketGrantRepoV2)(nil)

// Grant inserts the grant, or returns the one the tenant holds. No row back
// means the bucket does not exist.
func (r *BucketGrantRepoV2) Grant(ctx context.Context, backendID, bucketName string, tenantID uuid.UUID, grantedBy string) (admindomain.BucketGrant, error) {
	row, err := r.q.GrantBucket(ctx, pgUUID(tenantID), grantedBy, backendID, bucketName)
	if errors.Is(err, pgx.ErrNoRows) {
		return admindomain.BucketGrant{}, admindomain.ErrNotFound
	}
	if err != nil {
		return admindomain.BucketGrant{}, fmt.Errorf("grant bucket: %w", mapBucketGrantErr(err))
	}
	return admindomain.BucketGrant{
		TenantID:  uuid.UUID(row.TenantID.Bytes),
		GrantedBy: row.GrantedBy,
		GrantedAt: row.GrantedAt.Time,
	}, nil
}

// GrantTx is Grant on the caller's transaction: an owner move grants the
// previous owner in the same transaction that moves the bucket.
func (r *BucketGrantRepoV2) GrantTx(ctx context.Context, tx pgx.Tx, backendID, bucketName string, tenantID uuid.UUID, grantedBy string) (admindomain.BucketGrant, error) {
	return (&BucketGrantRepoV2{q: r.q.WithTx(tx)}).Grant(ctx, backendID, bucketName, tenantID, grantedBy)
}

// RevokeGrant deletes the grant unless collections remain; with no row
// deleted it asks which of the two it was.
func (r *BucketGrantRepoV2) RevokeGrant(ctx context.Context, backendID, bucketName string, tenantID uuid.UUID) error {
	n, err := r.q.RevokeBucketGrant(ctx, backendID, bucketName, pgUUID(tenantID))
	if err != nil {
		return fmt.Errorf("revoke bucket grant: %w", err)
	}
	if n > 0 {
		return nil
	}
	inUse, err := r.q.BucketGrantInUse(ctx, backendID, bucketName, pgUUID(tenantID))
	if err != nil {
		return fmt.Errorf("revoke bucket grant: in use: %w", err)
	}
	if inUse {
		return admindomain.ErrBucketGrantInUse
	}
	return admindomain.ErrNotFound
}

// ListGrants returns the bucket's grants, oldest first; none for a bucket that
// has none or does not exist — the caller resolves the bucket first.
func (r *BucketGrantRepoV2) ListGrants(ctx context.Context, backendID, bucketName string) ([]admindomain.BucketGrant, error) {
	rows, err := r.q.ListBucketGrants(ctx, backendID, bucketName)
	if err != nil {
		return nil, fmt.Errorf("list bucket grants: %w", err)
	}
	out := make([]admindomain.BucketGrant, 0, len(rows))
	for _, row := range rows {
		out = append(out, admindomain.BucketGrant{
			TenantID:  uuid.UUID(row.TenantID.Bytes),
			GrantedBy: row.GrantedBy,
			GrantedAt: row.GrantedAt.Time,
		})
	}
	return out, nil
}

// MayBind reports whether the tenant owns the bucket or holds a grant on it.
func (r *BucketGrantRepoV2) MayBind(ctx context.Context, backendID, bucketName string, tenantID uuid.UUID) (bool, error) {
	ok, err := r.q.TenantMayBindBucket(ctx, pgUUID(tenantID), backendID, bucketName)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, admindomain.ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("may bind bucket: %w", err)
	}
	return ok, nil
}

// mapBucketGrantErr reads a grant naming a tenant that does not exist — the
// foreign key on bucket_grants.tenant_id — as ErrNotFound.
func mapBucketGrantErr(err error) error {
	if pgerr.Is(err, pgerr.ForeignKeyViolation) {
		return fmt.Errorf("%w: no such tenant", admindomain.ErrNotFound)
	}
	return err
}
