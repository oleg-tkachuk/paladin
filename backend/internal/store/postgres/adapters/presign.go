package adapters

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/presignh"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// PresignRepo satisfies presignh.Repository.
type PresignRepo struct {
	q    *sqlc.Queries
	pool *pgxpool.Pool
}

func NewPresignRepo(q *sqlc.Queries, pool *pgxpool.Pool) *PresignRepo {
	return &PresignRepo{q: q, pool: pool}
}

var _ presignh.Repository = (*PresignRepo)(nil)

func (r *PresignRepo) LookupObject(ctx context.Context, tenantID uuid.UUID, collection string, objectID uuid.UUID) (presignh.ObjectRef, error) {
	row, err := r.q.GetObject(ctx, pgUUID(tenantID), pgUUID(objectID))
	if errors.Is(err, pgx.ErrNoRows) {
		return presignh.ObjectRef{}, fmt.Errorf("object %s: %w", objectID, presignh.ErrObjectNotFound)
	}
	if err != nil {
		return presignh.ObjectRef{}, fmt.Errorf("look up object %s: %w", objectID, err)
	}
	if row.CollectionName != collection {
		return presignh.ObjectRef{}, fmt.Errorf("object %s not in collection %s: %w", objectID, collection, presignh.ErrObjectNotFound)
	}
	return presignh.ObjectRef{
		Collection:    row.CollectionName,
		Key:           row.Object.Path,
		State:         string(row.Object.State),
		ContentType:   row.Object.ContentType,
		SizeBytes:     row.Object.SizeBytes,
		ChecksumAlgo:  checksumAlgoName(row.Object.ChecksumAlgorithm),
		ChecksumValue: derefStr(row.Object.Checksum),
		ETag:          derefStr(row.Object.Etag),
		Stored: bucketRefFrom(row.PlacementBackendName, row.PlacementBucketName,
			row.PlacementEnabled, row.PlacementReadOnly, row.PlacementProvisionState),
	}, nil
}

// LookupBucketMeta is the object repository's resolution, so presign shares
// its disabled-backend and read-only-drain gates: access is Read for a GET
// URL and BytesWrite for a PUT URL.
func (r *PresignRepo) LookupBucketMeta(ctx context.Context, tenantID uuid.UUID, collection string, access bucketgate.Access) (objecth.BucketMeta, error) {
	return NewObjectRepo(r.q, r.pool).LookupBucketMeta(ctx, tenantID, collection, access)
}

func (r *PresignRepo) ExtendPendingPresign(ctx context.Context, tenantID, objectID uuid.UUID, expiresAt time.Time) error {
	n, err := r.q.ExtendPendingPresign(ctx, pgUUID(tenantID), pgUUID(objectID), pgTS(expiresAt))
	if err != nil {
		return fmt.Errorf("extend pending presign: %w", err)
	}
	if n == 0 {
		return presignh.ErrNotPending
	}
	return nil
}
