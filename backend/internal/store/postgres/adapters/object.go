package adapters

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// ObjectRepo satisfies object.Repository. BucketCompletionMode walks the
// objectKey → storage_backend path, so the adapter needs the raw pool.
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
		args.ObjectKey,
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

func (r *ObjectRepo) FindByName(ctx context.Context, tenantID uuid.UUID, objectKey, objectID string) (object.Object, error) {
	id, err := uuid.Parse(objectID)
	if err != nil {
		return object.Object{}, fmt.Errorf("parse object_id: %w", err)
	}
	return r.getByID(ctx, tenantID, id)
}

func (r *ObjectRepo) FindByIDs(ctx context.Context, tenantID uuid.UUID, ids []uuid.UUID) ([]object.Object, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	pgIDs := make([]pgtype.UUID, len(ids))
	for i, id := range ids {
		pgIDs[i] = pgUUID(id)
	}
	rows, err := r.q.GetObjectsByIDs(ctx, pgUUID(tenantID), pgIDs)
	if err != nil {
		return nil, fmt.Errorf("get objects by ids: %w", err)
	}
	out := make([]object.Object, 0, len(rows))
	for _, row := range rows {
		out = append(out, objectFromSQLC(row.Object))
	}
	return out, nil
}

func (r *ObjectRepo) FindByPath(ctx context.Context, tenantID uuid.UUID, objectKey, key string) (object.Object, error) {
	row, err := r.q.LookupObjectByKey(ctx, pgUUID(tenantID), objectKey, key)
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
		args.ObjectKey,
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

// countScanCap bounds how many rows CountObjects will scan when a CEL
// filter is supplied. Above this limit the partial match count is returned
// with exact=false so the caller can render "N+ matches" rather than lie.
const countScanCap = 10000

// CountObjects returns the number of matching rows. No filter → single
// COUNT(*) query (exact). With filter → paginated keyset scan applying CEL
// in-process, capped at countScanCap.
func (r *ObjectRepo) CountObjects(ctx context.Context, args object.CountObjectsArgs) (int64, bool, error) {
	if args.CompiledCEL == nil {
		n, err := r.q.CountObjects(ctx, pgUUID(args.TenantID), args.ObjectKey, sqlc.NullObjectState{})
		if err != nil {
			return 0, false, fmt.Errorf("count objects: %w", err)
		}
		return n, true, nil
	}

	const pageSize int32 = 500
	var (
		matched int64
		scanned int64
		afterID uuid.UUID
	)
	for {
		rows, err := r.q.ListObjects(ctx,
			pgUUID(args.TenantID),
			args.ObjectKey,
			sqlc.NullObjectState{},
			nil,
			pgUUID(afterID),
			pageSize,
		)
		if err != nil {
			return 0, false, fmt.Errorf("count objects scan: %w", err)
		}
		if len(rows) == 0 {
			return matched, true, nil
		}
		for _, row := range rows {
			o := objectFromSQLC(row.Object)
			ok, evalErr := cel.Match(args.CompiledCEL, celVars(o))
			if evalErr != nil {
				return 0, false, fmt.Errorf("cel eval: %w", evalErr)
			}
			if ok {
				matched++
			}
			scanned++
			afterID = o.ObjectID
			if scanned >= countScanCap {
				return matched, false, nil
			}
		}
		if int32(len(rows)) < pageSize {
			return matched, true, nil
		}
	}
}

// LookupBucket returns the physical S3 bucket bound to a tenant's
// ObjectKey. Hits idx_object_keys_bucket_routing. After migration 005
// bucket_name is NOT NULL so a successful lookup always returns a
// non-empty string.
func (r *ObjectRepo) LookupBucket(ctx context.Context, tenantID uuid.UUID, objectKey string) (string, error) {
	// JOIN storage_backends so a disabled backend is refused at the single
	// resolution chokepoint (feature 002) — zero extra round trip.
	const q = `
		SELECT ok.bucket_name, sb.enabled
		FROM object_keys ok
		JOIN storage_backends sb ON sb.id = ok.backend_id
		WHERE ok.tenant_id = $1 AND ok.object_key = $2`
	var (
		bucket  string
		enabled bool
	)
	if err := r.pool.QueryRow(ctx, q, pgUUID(tenantID), objectKey).Scan(&bucket, &enabled); err != nil {
		if isNoRows(err) {
			return "", fmt.Errorf("objectKey %q not found", objectKey)
		}
		return "", fmt.Errorf("lookup bucket: %w", err)
	}
	if !enabled {
		return "", object.ErrBackendDisabled
	}
	return bucket, nil
}

// LookupBucketMeta returns the bucket binding plus versioning + lock flags
// in one trip. Hot-path call on every promote / delete; the JOIN hits
// idx_object_keys_bucket and the buckets PK.
//
// Versioning + Object Lock can be OVERRIDDEN per object_key via the
// `object_keys.constraints` JSONB field. Override semantics:
//
//   - `versioning_enabled` boolean — when set, takes precedence over the
//     parent bucket flag. true → force-on (even on a non-versioned bucket
//     — the object_versions rows just won't have S3 versionId metadata
//     until the bucket is also versioned). false → force-off.
//   - Missing key → fall through to bucket.versioning_enabled.
//   - `object_lock_enabled` follows the same overlay logic.
//
// This lets a tenant turn versioning on for one namespace within a
// shared bucket without touching the bucket's global config.
func (r *ObjectRepo) LookupBucketMeta(ctx context.Context, tenantID uuid.UUID, objectKey string) (object.BucketMeta, error) {
	// JOIN storage_backends so a disabled backend is refused here too —
	// the resolution chokepoint covers every promote/delete/version path.
	const q = `
		SELECT b.backend_id, b.bucket_name,
		       COALESCE(bk.versioning_enabled, false),
		       COALESCE(bk.object_lock_enabled, false),
		       b.constraints, sb.enabled, sb.events_enabled
		FROM object_keys b
		JOIN storage_backends sb ON sb.id = b.backend_id
		LEFT JOIN buckets bk
		  ON bk.backend_id = b.backend_id AND bk.bucket_name = b.bucket_name
		WHERE b.tenant_id = $1 AND b.object_key = $2
	`
	var (
		meta            object.BucketMeta
		constraintsJSON []byte
		enabled         bool
	)
	if err := r.pool.QueryRow(ctx, q, pgUUID(tenantID), objectKey).Scan(
		&meta.BackendID, &meta.BucketName, &meta.VersioningEnabled, &meta.ObjectLockEnabled,
		&constraintsJSON, &enabled, &meta.EventsEnabled,
	); err != nil {
		if isNoRows(err) {
			return object.BucketMeta{}, fmt.Errorf("objectKey %q not found", objectKey)
		}
		return object.BucketMeta{}, fmt.Errorf("lookup bucket meta: %w", err)
	}
	if !enabled {
		return object.BucketMeta{}, object.ErrBackendDisabled
	}
	if v, ok := readBoolOverride(constraintsJSON, "versioning_enabled"); ok {
		meta.VersioningEnabled = v
	}
	if v, ok := readBoolOverride(constraintsJSON, "object_lock_enabled"); ok {
		meta.ObjectLockEnabled = v
	}
	return meta, nil
}

// readBoolOverride extracts a top-level bool field from the constraints
// JSONB blob. Returns (value, true) when the key exists with a bool value;
// (false, false) when missing or wrong type. Liberal in input — malformed
// JSON degrades open (no override applied).
func readBoolOverride(raw []byte, key string) (bool, bool) {
	if len(raw) == 0 {
		return false, false
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return false, false
	}
	v, ok := m[key]
	if !ok {
		return false, false
	}
	b, ok := v.(bool)
	return b, ok
}

// HardDeleteWithBypass mirrors HardDelete but wraps the call in a
// transaction with `SET LOCAL paladin.governance_bypass = true`. The
// enforce_object_version_lock trigger on object_versions reads this GUC.
//
// Compliance-mode rows still raise — by design.
func (r *ObjectRepo) HardDeleteWithBypass(ctx context.Context, tenantID, objectID uuid.UUID, expectedVersion int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SET LOCAL paladin.governance_bypass = 'true'"); err != nil {
		return fmt.Errorf("set bypass GUC: %w", err)
	}
	rows, err := r.q.WithTx(tx).HardDeleteObject(ctx, pgUUID(tenantID), pgUUID(objectID), expectedVersion)
	if err != nil {
		return fmt.Errorf("hard delete (bypass): %w", err)
	}
	if rows == 0 {
		return object.ErrVersionMismatch
	}
	return tx.Commit(ctx)
}

