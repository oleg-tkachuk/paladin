package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// ObjectRepo satisfies object.Repository. BucketCompletionMode walks the
// collection → storage_backend path, so the adapter needs the raw pool.
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
	// The collection is known by name here; CreateObject stores its id.
	// Resolved rather than joined because sqlc rejects a parameter used only
	// inside a subquery in VALUES.
	collectionID, err := r.q.ResolveCollectionID(ctx, pgUUID(args.TenantID), args.Collection)
	if err != nil {
		return object.Object{}, fmt.Errorf("resolve collection %q: %w", args.Collection, err)
	}
	if err := r.q.CreateObject(ctx,
		pgUUID(objectID),
		pgUUID(args.TenantID),
		collectionID,
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

func (r *ObjectRepo) FindByName(ctx context.Context, tenantID uuid.UUID, collection, objectID string) (object.Object, error) {
	id, err := uuid.Parse(objectID)
	if err != nil {
		return object.Object{}, fmt.Errorf("parse object_id: %w", err)
	}
	return r.getByID(ctx, tenantID, id)
}

func (r *ObjectRepo) ObjectLock(ctx context.Context, tenantID, objectID uuid.UUID) (object.ObjectLock, error) {
	row, err := r.q.GetObjectLockState(ctx, pgUUID(tenantID), pgUUID(objectID))
	if err != nil {
		// No-rows is unexpected here — the delete path already resolved
		// the object via FindByName — so surface it as a plain error.
		return object.ObjectLock{}, fmt.Errorf("get object lock state: %w", err)
	}
	return object.ObjectLock{
		Mode:        lockModeFromSQL(row.LockMode),
		RetainUntil: timePtr(row.LockRetainUntil),
		LegalHold:   row.LegalHold,
	}, nil
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
		out = append(out, objectFromSQLC(row.Object, row.CollectionName))
	}
	return out, nil
}

func (r *ObjectRepo) FindByPath(ctx context.Context, tenantID uuid.UUID, collection, key string) (object.Object, error) {
	row, err := r.q.LookupObjectByKey(ctx, pgUUID(tenantID), collection, key)
	if err != nil {
		return object.Object{}, err
	}
	return objectFromSQLC(row.Object, row.CollectionName), nil
}

func (r *ObjectRepo) UpdateMetadata(ctx context.Context, args object.UpdateMetadataArgs) (object.Object, error) {
	return r.updateMetadata(ctx, r.q, args)
}

// UpdateMetadataTx runs UpdateMetadata on the caller's transaction so the
// handler can write the paladin.object.updated outbox rows atomically with the
// row update — closing the dual-write crash window (ADR-0003).
func (r *ObjectRepo) UpdateMetadataTx(ctx context.Context, tx pgx.Tx, args object.UpdateMetadataArgs) (object.Object, error) {
	return r.updateMetadata(ctx, r.q.WithTx(tx), args)
}

