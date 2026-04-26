package adapters

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/multipart"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// MultipartRepo satisfies multipart.Repository. InitiateSession writes two
// rows (objects + multipart_uploads) inside a single transaction so the
// handler never observes a dangling upload.
type MultipartRepo struct {
	q    *sqlc.Queries
	pool *pgxpool.Pool
}

func NewMultipartRepo(q *sqlc.Queries, pool *pgxpool.Pool) *MultipartRepo {
	return &MultipartRepo{q: q, pool: pool}
}

var _ multipart.Repository = (*MultipartRepo)(nil)

const multipartSessionTTL = 24 * time.Hour

func (r *MultipartRepo) InitiateSession(ctx context.Context, args multipart.InitiateArgs, objectID uuid.UUID, storageUploadID string) (multipart.Session, error) {
	uploadID := uuid.Must(uuid.NewV7()).String()

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return multipart.Session{}, fmt.Errorf("begin multipart tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := r.q.WithTx(tx)

	var sizePtr *int64
	if args.SizeHint > 0 {
		s := args.SizeHint
		sizePtr = &s
	}
	if err := qtx.CreateObject(ctx,
		pgUUID(objectID),
		pgUUID(args.TenantID),
		args.ObjectKey,
		args.Key,
		sqlc.ObjectStatePENDING,
		args.ContentType,
		sizePtr,
		checksumAlgoInt(args.ChecksumAlgo),
		nil,
		encodeMap(args.Metadata),
		encodeMap(args.Tags),
		nil,
		pgTS(time.Now().Add(multipartSessionTTL)),
	); err != nil {
		return multipart.Session{}, fmt.Errorf("create multipart object row: %w", err)
	}

	if err := qtx.CreateMultipartUpload(ctx,
		uploadID,
		pgUUID(objectID),
		storageUploadID,
		args.PartSizeBytes,
		args.TotalParts,
	); err != nil {
		return multipart.Session{}, fmt.Errorf("create multipart upload row: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return multipart.Session{}, fmt.Errorf("commit multipart tx: %w", err)
	}

	return multipart.Session{
		UploadID:        uploadID,
		ObjectID:        objectID,
		ObjectKey:       args.ObjectKey,
		Key:             args.Key,
		StorageUploadID: storageUploadID,
		PartSizeBytes:   args.PartSizeBytes,
		TotalParts:      args.TotalParts,
		CreatedAt:       time.Now(),
	}, nil
}

func (r *MultipartRepo) GetSession(ctx context.Context, uploadID string) (multipart.Session, error) {
	const q = `
		SELECT mu.upload_id, mu.object_id, mu.storage_upload_id,
		       mu.part_size_bytes, mu.total_parts, mu.created_at,
		       o.object_key, o.key
		FROM multipart_uploads mu
		JOIN objects o ON o.object_id = mu.object_id
		WHERE mu.upload_id = $1
	`
	var (
		s          multipart.Session
		objectID   uuid.UUID
		createdAt  time.Time
		objectKey  string
		key        string
		partSize   int64
		totalParts int32
	)
	err := r.pool.QueryRow(ctx, q, uploadID).Scan(
		&s.UploadID,
		&objectID,
		&s.StorageUploadID,
		&partSize,
		&totalParts,
		&createdAt,
		&objectKey,
		&key,
	)
	if err != nil {
		if isNoRows(err) {
			return multipart.Session{}, fmt.Errorf("upload %q not found", uploadID)
		}
		return multipart.Session{}, fmt.Errorf("get multipart session: %w", err)
	}
	s.ObjectID = objectID
	s.PartSizeBytes = partSize
	s.TotalParts = totalParts
	s.CreatedAt = createdAt
	s.ObjectKey = objectKey
	s.Key = key
	return s, nil
}

func (r *MultipartRepo) RecordPart(ctx context.Context, uploadID string, part multipart.PartETag, sizeBytes int64, checksum string) error {
	return r.q.RecordMultipartPart(ctx,
		uploadID,
		part.PartNumber,
		sizeBytes,
		part.ETag,
		strPtr(checksum),
	)
}

func (r *MultipartRepo) DeleteSession(ctx context.Context, uploadID string) error {
	return r.q.DeleteMultipartUpload(ctx, uploadID)
}

func (r *MultipartRepo) GetObjectLocation(ctx context.Context, objectID uuid.UUID) (objectKey, key string, err error) {
	const q = `SELECT object_key, key FROM objects WHERE object_id = $1`
	err = r.pool.QueryRow(ctx, q, pgUUID(objectID)).Scan(&objectKey, &key)
	if err != nil {
		if isNoRows(err) {
			return "", "", fmt.Errorf("object %s not found", objectID)
		}
		return "", "", fmt.Errorf("object location: %w", err)
	}
	return objectKey, key, nil
}

// LookupBucket reads the physical S3 bucket bound to an ObjectKey via
// idx_object_keys_bucket_routing. bucket_name is NOT NULL after
// migration 005 so a successful lookup always returns a non-empty value.
func (r *MultipartRepo) LookupBucket(ctx context.Context, tenantID uuid.UUID, objectKey string) (string, error) {
	const q = `SELECT bucket_name FROM object_keys WHERE tenant_id = $1 AND object_key = $2`
	var bucket string
	if err := r.pool.QueryRow(ctx, q, pgUUID(tenantID), objectKey).Scan(&bucket); err != nil {
		if isNoRows(err) {
			return "", fmt.Errorf("objectKey %q not found", objectKey)
		}
		return "", fmt.Errorf("lookup bucket: %w", err)
	}
	return bucket, nil
}