// HardDelete removes the row. expectedVersion=0 disables the OCC guard.
// ErrVersionMismatch when no rows match (either gone or version drift).
func (r *ObjectRepo) HardDelete(ctx context.Context, tenantID, objectID uuid.UUID, expectedVersion int64) error {
	rows, err := r.q.HardDeleteObject(ctx, pgUUID(tenantID), pgUUID(objectID), expectedVersion)
	if err != nil {
		return fmt.Errorf("hard delete: %w", err)
	}
	if rows == 0 {
		return object.ErrVersionMismatch
	}
	return nil
}

// LiveCollision reports whether a non-DELETED row exists at (tenant, objectKey, key).
func (r *ObjectRepo) LiveCollision(ctx context.Context, tenantID uuid.UUID, objectKey, key string) (bool, error) {
	exists, err := r.q.CheckLiveCollision(ctx, pgUUID(tenantID), objectKey, key)
	if err != nil {
		return false, fmt.Errorf("live collision check: %w", err)
	}
	return exists, nil
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
		ObjectKey:        o.ObjectKey,
		Key:              o.Key,
		State:            statemachine.State(string(o.State)),
		ContentType:      o.ContentType,
		SizeBytes:        size,
		ETag:             derefStr(o.Etag),
		ChecksumAlgo:     checksumAlgoName(o.ChecksumAlgorithm),
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