func (r *ObjectRepo) updateMetadata(ctx context.Context, q *sqlc.Queries, args object.UpdateMetadataArgs) (object.Object, error) {
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
	rows, err := q.UpdateObjectMetadata(ctx,
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
	return r.getByIDWith(ctx, q, args.TenantID, args.ObjectID)
}

func (r *ObjectRepo) ListObjects(ctx context.Context, args object.ListObjectsArgs) ([]object.Object, string, error) {
	pageSize := pageSizeOrDefault(args.PageSize)
	var afterID uuid.UUID
	if args.PageToken != "" {
		id, err := uuid.Parse(args.PageToken)
		if err != nil {
			return nil, "", fmt.Errorf("parse page_token: %w", err)
		}
		afterID = id
	}

	// Pushdown: extract the SQL-expressible subset of the CEL filter
	// (state equality, key prefix/substring) and let Postgres narrow the
	// scan instead of streaming the whole namespace into Go. The full
	// CompiledCEL is still evaluated per row below, so an unrecognised
	// or partially-pushed filter only over-fetches — it never drops a
	// matching row. A pushdown parse error is non-fatal (the CompiledCEL
	// path already validated the same expression).
	var state *sqlc.ObjectState
	var prefix, substr *string
	if args.Filter != "" {
		if pd, perr := cel.ExtractObjectPushdown(args.Filter); perr == nil {
			if pd.StateEq != "" {
				v := sqlc.ObjectState(pd.StateEq)
				state = &v
			}
			// Only push a key literal when it has no LIKE metacharacters
			// (%, _, \). Otherwise the SQL LIKE would interpret them as
			// wildcards and broaden the scan; since the CompiledCEL pass
			// is authoritative that's still correct, but skipping keeps
			// the hint precise without an ESCAPE clause.
			if p, ok := likeLiteral(pd.KeyPrefix); ok {
				prefix = &p
			}
			if s, ok := likeLiteral(pd.KeyContains); ok {
				substr = &s
			}
		}
	}

	rows, err := r.q.ListObjects(ctx,
		pgUUID(args.TenantID),
		args.Collection,
		state,
		prefix,
		substr,
		pgUUID(afterID),
		pageSize,
	)
	if err != nil {
		return nil, "", fmt.Errorf("list objects: %w", err)
	}
	out := make([]object.Object, 0, len(rows))
	for _, row := range rows {
		o := objectFromSQLC(row.Object, row.CollectionName)
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
	// Page token advances by the last FETCHED row's id, NOT the last
	// matching one. Deriving it from `out` truncated the listing when a
	// full DB page was entirely CEL-filtered (len(out)==0 → empty token →
	// caller stops, missing matches further on) and re-scanned the
	// filtered rows on the next page. A full page (len(rows)==pageSize)
	// means more may exist; a short page means the keyset is exhausted.
	var next string
	if len(rows) == int(pageSize) && len(rows) > 0 {
		next = uuid.UUID(rows[len(rows)-1].Object.ID.Bytes).String()
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
		n, err := r.q.CountObjects(ctx, pgUUID(args.TenantID), args.Collection, nil)
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
			args.Collection,
			nil, // state (no filter: count every state)
			nil, // prefix
			nil, // substr (CountObjectsArgs carries no raw filter to push down)
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
			o := objectFromSQLC(row.Object, row.CollectionName)
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
		if len(rows) < int(pageSize) {
			return matched, true, nil
		}
	}
}

// ListDistinctTags returns one page of tag facets for the Collection's live
// objects, sorted in SQL.
//
// Two axes are unbounded in the underlying data and both are capped here. The
// number of distinct KEYS is paged with a keyset cursor (afterKey, exclusive);
// the number of VALUES under one key is capped by valueLimit, because a single
// key can hold more values than a whole page of keys. The window function does
// the value cap inside the same scan rather than aggregating everything and
// slicing in Go — the point is to not build the full array in the first place.
//
// Both limits fetch one extra row to detect "there is more" without a second
// count query. A single pass over the rows via LATERAL jsonb_each_text; the
// per-Collection scope bounds the scan, and idx_objects_tags_gin covers tag
// predicates on the same table.
func (r *ObjectRepo) ListDistinctTags(
	ctx context.Context, tenantID uuid.UUID, collection, afterKey string, keyLimit, valueLimit int32,
) (object.DistinctTagPage, error) {
	if keyLimit <= 0 {
		keyLimit = 50
	}
	if valueLimit <= 0 {
		valueLimit = 100
	}
	rows, err := r.pool.Query(ctx,
		`WITH ranked AS (
		   SELECT DISTINCT t.key, t.value,
		          dense_rank() OVER (PARTITION BY t.key ORDER BY t.value) AS vrank
		     FROM objects o
		     JOIN collections c ON c.id = o.collection_id,
		          LATERAL jsonb_each_text(o.tags) AS t(key, value)
		    WHERE o.tenant_id = $1
		      AND c.name      = $2
		      AND o.state <> 'DELETED'
		      AND ($3 = '' OR t.key > $3)
		 ),
		 keys AS (
		   SELECT DISTINCT key FROM ranked ORDER BY key LIMIT $4
		 )
		 SELECT r.key,
		        array_agg(r.value ORDER BY r.value) FILTER (WHERE r.vrank <= $5) AS vals,
		        bool_or(r.vrank > $5) AS more
		   FROM ranked r
		   JOIN keys k ON k.key = r.key
		  GROUP BY r.key
		  ORDER BY r.key`,
		pgUUID(tenantID), collection, afterKey, keyLimit+1, valueLimit,
	)
	if err != nil {
		return object.DistinctTagPage{}, fmt.Errorf("list distinct tags: %w", err)
	}
	defer rows.Close()

	page := object.DistinctTagPage{
		Values:    map[string][]string{},
		Truncated: map[string]bool{},
	}
	for rows.Next() {
		var key string
		var values []string
		var more bool
		if err := rows.Scan(&key, &values, &more); err != nil {
			return object.DistinctTagPage{}, fmt.Errorf("scan distinct tag: %w", err)
		}
		page.Keys = append(page.Keys, key)
		page.Values[key] = values
		page.Truncated[key] = more
	}
	if err := rows.Err(); err != nil {
		return object.DistinctTagPage{}, fmt.Errorf("iterate distinct tags: %w", err)
	}

	// The extra key proves there is another page; drop it and cursor on the
	// last key actually RETURNED, not the overflow one.
	if len(page.Keys) > int(keyLimit) {
		overflow := page.Keys[keyLimit]
		page.Keys = page.Keys[:keyLimit]
		delete(page.Values, overflow)
		delete(page.Truncated, overflow)
		page.NextKey = page.Keys[len(page.Keys)-1]
	}
	return page, nil
}

// LookupBucket returns the physical S3 bucket bound to a tenant's
// Collection. Hits idx_collections_bucket_routing. After the schema baseline (001_initial_schema.sql)
// bucket_name is NOT NULL so a successful lookup always returns a
// non-empty string.
func (r *ObjectRepo) LookupBucket(ctx context.Context, tenantID uuid.UUID, collection string, write bool) (string, string, error) {
	// The query and the three gates live in bucket_resolver.go, shared with
	// the presign and multipart repos.
	return resolveBucket(ctx, r.pool, tenantID, collection, write)
}

// LookupBucketMeta returns the bucket binding plus versioning + lock flags
// in one trip. Hot-path call on every promote / delete; the JOIN hits
// idx_collections_bucket and the buckets PK.
//
// Versioning + Object Lock can be OVERRIDDEN per collection via the
// `collections.constraints` JSONB field. Override semantics:
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
func (r *ObjectRepo) LookupBucketMeta(ctx context.Context, tenantID uuid.UUID, collection string, write bool) (object.BucketMeta, error) {
	// JOIN storage_backends so a disabled backend is refused here too —
	// the resolution chokepoint covers every promote/delete/version path.
	// `write` splits the read_only (drain) gate by operation class (047).
	const q = `
		SELECT sb.name, bk.name,
		       bk.versioning_enabled,
		       bk.object_lock_enabled,
		       bk.object_lock_default_mode,
		       bk.object_lock_default_retention_seconds,
		       c.constraints, sb.enabled, sb.read_only, sb.events_enabled,
		       bk.provision_state
		FROM collections c
		JOIN buckets bk          ON bk.id = c.bucket_id
		JOIN storage_backends sb ON sb.id = bk.backend_id
		WHERE c.tenant_id = $1 AND c.name = $2
	`
	var (
		meta             object.BucketMeta
		constraintsJSON  []byte
		enabled          bool
		readOnly         bool
		provisionState   string
		defaultMode      *string
		retentionSeconds int64
	)
	if err := r.pool.QueryRow(ctx, q, pgUUID(tenantID), collection).Scan(
		&meta.BackendID, &meta.BucketName, &meta.VersioningEnabled, &meta.ObjectLockEnabled,
		&defaultMode, &retentionSeconds,
		&constraintsJSON, &enabled, &readOnly, &meta.EventsEnabled, &provisionState,
	); err != nil {
		if isNoRows(err) {
			return object.BucketMeta{}, fmt.Errorf("collection %q not found", collection)
		}
		return object.BucketMeta{}, fmt.Errorf("lookup bucket meta: %w", err)
	}
	if err := bucketOpAllowed(write, enabled, readOnly, provisionState); err != nil {
		return object.BucketMeta{}, err
	}
	if v, ok := readBoolOverride(constraintsJSON, "versioning_enabled"); ok {
		meta.VersioningEnabled = v
	}
	if v, ok := readBoolOverride(constraintsJSON, "object_lock_enabled"); ok {
		meta.ObjectLockEnabled = v
	}
	if defaultMode != nil {
		meta.ObjectLockDefaultMode = *defaultMode
	}
	if retentionSeconds > 0 {
		meta.ObjectLockDefaultRetention = time.Duration(retentionSeconds) * time.Second
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
// transaction with `SET LOCAL paladin.bypass_governance_retention = 'on'`. The
// enforce_object_version_lock trigger on object_versions reads this GUC.
//
// Compliance-mode rows still raise — by design.
// RunInTx runs fn inside a single transaction on the repo's pool. Event-
// producing handlers use it to write a lifecycle mutation and its outbox
// rows atomically (ADR-0003): fn does the repo write via the *Tx methods
// and the dispatch on the same tx; an error from either rolls back both.
func (r *ObjectRepo) RunInTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// HardDeleteWithBypassTx performs the bypass delete on the caller's tx,
// setting the governance-bypass GUC on that same tx (so the object_versions
// trigger permits removal of GOVERNANCE-locked rows). Used by the permanent-
// delete handler to enqueue paladin.object.deleted atomically (ADR-0003).
func (r *ObjectRepo) HardDeleteWithBypassTx(ctx context.Context, tx pgx.Tx, tenantID, objectID uuid.UUID, expectedVersion int64) error {
	if _, err := tx.Exec(ctx, "SET LOCAL paladin.bypass_governance_retention = 'on'"); err != nil {
		return fmt.Errorf("set bypass GUC: %w", err)
	}
	if err := hardDeleteObject(ctx, r.q.WithTx(tx), tenantID, objectID, expectedVersion); err != nil {
		return fmt.Errorf("hard delete (bypass): %w", err)
	}
	return nil
}

// HardDeleteTx runs HardDelete on the caller's tx so the permanent-delete
// handler can enqueue paladin.object.deleted atomically with the row removal
// (ADR-0003). The DELETE keeps its non-bypassable lock guard.
func (r *ObjectRepo) HardDeleteTx(ctx context.Context, tx pgx.Tx, tenantID, objectID uuid.UUID, expectedVersion int64) error {
	return hardDeleteObject(ctx, r.q.WithTx(tx), tenantID, objectID, expectedVersion)
}

// EnqueuePurgeTx records the byte-reclaim debt on the caller's tx — the same
// tx that removes the objects row, so the handle outlives the row it describes.
func (r *ObjectRepo) EnqueuePurgeTx(ctx context.Context, tx pgx.Tx, p object.PurgeDebt) error {
	if err := r.q.WithTx(tx).InsertPendingPurge(ctx,
		pgUUID(p.PurgeID), pgUUID(p.TenantID), pgUUID(p.ObjectID),
		p.BackendID, p.BucketName, p.Collection, p.Key,
	); err != nil {
		return fmt.Errorf("enqueue purge: %w", err)
	}
	return nil
}

// SettlePurgeTx clears one debt row. A zero-row result is not an error: the
// purge drainer may have settled the same debt first, which is the expected
// outcome of a race, not a fault.
func (r *ObjectRepo) SettlePurgeTx(ctx context.Context, tx pgx.Tx, purgeID uuid.UUID) error {
	if _, err := r.q.WithTx(tx).DeletePendingPurge(ctx, pgUUID(purgeID)); err != nil {
		return fmt.Errorf("settle purge: %w", err)
	}
	return nil
}

func hardDeleteObject(ctx context.Context, q *sqlc.Queries, tenantID, objectID uuid.UUID, expectedVersion int64) error {
	rows, err := q.HardDeleteObject(ctx, pgUUID(tenantID), pgUUID(objectID), expectedVersion)
	if err != nil {
		return fmt.Errorf("hard delete: %w", err)
	}
	if rows == 0 {
		return object.ErrVersionMismatch
	}
	return nil
}

// LiveCollision reports whether a non-DELETED row exists at (tenant, collection, key).
func (r *ObjectRepo) LiveCollision(ctx context.Context, tenantID uuid.UUID, collection, key string) (bool, error) {
	exists, err := r.q.CheckLiveCollision(ctx, pgUUID(tenantID), collection, key)
	if err != nil {
		return false, fmt.Errorf("live collision check: %w", err)
	}
	return exists, nil
}

func (r *ObjectRepo) getByID(ctx context.Context, tenantID, objectID uuid.UUID) (object.Object, error) {
	return r.getByIDWith(ctx, r.q, tenantID, objectID)
}

func (r *ObjectRepo) getByIDWith(ctx context.Context, q *sqlc.Queries, tenantID, objectID uuid.UUID) (object.Object, error) {
	row, err := q.GetObject(ctx, pgUUID(tenantID), pgUUID(objectID))
	if err != nil {
		return object.Object{}, err
	}
	return objectFromSQLC(row.Object, row.CollectionName), nil
}

func objectFromSQLC(o sqlc.Object, collectionName string) object.Object {
	var size int64
	if o.SizeBytes != nil {
		size = *o.SizeBytes
	}
	return object.Object{
		ObjectID:         uuidFrom(o.ID),
		TenantID:         uuidFrom(o.TenantID),
		Collection:       collectionName,
		Key:              o.Path,
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
// likeLiteral returns (s, true) when s is a non-empty pushdownable LIKE
// literal — i.e. contains no LIKE metacharacter (%, _, \) that would be
// reinterpreted as a wildcard. Empty or metachar-bearing literals return
// ok=false so the caller leaves the predicate to the in-memory CEL pass.
func likeLiteral(s string) (string, bool) {
	if s == "" {
		return "", false
	}
	if strings.ContainsAny(s, `%_\`) {
		return "", false
	}
	return s, true
}

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
