package adapters

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin-private/internal/api/v1/multipart"
	"github.com/oleg-tkachuk/paladin-private/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin-private/internal/store/postgres/sqlc"
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

func (r *MultipartRepo) InitiateSession(ctx context.Context, args multipart.InitiateArgs, objectID uuid.UUID, storageUploadID, backendID, bucket string) (multipart.Session, error) {
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
		backendID,
		bucket,
	); err != nil {
		return multipart.Session{}, fmt.Errorf("create multipart upload row: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return multipart.Session{}, fmt.Errorf("commit multipart tx: %w", err)
	}

	return multipart.Session{
		UploadID:        uploadID,
		ObjectID:        objectID,
		BackendID:       backendID,
		Bucket:          bucket,
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
		       mu.backend_id, mu.bucket_name,
		       o.tenant_id, o.object_key, o.key
		FROM multipart_uploads mu
		JOIN objects o ON o.object_id = mu.object_id
		WHERE mu.upload_id = $1
	`
	var (
		s          multipart.Session
		objectID   uuid.UUID
		tenantID   uuid.UUID
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
		&s.BackendID,
		&s.Bucket,
		&tenantID,
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
	s.TenantID = tenantID
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

// ListParts returns recorded parts in part_number order. The sqlc query
// returns the full set; pagination is applied in-memory because the parts
// table is small (<= 10_000 rows per upload by S3 contract).
func (r *MultipartRepo) ListParts(ctx context.Context, uploadID string, pageSize int32, pageToken string) ([]multipart.Part, string, error) {
	rows, err := r.q.ListMultipartParts(ctx, uploadID)
	if err != nil {
		return nil, "", fmt.Errorf("list parts: %w", err)
	}
	var after int32
	if pageToken != "" {
		var n int
		if _, perr := fmt.Sscanf(pageToken, "%d", &n); perr != nil {
			return nil, "", fmt.Errorf("parse page_token: %w", perr)
		}
		after = int32(n)
	}
	out := make([]multipart.Part, 0, len(rows))
	for _, r := range rows {
		if r.PartNumber <= after {
			continue
		}
		out = append(out, multipart.Part{
			PartNumber: r.PartNumber,
			SizeBytes:  r.SizeBytes,
			ETag:       r.Etag,
			Checksum:   derefStr(r.Checksum),
			UploadedAt: timeFrom(r.UploadedAt),
		})
		if int32(len(out)) >= pageSize {
			break
		}
	}
	var next string
	if int32(len(out)) == pageSize && len(out) > 0 {
		next = fmt.Sprintf("%d", out[len(out)-1].PartNumber)
	}
	return out, next, nil
}

// LookupBucket reads the physical S3 bucket bound to an ObjectKey via
// idx_object_keys_bucket_routing. bucket_name is NOT NULL after
// migration 005 so a successful lookup always returns a non-empty value.
// `write` splits the read-only-drain gate (migration 047). Every multipart
// path (init / complete / abort / presign-part) is a mutation, so callers
// pass write=true; the disabled (feature 002) gate applies to all.
func (r *MultipartRepo) LookupBucket(ctx context.Context, tenantID uuid.UUID, objectKey string, write bool) (string, string, error) {
	const q = `
		SELECT ok.backend_id, ok.bucket_name, sb.enabled, sb.read_only,
		       COALESCE(bk.provision_state, 'ready')
		FROM object_keys ok
		JOIN storage_backends sb ON sb.id = ok.backend_id
		LEFT JOIN buckets bk ON bk.backend_id = ok.backend_id AND bk.bucket_name = ok.bucket_name
		WHERE ok.tenant_id = $1 AND ok.object_key = $2`
	var (
		backendID      string
		bucket         string
		enabled        bool
		readOnly       bool
		provisionState string
	)
	if err := r.pool.QueryRow(ctx, q, pgUUID(tenantID), objectKey).Scan(&backendID, &bucket, &enabled, &readOnly, &provisionState); err != nil {
		if isNoRows(err) {
			return "", "", fmt.Errorf("objectKey %q not found", objectKey)
		}
		return "", "", fmt.Errorf("lookup bucket: %w", err)
	}
	if !enabled {
		return "", "", object.ErrBackendDisabled
	}
	if write && readOnly {
		return "", "", object.ErrBackendReadOnly
	}
	if write && provisionState != "ready" {
		return "", "", object.ErrBucketProvisioning
	}
	return backendID, bucket, nil
}
