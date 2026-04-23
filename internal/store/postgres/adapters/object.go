package adapters

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// ObjectRepo satisfies object.Repository. BucketCompletionMode walks the
// bucket → storage_backend path, so the adapter needs the raw pool.
type ObjectRepo struct {
	q    *sqlc.Queries
	pool *pgxpool.Pool
}

func NewObjectRepo(q *sqlc.Queries, pool *pgxpool.Pool) *ObjectRepo {
	return &ObjectRepo{q: q, pool: pool}
}

var _ object.Repository = (*ObjectRepo)(nil)

func (r *ObjectRepo) CreateObject(ctx context.Context, args object.CreateObjectArgs) (object.Object, error) {
	objectID := uuid.Must(uuid.NewV7())
	var sizePtr *int64
	if args.SizeHint > 0 {
		s := args.SizeHint
		sizePtr = &s
	}
	if err := r.q.CreateObject(ctx,
		pgUUID(objectID),
		pgUUID(args.TenantID),
		args.Bucket,
		args.Key,
		sqlc.ObjectStatePENDING,
		args.ContentType,
		sizePtr,
		checksumAlgoInt(args.ChecksumAlgo),
		nil, // checksum fills on promote
		encodeMap(args.Metadata),
		encodeMap(args.Tags),
		strPtr(args.ExternalRef),
		pgTS(args.PresignExpiresAt),
	); err != nil {
		return object.Object{}, fmt.Errorf("create object: %w", err)
	}
	return r.getByID(ctx, args.TenantID, objectID)
}

func (r *ObjectRepo) FindByName(ctx context.Context, tenantID uuid.UUID, bucket, objectID string) (object.Object, error) {
	id, err := uuid.Parse(objectID)
	if err != nil {
		return object.Object{}, fmt.Errorf("parse object_id: %w", err)
	}
	return r.getByID(ctx, tenantID, id)
}

func (r *ObjectRepo) FindByPath(ctx context.Context, tenantID uuid.UUID, bucketID, key string) (object.Object, error) {
	row, err := r.q.LookupObjectByKey(ctx, pgUUID(tenantID), bucketID, key)
	if err != nil {
		return object.Object{}, err
	}
	return objectFromSQLC(row.Object), nil
}

func (r *ObjectRepo) UpdateMetadata(ctx context.Context, args object.UpdateMetadataArgs) (object.Object, error) {
	var metadata, tags []byte
	var extRef *string
	for _, field := range args.UpdatedFields {
		switch field {
		case "metadata":
			metadata = encodeMap(args.Metadata)
		case "tags":
			tags = encodeMap(args.Tags)
		case "external_ref":
			e := args.ExternalRef
			extRef = &e
		}
	}
	rows, err := r.q.UpdateObjectMetadata(ctx,
		pgUUID(args.TenantID),
		pgUUID(args.ObjectID),
		metadata,
		tags,
		extRef,
		args.ResourceVersion,
	)
	if err != nil {
		return object.Object{}, fmt.Errorf("update metadata: %w", err)
	}
	if rows == 0 {
		return object.Object{}, object.ErrVersionMismatch
	}
	return r.getByID(ctx, args.TenantID, args.ObjectID)
}

func (r *ObjectRepo) ListObjects(ctx context.Context, args object.ListObjectsArgs) ([]object.Object, string, error) {
	pageSize := args.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	var afterID uuid.UUID
	if args.PageToken != "" {
		id, err := uuid.Parse(args.PageToken)
		if err != nil {
			return nil, "", fmt.Errorf("parse page_token: %w", err)
		}
		afterID = id
	}
	rows, err := r.q.ListObjects(ctx,
		pgUUID(args.TenantID),
		args.Bucket,
		sqlc.NullObjectState{}, // no state filter from handler yet
		nil,                    // prefix
		pgUUID(afterID),
		pageSize,
	)
	if err != nil {
		return nil, "", fmt.Errorf("list objects: %w", err)
	}
	out := make([]object.Object, 0, len(rows))
	for _, row := range rows {
		o := objectFromSQLC(row.Object)
		if args.CompiledCEL != nil {
			ok, evalErr := cel.Match(args.CompiledCEL, celVars(o))
			if evalErr != nil {
				return nil, "", fmt.Errorf("cel eval: %w", evalErr)
			}
			if !ok {
				continue
			}
		}
		out = append(out, o)
	}
	var next string
	if int32(len(rows)) == pageSize && len(out) > 0 {
		next = out[len(out)-1].ObjectID.String()
	}
	return out, next, nil
}

// BucketCompletionMode reads the storage backend tied to the bucket and
// returns Implicit when events are enabled on that backend, otherwise
// Explicit. Unknown bucket → Unspecified + error.
func (r *ObjectRepo) BucketCompletionMode(ctx context.Context, tenantID uuid.UUID, bucketID string) (object.CompletionMode, error) {
	const q = `
		SELECT sb.events_enabled
		FROM buckets b
		JOIN storage_backends sb ON sb.id = b.storage_backend
		WHERE b.tenant_id = $1 AND b.bucket_id = $2
	`
	var eventsEnabled bool
	if err := r.pool.QueryRow(ctx, q, pgUUID(tenantID), bucketID).Scan(&eventsEnabled); err != nil {
		if isNoRows(err) {
			return object.CompletionModeUnspecified, fmt.Errorf("bucket %q not found", bucketID)
		}
		return object.CompletionModeUnspecified, fmt.Errorf("bucket completion mode: %w", err)
	}
	if eventsEnabled {
		return object.CompletionModeImplicit, nil
	}
	return object.CompletionModeExplicit, nil
}

func (r *ObjectRepo) getByID(ctx context.Context, tenantID, objectID uuid.UUID) (object.Object, error) {
	row, err := r.q.GetObject(ctx, pgUUID(tenantID), pgUUID(objectID))
	if err != nil {
		return object.Object{}, err
	}
	return objectFromSQLC(row.Object), nil
}

func objectFromSQLC(o sqlc.Object) object.Object {
	var size int64
	if o.SizeBytes != nil {
		size = *o.SizeBytes
	}
	return object.Object{
		ObjectID:         uuidFrom(o.ObjectID),
		TenantID:         uuidFrom(o.TenantID),
		Bucket:           o.BucketID,
		Key:              o.Key,
		State:            statemachine.State(string(o.State)),
		ContentType:      o.ContentType,
		SizeBytes:        size,
		ETag:             derefStr(o.Etag),
		Checksum:         derefStr(o.Checksum),
		Sequencer:        derefStr(o.Sequencer),
		Metadata:         decodeMap(o.Metadata),
		Tags:             decodeMap(o.Tags),
		ExternalRef:      derefStr(o.ExternalRef),
		ResourceVersion:  o.ResourceVersion,
		CreatedAt:        timeFrom(o.CreatedAt),
		UpdatedAt:        timeFrom(o.UpdatedAt),
		CommittedAt:      timePtr(o.CommittedAt),
		TerminatedAt:     timePtr(o.TerminatedAt),
		PresignExpiresAt: timePtr(o.PresignExpiresAt),
	}
}

// celVars surfaces a flat map of attributes CEL programs can reference. Keep
// the list stable — changes ripple out to every user-defined filter.
func celVars(o object.Object) map[string]any {
	return map[string]any{
		"key":          o.Key,
		"content_type": o.ContentType,
		"size_bytes":   o.SizeBytes,
		"state":        string(o.State),
		"external_ref": o.ExternalRef,
		"tags":         o.Tags,
		"metadata":     o.Metadata,
	}
}
