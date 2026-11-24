package adapters

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// uploadWritesCloseAt reads the deadline a URL that writes (backendID,
// bucket) must expire by: set while the tenant's storage migration empties
// the bucket (ADR-0051). ok false when none bounds it.
func uploadWritesCloseAt(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, backendID, bucket string) (time.Time, bool, error) {
	t, err := q.UploadWritesCloseAt(ctx, pgUUID(tenantID), backendID, bucket)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("read the write deadline of %s/%s: %w", backendID, bucket, err)
	}
	return t.Time, t.Valid, nil
}

// UploadWritesCloseAt is objecth.WriteDeadlines for a regenerated upload URL.
func (r *PresignRepo) UploadWritesCloseAt(ctx context.Context, tenantID uuid.UUID, backendID, bucket string) (time.Time, bool, error) {
	return uploadWritesCloseAt(ctx, r.q, tenantID, backendID, bucket)
}

// UploadWritesCloseAt is objecth.WriteDeadlines for a multipart part URL.
func (r *MultipartRepo) UploadWritesCloseAt(ctx context.Context, tenantID uuid.UUID, backendID, bucket string) (time.Time, bool, error) {
	return uploadWritesCloseAt(ctx, r.q, tenantID, backendID, bucket)
}
