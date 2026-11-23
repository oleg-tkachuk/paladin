package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/multiparth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/storagepath"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/objectpath"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
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

var _ multiparth.Repository = (*MultipartRepo)(nil)

// Object reads the object a completed upload produced, through the object
// repository's own read, so both answer with the same row.
func (r *MultipartRepo) Object(ctx context.Context, tenantID, objectID uuid.UUID) (objecth.Object, error) {
	return NewObjectRepo(r.q, r.pool).getByID(ctx, tenantID, objectID)
}

const multipartSessionTTL = 24 * time.Hour

func (r *MultipartRepo) InitiateSession(ctx context.Context, args multiparth.InitiateArgs, objectID uuid.UUID, storageUploadID, backendID, bucket string) (multiparth.Session, error) {
	// As ObjectRepo.CreateObject: the handler admitted the key; this refuses
	// a caller that did not.
	if _, err := storagepath.Compose(args.TenantID, args.Collection, args.Key); err != nil {
		return multiparth.Session{}, fmt.Errorf("initiate multipart session: %w", err)
	}
	uploadID := uuid.NewV7().String()

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return multiparth.Session{}, fmt.Errorf("begin multipart tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := r.q.WithTx(tx)

	// Under the path lock, as every object insert (package objectpath).
	if err := objectpath.Lock(ctx, qtx, args.TenantID, args.Collection, args.Key); err != nil {
		return multiparth.Session{}, fmt.Errorf("lock object path: %w", err)
	}
	collectionID, err := qtx.ResolveCollectionID(ctx, pgUUID(args.TenantID), args.Collection)
	if err != nil {
		return multiparth.Session{}, fmt.Errorf("resolve collection %q: %w", args.Collection, err)
	}
	// Anchored to the session's bucket, where its parts are assembled.
	uploadBucketID, err := resolveUploadBucket(ctx, qtx, backendID, bucket)
	if err != nil {
		return multiparth.Session{}, err
	}
	if err := qtx.CreateObject(ctx,
		pgUUID(objectID),
		pgUUID(args.TenantID),
		collectionID,
		args.Key,
		sqlc.ObjectStatePENDING,
		args.ContentType,
		&args.SizeHint,
		checksumAlgoInt(args.ChecksumAlgo),
		nil,
		encodeMap(args.Metadata),
		encodeMap(args.Tags),
		strPtrOrNil(args.ExternalRef),
		pgTS(time.Now().Add(multipartSessionTTL)),
		args.PublicURL,
		pgtype.UUID{}, // an upload session, not an operation, creates it
		uploadBucketID,
	); err != nil {
		return multiparth.Session{}, fmt.Errorf("create multipart object row: %w", err)
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
		// Denormalised so the abort-debt trigger can write a self-sufficient
		// row: when a cascade from objects removes this session, the object
		// row is already gone and the key could not be recovered by a join.
		args.Collection,
		args.Key,
	); err != nil {
		return multiparth.Session{}, fmt.Errorf("create multipart upload row: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return multiparth.Session{}, fmt.Errorf("commit multipart tx: %w", err)
	}

	return multiparth.Session{
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
		SizeBytes:       args.SizeHint,
		ChecksumAlgo:    args.ChecksumAlgo,
		CreatedAt:       time.Now(),
	}, nil
}

// GetSession reads one upload session; multiparth.ErrUploadNotFound when the
// store has none by that id — an id that does not parse names none either,
// and Postgres would refuse it as a malformed uuid rather than match no row.
//
// The session comes with the bucket it was anchored to at initiate — not the
// collection's current binding — and that bucket's gate state and upload
// constraints, which every RPC on the session is decided on.
func (r *MultipartRepo) GetSession(ctx context.Context, uploadID string) (multiparth.Session, error) {
	if _, err := uuid.Parse(uploadID); err != nil {
		return multiparth.Session{}, fmt.Errorf("upload %q: %w", uploadID, multiparth.ErrUploadNotFound)
	}
	const q = `
				SELECT mu.id, mu.object_id, mu.storage_upload_id,
		       mu.part_size_bytes, mu.total_parts, mu.created_at,
		       sb.name, bk.name,
		       o.tenant_id, c.name, o.path,
		       o.size_bytes, o.checksum_algorithm,
		       sb.enabled, sb.read_only, bk.provision_state, bk.constraints
		FROM multipart_uploads mu
		JOIN objects o           ON o.id = mu.object_id
		JOIN collections c       ON c.id = o.collection_id
		JOIN buckets bk          ON bk.id = mu.bucket_id
		JOIN storage_backends sb ON sb.id = bk.backend_id
		WHERE mu.id = $1
	`
	var (
		s          multiparth.Session
		objectID   uuid.UUID
		tenantID   uuid.UUID
		createdAt  time.Time
		collection string
		key        string
		partSize   int64
		totalParts int32
		size       *int64
		algo       int16
		bucketDoc  []byte
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
		&size,
		&algo,
		&s.BucketState.Enabled,
		&s.BucketState.ReadOnly,
		&s.BucketState.ProvisionState,
		&bucketDoc,
	)
	if err != nil {
		if isNoRows(err) {
			return multiparth.Session{}, fmt.Errorf("upload %q: %w", uploadID, multiparth.ErrUploadNotFound)
		}
		return multiparth.Session{}, fmt.Errorf("get multipart session: %w", err)
	}
	// Fail closed, as LookupBucketMeta does: a constraints document that does
	// not decode is not "no limits".
	if len(bucketDoc) > 0 {
		if err := json.Unmarshal(bucketDoc, &s.BucketConstraints); err != nil {
			return multiparth.Session{}, fmt.Errorf("bucket %q constraints: %w", s.Bucket, err)
		}
	}
	s.ObjectID = objectID
	s.TenantID = tenantID
	s.PartSizeBytes = partSize
	s.TotalParts = totalParts
	s.CreatedAt = createdAt
	s.Collection = collection
	s.Key = key
	if size != nil {
		s.SizeBytes = *size
	}
	s.ChecksumAlgo = checksumAlgoName(algo)
	return s, nil
}

// DeleteSession removes a session whose S3-side upload has already been
// aborted. Both callers — the Abort RPC and MultipartReaper — call
// AbortMultipart first and only then come here.
//
// The delete runs in a transaction that sets `paladin.multipart_aborted`, which
// is what tells the abort-debt trigger (010) to stay quiet. Every other way a
// session row disappears is a cascade from objects or tenants, which cannot
// have called S3 — so the trigger records the obligation and
// MultipartAbortDrainer discharges it. Without the flag this path would file a
// debt against an upload it has just aborted.
// RecordCompositeChecksum writes a PENDING object's composite checksum and
// its part size. A row past PENDING is not rewritten, and the zero-row write
// is multiparth.ErrNotPending, for the handler to decide.
func (r *MultipartRepo) RecordCompositeChecksum(ctx context.Context, objectID uuid.UUID, composite string, partSizeBytes int64) error {
	n, err := r.q.RecordCompositeChecksum(ctx, &composite, &partSizeBytes, pgUUID(objectID))
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("record composite checksum of %s: %w", objectID, multiparth.ErrNotPending)
	}
	return nil
}

func (r *MultipartRepo) DeleteSession(ctx context.Context, uploadID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("delete multipart session: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SET LOCAL paladin.multipart_aborted = 'on'"); err != nil {
		return fmt.Errorf("delete multipart session: mark aborted: %w", err)
	}
	if err := r.q.WithTx(tx).DeleteMultipartUpload(ctx, pgUUIDFromString(uploadID)); err != nil {
		return fmt.Errorf("delete multipart session: %w", err)
	}
	return tx.Commit(ctx)
}

// LookupBucketMeta is the object repository's resolution, constraints
// included. Only initiate resolves the collection: every later RPC on the
// session is decided on the bucket GetSession reads it was anchored to.
func (r *MultipartRepo) LookupBucketMeta(ctx context.Context, tenantID uuid.UUID, collection string, access bucketgate.Access) (objecth.BucketMeta, error) {
	return NewObjectRepo(r.q, r.pool).LookupBucketMeta(ctx, tenantID, collection, access)
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
