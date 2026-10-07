package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/objectpath"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// ObjectRepo satisfies object.Repository. BucketCompletionMode walks the
// collection → storage_backend path, so the adapter needs the raw pool.
type ObjectRepo struct {
	q    *sqlc.Queries
	pool *pgxpool.Pool
	// reads serves the lag-tolerant reads — ListObjects, CountObjects,
	// ListDistinctTags — from the read replica when one is enabled. Every
	// other method, writes and point lookups alike, stays on q/pool.
	reads Reader
}

// Reader runs a read on whichever database should serve it;
// postgres.ReadRouter is the production one.
type Reader interface {
	Read(ctx context.Context, fn func(db sqlc.DBTX) error) error
}

// primaryReader is the Reader for a repo with no replica.
type primaryReader struct{ db sqlc.DBTX }

func (p primaryReader) Read(_ context.Context, fn func(db sqlc.DBTX) error) error { return fn(p.db) }

func NewObjectRepo(q *sqlc.Queries, pool *pgxpool.Pool) *ObjectRepo {
	r := &ObjectRepo{q: q, pool: pool}
	if pool != nil {
		r.reads = primaryReader{db: pool}
	}
	return r
}

// WithReads routes the repo's lag-tolerant reads through rd. A nil rd keeps
// them on the primary.
func (r *ObjectRepo) WithReads(rd Reader) *ObjectRepo {
	if rd != nil {
		r.reads = rd
	}
	return r
}

// read runs fn through the configured Reader, or on q when the repo was built
// without a pool (unit tests over a mocked Querier).
func (r *ObjectRepo) read(ctx context.Context, fn func(q sqlc.Querier, db sqlc.DBTX) error) error {
	if r.reads == nil {
		return fn(r.q, r.pool)
	}
	return r.reads.Read(ctx, func(db sqlc.DBTX) error { return fn(sqlc.New(db), db) })
}

var _ objecth.Repository = (*ObjectRepo)(nil)

