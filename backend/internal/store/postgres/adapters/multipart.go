package adapters

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/multipart"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
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
	collectionID, err := qtx.ResolveCollectionID(ctx, pgUUID(args.TenantID), args.Collection)
	if err != nil {
		return multipart.Session{}, fmt.Errorf("resolve collection %q: %w", args.Collection, err)
	}
	if err := qtx.CreateObject(ctx,
		pgUUID(objectID),
		pgUUID(args.TenantID),
		collectionID,
		args.Key,
		sqlc.ObjectStatePENDING,
		args.ContentType,
		sizePtr,
		checksumAlgoInt(args.ChecksumAlgo),
		nil,
		encodeMap(args.Metadata),
		encodeMap(args.Tags),
		strPtrOrNil(args.ExternalRef),
		pgTS(time.Now().Add(multipartSessionTTL)),
	); err != nil {
		return multipart.Session{}, fmt.Errorf("create multipart object row: %w", err)
	}

	if err := qtx.CreateMultipartUpload(ctx,
		pgUUIDFromString(uploadID),
		pgUUID(args.TenantID),
		pgUUID(objectID),
		storageUploadID,
		args.PartSizeBytes,
		args.TotalParts,
		backendID,
		bucket,
		args.InitiatedBySubject,
		args.InitiatedByKind,
	); err != nil {
		return multipart.Session{}, fmt.Errorf("create multipart upload row: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return multipart.Session{}, fmt.Errorf("commit multipart tx: %w", err)
	}

	return multipart.Session{
		UploadID: uploadID,
		ObjectID: objectID,
		// Carried back so the caller can build the object's resource name.
		// Leaving it zero made every Initiate response name a tenant of all
		// zeroes, which no subsequent call could resolve.
		TenantID:        args.TenantID,
		BackendID:       backendID,
		Bucket:          bucket,
		Collection:      args.Collection,
		Key:             args.Key,
		StorageUploadID: storageUploadID,
		PartSizeBytes:   args.PartSizeBytes,
		TotalParts:      args.TotalParts,
		CreatedAt:       time.Now(),
	}, nil
}

func (r *MultipartRepo) GetSession(ctx context.Context, uploadID string) (multipart.Session, error) {
	const q = `
				SELECT mu.id, mu.object_id, mu.storage_upload_id,
		       mu.part_size_bytes, mu.total_parts, mu.created_at,
		       sb.name, bk.name,
		       o.tenant_id, c.name, o.path
		FROM multipart_uploads mu
		JOIN objects o           ON o.id = mu.object_id
		JOIN collections c       ON c.id = o.collection_id
		JOIN buckets bk          ON bk.id = mu.bucket_id
		JOIN storage_backends sb ON sb.id = bk.backend_id
		WHERE mu.id = $1
	`
	var (
		s          multipart.Session
		objectID   uuid.UUID
		tenantID   uuid.UUID
		createdAt  time.Time
		collection string
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
		&collection,
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
	s.Collection = collection
	s.Key = key
	return s, nil
}

func (r *MultipartRepo) DeleteSession(ctx context.Context, uploadID string) error {
	return r.q.DeleteMultipartUpload(ctx, pgUUIDFromString(uploadID))
}

// LookupBucket reads the physical S3 bucket bound to a Collection via
// idx_collections_bucket_routing. bucket_name is NOT NULL after
// the schema baseline (001_initial_schema.sql) so a successful lookup always returns a non-empty value.
// `write` splits the read-only-drain gate (the schema baseline (001_initial_schema.sql)). Every multipart
// path (init / complete / abort / presign-part) is a mutation, so callers
// pass write=true; the disabled (feature 002) gate applies to all.
func (r *MultipartRepo) LookupBucket(ctx context.Context, tenantID uuid.UUID, collection string, write bool) (string, string, error) {
	const q = `
		SELECT sb.name, bk.name, sb.enabled, sb.read_only, bk.provision_state
		FROM collections c
		JOIN buckets bk          ON bk.id = c.bucket_id
		JOIN storage_backends sb ON sb.id = bk.backend_id
		WHERE c.tenant_id = $1 AND c.name = $2`
	var (
		backendID      string
		bucket         string
		enabled        bool
		readOnly       bool
		provisionState string
	)
	if err := r.pool.QueryRow(ctx, q, pgUUID(tenantID), collection).Scan(&backendID, &bucket, &enabled, &readOnly, &provisionState); err != nil {
		if isNoRows(err) {
			return "", "", fmt.Errorf("collection %q not found", collection)
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

// upload_id is a uuid in the schema and a string on the API surface, so the
// boundary parses it. An unparseable id yields the zero uuid, which matches
// no row — the same outcome as an unknown id, without a second error path.
func pgUUIDFromString(s string) pgtype.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgUUID(id)
}

// strPtrOrNil maps "" to a SQL NULL so an unset external_ref stays absent
// rather than being stored as an empty string — the two are different to a
// caller looking the object up by their own identifier.
func strPtrOrNil(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