func (r *ObjectRepo) CreateObject(ctx context.Context, args objecth.CreateObjectArgs) (objecth.Object, error) {
	objectID := uuid.Must(uuid.NewV7())
	// Under the path lock, so a purge of bytes a deleted object left at this
	// path cannot delete this one's (package objectpath).
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return objecth.Object{}, fmt.Errorf("begin create object: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := r.q.WithTx(tx)
	if err := objectpath.Lock(ctx, qtx, args.TenantID, args.Collection, args.Key); err != nil {
		return objecth.Object{}, fmt.Errorf("lock object path: %w", err)
	}
	// The collection is known by name here; CreateObject stores its id.
	// Resolved rather than joined because sqlc rejects a parameter used only
	// inside a subquery in VALUES.
	collectionID, err := qtx.ResolveCollectionID(ctx, pgUUID(args.TenantID), args.Collection)
	if err != nil {
		return objecth.Object{}, fmt.Errorf("resolve collection %q: %w", args.Collection, err)
	}
	if err := qtx.CreateObject(ctx,
		pgUUID(objectID),
		pgUUID(args.TenantID),
		collectionID,
		args.Key,
		sqlc.ObjectStatePENDING,
		args.ContentType,
		args.SizeBytes,
		checksumAlgoInt(args.ChecksumAlgo),
		strPtr(args.ChecksumValue),
		encodeMap(args.Metadata),
		encodeMap(args.Tags),
		strPtr(args.ExternalRef),
		pgTS(args.PresignExpiresAt),
	); err != nil {
		return objecth.Object{}, fmt.Errorf("create object: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return objecth.Object{}, fmt.Errorf("commit create object: %w", err)
	}
	return r.getByID(ctx, args.TenantID, objectID)
}

// FindByName reads one of the tenant's objects; objecth.ErrObjectNotFound
// when it has none by that id — an id that does not parse names none either.
func (r *ObjectRepo) FindByName(ctx context.Context, tenantID uuid.UUID, collection, objectID string) (objecth.Object, error) {
	id, err := uuid.Parse(objectID)
	if err != nil {
		return objecth.Object{}, objecth.ErrObjectNotFound
	}
	obj, err := r.getByID(ctx, tenantID, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return objecth.Object{}, objecth.ErrObjectNotFound
	}
	return obj, err
}

func (r *ObjectRepo) ObjectLock(ctx context.Context, tenantID, objectID uuid.UUID) (objecth.ObjectLock, error) {
	row, err := r.q.GetObjectLockState(ctx, pgUUID(tenantID), pgUUID(objectID))
	if err != nil {
		// No-rows is unexpected here — the delete path already resolved
		// the object via FindByName — so surface it as a plain error.
		return objecth.ObjectLock{}, fmt.Errorf("get object lock state: %w", err)
	}
	return objecth.ObjectLock{
		Mode:        lockModeFromSQL(row.LockMode),
		RetainUntil: timePtr(row.LockRetainUntil),
		LegalHold:   row.LegalHold,
	}, nil
}

func (r *ObjectRepo) FindByIDs(ctx context.Context, tenantID uuid.UUID, ids []uuid.UUID) ([]objecth.Object, error) {
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
	out := make([]objecth.Object, 0, len(rows))
	for _, row := range rows {
		out = append(out, objectFromSQLC(row.Object, row.CollectionName))
	}
	return out, nil
}

func (r *ObjectRepo) FindByPath(ctx context.Context, tenantID uuid.UUID, collection, key string) (objecth.Object, error) {
	row, err := r.q.LookupObjectByKey(ctx, pgUUID(tenantID), collection, key)
	if err != nil {
		return objecth.Object{}, err
	}
	return objectFromSQLC(row.Object, row.CollectionName), nil
}

func (r *ObjectRepo) UpdateMetadata(ctx context.Context, args objecth.UpdateMetadataArgs) (objecth.Object, error) {
	return r.updateMetadata(ctx, r.q, args)
}

// UpdateMetadataTx runs UpdateMetadata on the caller's transaction so the
// handler can write the paladin.object.updated outbox rows atomically with the
// row update — closing the dual-write crash window (ADR-0003).
func (r *ObjectRepo) UpdateMetadataTx(ctx context.Context, tx pgx.Tx, args objecth.UpdateMetadataArgs) (objecth.Object, error) {
	return r.updateMetadata(ctx, r.q.WithTx(tx), args)
}

func (r *ObjectRepo) updateMetadata(ctx context.Context, q *sqlc.Queries, args objecth.UpdateMetadataArgs) (objecth.Object, error) {
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
		return objecth.Object{}, fmt.Errorf("update metadata: %w", err)
	}
	if rows == 0 {
		return objecth.Object{}, objecth.ErrVersionMismatch
	}
	return r.getByIDWith(ctx, q, args.TenantID, args.ObjectID)
}

func (r *ObjectRepo) ListObjects(ctx context.Context, args objecth.ListObjectsArgs) ([]objecth.Object, string, error) {
	pageSize := pageSizeOrDefault(args.PageSize)
	var afterID uuid.UUID
	if args.PageToken != "" {
		id, err := uuid.Parse(args.PageToken)
		if err != nil {
			return nil, "", fmt.Errorf("parse page_token: %w", err)
		}
		afterID = id
	}

	rows, nextID, more, err := r.candidates(ctx, args.TenantID, args.Collection,
		objectBranchHints(args.Filter), afterID, pageSize)
	if err != nil {
		return nil, "", fmt.Errorf("list objects: %w", err)
	}
	out := make([]objecth.Object, 0, len(rows))
	for _, row := range rows {
		o := objectFromSQLC(row.Object, row.CollectionName)
		if args.CompiledCEL != nil && !rowMatches(args.CompiledCEL, o) {
			continue
		}
		out = append(out, o)
	}
	// Page token advances by the last FETCHED candidate, NOT the last
	// matching one. Deriving it from `out` truncated the listing when a
	// full DB page was entirely CEL-filtered (len(out)==0 → empty token →
	// caller stops, missing matches further on) and re-scanned the
	// filtered rows on the next page.
	var next string
	if more {
		next = nextID.String()
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
func (r *ObjectRepo) CountObjects(ctx context.Context, args objecth.CountObjectsArgs) (int64, bool, error) {
	if args.CompiledCEL == nil {
		var n int64
		err := r.read(ctx, func(q sqlc.Querier, _ sqlc.DBTX) error {
			var err error
			n, err = q.CountObjects(ctx, pgUUID(args.TenantID), args.Collection, nil)
			return err
		})
		if err != nil {
			return 0, false, fmt.Errorf("count objects: %w", err)
		}
		return n, true, nil
	}

	// The same pushdown as ListObjects: the scan cap counts rows READ, so
	// every row the query drops is headroom for one that might match, and a
	// selective filter that used to stop at "10000+" is now counted exactly.
	branches := objectBranchHints(args.Filter)
	const pageSize int32 = 500
	var (
		matched int64
		scanned int64
		afterID uuid.UUID
	)
	for {
		rows, nextID, more, err := r.candidates(ctx, args.TenantID, args.Collection, branches, afterID, pageSize)
		if err != nil {
			return 0, false, fmt.Errorf("count objects scan: %w", err)
		}
		for _, row := range rows {
			o := objectFromSQLC(row.Object, row.CollectionName)
			if rowMatches(args.CompiledCEL, o) {
				matched++
			}
			scanned++
			if scanned >= countScanCap {
				return matched, false, nil
			}
		}
		if !more {
			return matched, true, nil
		}
		afterID = nextID
	}
}

// candidates returns one keyset page of objects that may match: those
// after afterID, in id order, admitted by at least one branch.
//
// One branch is one query. Several — a filter with `||` or `in` — are one
// query each, merged by mergeCandidatePages.
//
// more reports that rows may follow next; next is the cursor for them.
func (r *ObjectRepo) candidates(
	ctx context.Context, tenantID uuid.UUID, collection string,
	branches []objectHints, afterID uuid.UUID, pageSize int32,
) (rows []sqlc.ListObjectsRow, next uuid.UUID, more bool, err error) {
	err = r.read(ctx, func(q sqlc.Querier, _ sqlc.DBTX) error {
		rows, next, more = nil, uuid.UUID{}, false
		pages := make([][]sqlc.ListObjectsRow, 0, len(branches))
		for _, h := range branches {
			page, err := q.ListObjects(ctx, collection, pgUUID(tenantID),
				h.state, h.prefix, h.substr,
				h.contentType, h.contentTypePrefix,
				h.tags, h.metadata,
				pgUUID(afterID), pageSize)
			if err != nil {
				return err
			}
			pages = append(pages, page)
		}
		rows, next, more = mergeCandidatePages(pages, int(pageSize))
		return nil
	})
	return rows, next, more, err
}

// mergeCandidatePages merges per-branch keyset pages (each in id order, each
// at most pageSize long) into one page: the union, in id order, cut to
// pageSize.
//
// The cut is what keeps the walk from skipping rows. A branch that filled its
// page may have more after its last id; but its pageSize rows are all in the
// union, so the union's first pageSize ids never pass that last id, and
// nothing that branch has not yet returned can sort before the cursor.
// more is set when any branch filled its page or the union was cut.
func mergeCandidatePages(pages [][]sqlc.ListObjectsRow, pageSize int) (rows []sqlc.ListObjectsRow, next uuid.UUID, more bool) {
	idOf := func(r sqlc.ListObjectsRow) uuid.UUID { return uuid.UUID(r.Object.ID.Bytes) }
	if len(pages) == 1 {
		rows = pages[0]
	} else {
		seen := map[uuid.UUID]bool{}
		for _, page := range pages {
			for _, row := range page {
				if id := idOf(row); !seen[id] {
					seen[id] = true
					rows = append(rows, row)
				}
			}
		}
		// Postgres orders uuid bytewise, as bytes.Compare does.
		sort.Slice(rows, func(i, j int) bool {
			a, b := idOf(rows[i]), idOf(rows[j])
			return bytes.Compare(a[:], b[:]) < 0
		})
	}
	for _, page := range pages {
		if len(page) == pageSize {
			more = true
		}
	}
	if len(rows) > pageSize {
		rows, more = rows[:pageSize], true
	}
	if more && len(rows) > 0 {
		return rows, idOf(rows[len(rows)-1]), true
	}
	return rows, uuid.UUID{}, false
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
) (objecth.DistinctTagPage, error) {
	if keyLimit <= 0 {
		keyLimit = 50
	}
	if valueLimit <= 0 {
		valueLimit = 100
	}
	var page objecth.DistinctTagPage
	err := r.read(ctx, func(_ sqlc.Querier, db sqlc.DBTX) error {
		// Built inside the closure: a replica failure re-runs it on the
		// primary, which must start from an empty page.
		page = objecth.DistinctTagPage{
			Values:    map[string][]string{},
			Truncated: map[string]bool{},
		}
		rows, err := db.Query(ctx,
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
			return fmt.Errorf("list distinct tags: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var key string
			var values []string
			var more bool
			if err := rows.Scan(&key, &values, &more); err != nil {
				return fmt.Errorf("scan distinct tag: %w", err)
			}
			page.Keys = append(page.Keys, key)
			page.Values[key] = values
			page.Truncated[key] = more
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate distinct tags: %w", err)
		}
		return nil
	})
	if err != nil {
		return objecth.DistinctTagPage{}, err
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
func (r *ObjectRepo) LookupBucketMeta(ctx context.Context, tenantID uuid.UUID, collection string, write bool) (objecth.BucketMeta, error) {
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
		       bk.provision_state, bk.constraints
		FROM collections c
		JOIN buckets bk          ON bk.id = c.bucket_id
		JOIN storage_backends sb ON sb.id = bk.backend_id
		WHERE c.tenant_id = $1 AND c.name = $2
	`
	var (
		meta             objecth.BucketMeta
		constraintsJSON  []byte
		enabled          bool
		readOnly         bool
		provisionState   string
		defaultMode      *string
		retentionSeconds int64
		bucketConstraint []byte
	)
	if err := r.pool.QueryRow(ctx, q, pgUUID(tenantID), collection).Scan(
		&meta.BackendID, &meta.BucketName, &meta.VersioningEnabled, &meta.ObjectLockEnabled,
		&defaultMode, &retentionSeconds,
		&constraintsJSON, &enabled, &readOnly, &meta.EventsEnabled, &provisionState, &bucketConstraint,
	); err != nil {
		if isNoRows(err) {
			return objecth.BucketMeta{}, fmt.Errorf("collection %q not found", collection)
		}
		return objecth.BucketMeta{}, fmt.Errorf("lookup bucket meta: %w", err)
	}
	if err := bucketOpAllowed(write, enabled, readOnly, provisionState); err != nil {
		return objecth.BucketMeta{}, err
	}
	// Fail closed on a document that does not decode: these are limits the
	// bucket owner set, and reading a broken one as "no limits" would admit
	// exactly what they were set to refuse.
	if len(bucketConstraint) > 0 {
		if err := json.Unmarshal(bucketConstraint, &meta.Constraints); err != nil {
			return objecth.BucketMeta{}, fmt.Errorf("bucket %q constraints: %w", meta.BucketName, err)
		}
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
func (r *ObjectRepo) EnqueuePurgeTx(ctx context.Context, tx pgx.Tx, p objecth.PurgeDebt) error {
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

// PurgeBytesTx decides the purge under the path lock (package objectpath).
func (r *ObjectRepo) PurgeBytesTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, collection, key string, deleteBytes func() error) (bool, error) {
	verdict, err := objectpath.Check(ctx, r.q.WithTx(tx), tenantID, collection, key)
	if err != nil {
		return false, fmt.Errorf("purge path check: %w", err)
	}
	switch verdict {
	case objectpath.Delete:
		if err := deleteBytes(); err != nil {
			return false, err
		}
		return true, nil
	case objectpath.Superseded:
		return true, nil
	default:
		return false, nil
	}
}

func hardDeleteObject(ctx context.Context, q *sqlc.Queries, tenantID, objectID uuid.UUID, expectedVersion int64) error {
	rows, err := q.HardDeleteObject(ctx, pgUUID(tenantID), pgUUID(objectID), expectedVersion)
	if err != nil {
		return fmt.Errorf("hard delete: %w", err)
	}
	if rows == 0 {
		return objecth.ErrVersionMismatch
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

func (r *ObjectRepo) getByID(ctx context.Context, tenantID, objectID uuid.UUID) (objecth.Object, error) {
	return r.getByIDWith(ctx, r.q, tenantID, objectID)
}

func (r *ObjectRepo) getByIDWith(ctx context.Context, q *sqlc.Queries, tenantID, objectID uuid.UUID) (objecth.Object, error) {
	row, err := q.GetObject(ctx, pgUUID(tenantID), pgUUID(objectID))
	if err != nil {
		return objecth.Object{}, err
	}
	return objectFromSQLC(row.Object, row.CollectionName), nil
}

func objectFromSQLC(o sqlc.Object, collectionName string) objecth.Object {
	var size int64
	if o.SizeBytes != nil {
		size = *o.SizeBytes
	}
	return objecth.Object{
		ObjectID:              uuidFrom(o.ID),
		TenantID:              uuidFrom(o.TenantID),
		Collection:            collectionName,
		Key:                   o.Path,
		State:                 statemachine.State(string(o.State)),
		ContentType:           o.ContentType,
		SizeBytes:             size,
		ETag:                  derefStr(o.Etag),
		ChecksumAlgo:          checksumAlgoName(o.ChecksumAlgorithm),
		Checksum:              derefStr(o.Checksum),
		ChecksumPartSizeBytes: derefInt64(o.ChecksumPartSizeBytes),
		Sequencer:             derefStr(o.Sequencer),
		Metadata:              decodeMap(o.Metadata),
		Tags:                  decodeMap(o.Tags),
		ExternalRef:           derefStr(o.ExternalRef),
		ResourceVersion:       o.ResourceVersion,
		CreatedAt:             timeFrom(o.CreatedAt),
		UpdatedAt:             timeFrom(o.UpdatedAt),
		CommittedAt:           timePtr(o.CommittedAt),
		TerminatedAt:          timePtr(o.TerminatedAt),
		PresignExpiresAt:      timePtr(o.PresignExpiresAt),
		Taint:                 o.Taint,
	}
}

// likeEscape returns s with the LIKE metacharacters (%, _, \) escaped by a
// backslash — Postgres' default LIKE escape — so the pattern matches s
// literally. ok is false for an empty s, which narrows nothing.
//
// Escaping rather than dropping matters for search: object keys are full of
// underscores (`report_2026_q3.pdf`), and a dropped hint sent the console's
// search to an unfiltered page scan.
func likeEscape(s string) (string, bool) {
	if s == "" {
		return "", false
	}
	if !strings.ContainsAny(s, `%_\`) {
		return s, true
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	for _, r := range s {
		if r == '%' || r == '_' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String(), true
}

// objectBranchHints extracts the pushdown from a raw CEL filter: one
// objectHints per disjunct of cel.ExtractObjectBranches, at least one. The
// compiled program stays authoritative over every fetched row, so a hint may
// only narrow; a parse error (reported by the handler's own compile) yields a
// single branch that narrows nothing.
func objectBranchHints(filter string) []objectHints {
	branches, err := cel.ExtractObjectBranches(filter)
	if err != nil || len(branches) == 0 {
		return []objectHints{{}}
	}
	out := make([]objectHints, len(branches))
	for i, pd := range branches {
		out[i] = hintsFrom(pd)
	}
	return out
}

func hintsFrom(pd cel.ObjectPushdown) objectHints {
	var h objectHints
	if pd.StateEq != "" {
		v := sqlc.ObjectState(pd.StateEq)
		h.state = &v
	}
	if p, ok := likeEscape(pd.KeyPrefix); ok {
		h.prefix = &p
	}
	if p, ok := likeEscape(pd.KeyContains); ok {
		h.substr = &p
	}
	if pd.ContentTypeEq != "" {
		v := pd.ContentTypeEq
		h.contentType = &v
	}
	if p, ok := likeEscape(pd.ContentTypePrefix); ok {
		h.contentTypePrefix = &p
	}
	if len(pd.TagsEq) > 0 {
		h.tags = encodeMap(pd.TagsEq)
	}
	if len(pd.MetadataEq) > 0 {
		h.metadata = encodeMap(pd.MetadataEq)
	}
	return h
}

// objectHints is the SQL-expressible subset of an object filter, in the shape
// the ListObjects query takes. The zero value narrows nothing.
type objectHints struct {
	state             *sqlc.ObjectState
	prefix            *string
	substr            *string
	contentType       *string
	contentTypePrefix *string
	tags              []byte // jsonb object for `tags @>`; nil = no predicate
	metadata          []byte // jsonb object for `metadata @>`
}

// celVars is the object as a filter sees it — cel.ObjectVars, shared with the
// lifecycle worker so a rule and a listing filter can never disagree.
func celVars(o objecth.Object) map[string]any {
	return cel.ObjectVars(cel.ObjectRow{
		Key:         o.Key,
		State:       string(o.State),
		ContentType: o.ContentType,
		SizeBytes:   o.SizeBytes,
		Tags:        o.Tags,
		Metadata:    o.Metadata,
		ExternalRef: o.ExternalRef,
		CreatedAt:   o.CreatedAt,
		UpdatedAt:   o.UpdatedAt,
		CommittedAt: o.CommittedAt,
	})
}

// rowMatches evaluates a filter against one object. An evaluation error is a
// non-match, not a failed request: the expression was type-checked when it
// was compiled, so what remains is a value this object does not have — a tag
// key it lacks, a committed_at it has not got yet. Failing the whole page on
// that made `tags["env"] == "prod"` unusable on any collection where one
// object was untagged. The lifecycle worker has always treated it this way.
func rowMatches(prog cel.Program, o objecth.Object) bool {
	ok, err := cel.Match(prog, celVars(o))
	return err == nil && ok
}
