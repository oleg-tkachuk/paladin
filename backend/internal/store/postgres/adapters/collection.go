package adapters

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"

	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	objectkey "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/collectionh"
	"github.com/oleg-tkachuk/paladin/backend/internal/publicread"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgerr"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/schema"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// CollectionRepo satisfies objectkey.Repository. Stats require an aggregation that
// isn't expressed in the sqlc query set; we issue it directly through the
// pool.
type CollectionRepo struct {
	q    *sqlc.Queries
	pool *pgxpool.Pool
}

func NewCollectionRepo(q *sqlc.Queries, pool *pgxpool.Pool) *CollectionRepo {
	return &CollectionRepo{q: q, pool: pool}
}

var _ objectkey.Repository = (*CollectionRepo)(nil)

// RunInTx runs fn in one transaction on the repo's pool — the ADR-0003 seam
// the handler uses to write a collection mutation and its outbox rows
// atomically. The *Tx mutation methods run on the same tx.
func (r *CollectionRepo) RunInTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("collection: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("collection: commit tx: %w", err)
	}
	return nil
}

func (r *CollectionRepo) Create(ctx context.Context, args objectkey.CreateCollectionArgs) (objectkey.Collection, error) {
	return r.createWith(ctx, r.q, args)
}

// CreateTx runs Create on the caller's tx (ADR-0003).
func (r *CollectionRepo) CreateTx(ctx context.Context, tx pgx.Tx, args objectkey.CreateCollectionArgs) (objectkey.Collection, error) {
	return r.createWith(ctx, r.q.WithTx(tx), args)
}

func (r *CollectionRepo) createWith(ctx context.Context, q *sqlc.Queries, args objectkey.CreateCollectionArgs) (objectkey.Collection, error) {
	// collections.lifecycle_rules is JSONB NOT NULL with default '[]'. The SQL
	// INSERT binds this column explicitly, so a nil []byte would surface as
	// NULL and violate the constraint. Normalize to an empty JSON array.
	rules := args.LifecycleRules
	if len(rules) == 0 {
		rules = []byte("[]")
	}
	if err := q.CreateCollection(ctx,
		pgUUID(args.TenantID),
		args.Collection,
		args.DisplayName,
		args.BackendID,
		args.BucketName,
		args.CedarPolicy,
		rules,
		args.PublicRead,
		args.CacheControl,
	); err != nil {
		// A name the tenant already uses is an ordinary answer to an ordinary
		// request. Unmapped it arrived as CodeInternal carrying
		// `duplicate key value violates unique constraint
		// "collections_tenant_id_name_key" (SQLSTATE 23505)` — a 500 for the
		// database working exactly as designed. The delete path in this same
		// file has classified its constraint since it was written; the create
		// path never did.
		if pgerr.Is(err, pgerr.UniqueViolation) &&
			pgerr.ConstraintIs(err, schema.CollectionsNameUnique) {
			return objectkey.Collection{}, fmt.Errorf("%w: %q",
				objectkey.ErrCollectionExists, args.Collection)
		}
		// The bucket is resolved by name inside the INSERT, so one that is not
		// registered — or was deleted after the caller listed it — leaves
		// bucket_id NULL. That is a missing resource, not a server fault.
		if pgerr.Is(err, pgerr.NotNullViolation) &&
			pgerr.Column(err) == schema.CollectionsBucketIDColumn {
			return objectkey.Collection{}, fmt.Errorf("%w: %s/%s",
				objectkey.ErrBucketNotFound, args.BackendID, args.BucketName)
		}
		if err := publicRuleError(err); err != nil {
			return objectkey.Collection{}, err
		}
		if err := tenancyError(err); err != nil {
			return objectkey.Collection{}, err
		}
		return objectkey.Collection{}, fmt.Errorf("create collection: %w", err)
	}
	return r.getWith(ctx, q, args.TenantID, args.Collection)
}

func (r *CollectionRepo) Get(ctx context.Context, tenantID uuid.UUID, collection string) (objectkey.Collection, error) {
	return r.getWith(ctx, r.q, tenantID, collection)
}

func (r *CollectionRepo) getWith(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, collection string) (objectkey.Collection, error) {
	row, err := q.GetCollection(ctx, pgUUID(tenantID), collection)
	if err != nil {
		return objectkey.Collection{}, err
	}
	return collectionFromSQLC(row.Collection, row.BackendName, row.BucketName), nil
}

func (r *CollectionRepo) Update(ctx context.Context, args objectkey.UpdateCollectionArgs) (objectkey.Collection, error) {
	return r.updateWith(ctx, r.q, args)
}

// UpdateTx runs Update on the caller's tx (ADR-0003).
func (r *CollectionRepo) UpdateTx(ctx context.Context, tx pgx.Tx, args objectkey.UpdateCollectionArgs) (objectkey.Collection, error) {
	return r.updateWith(ctx, r.q.WithTx(tx), args)
}

func (r *CollectionRepo) updateWith(ctx context.Context, q *sqlc.Queries, args objectkey.UpdateCollectionArgs) (objectkey.Collection, error) {
	var policyHash []byte
	if args.CedarPolicy != nil {
		sum := sha256.Sum256([]byte(*args.CedarPolicy))
		policyHash = sum[:]
	}
	rows, err := q.UpdateCollection(ctx,
		pgUUID(args.TenantID),
		args.Collection,
		args.DisplayName,
		args.CedarPolicy,
		policyHash,
		args.LifecycleRules,
		args.ExpectedVersion,
	)
	if err != nil {
		return objectkey.Collection{}, fmt.Errorf("update collection: %w", err)
	}
	if rows == 0 {
		return objectkey.Collection{}, objectkey.ErrVersionMismatch
	}
	return r.getWith(ctx, q, args.TenantID, args.Collection)
}

func (r *CollectionRepo) Delete(ctx context.Context, tenantID uuid.UUID, collection string, expectedVersion int64) error {
	return r.deleteWith(ctx, r.q, tenantID, collection, expectedVersion)
}

// DeleteTx runs Delete on the caller's tx (ADR-0003).
func (r *CollectionRepo) DeleteTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, collection string, expectedVersion int64) error {
	return r.deleteWith(ctx, r.q.WithTx(tx), tenantID, collection, expectedVersion)
}

func (r *CollectionRepo) deleteWith(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, collection string, expectedVersion int64) error {
	rows, err := q.DeleteCollection(ctx, pgUUID(tenantID), collection, expectedVersion)
	if err != nil {
		// FK violation: objects.tenant_id_collection_fkey still
		// references this row. The constraint is ON DELETE RESTRICT
		// so we can't cascade — surface as a typed sentinel and let
		// the handler return FAILED_PRECONDITION with a clear hint
		// rather than the opaque "internal: ERROR: ... 23001" leak.
		if pgerr.Is(err, pgerr.ForeignKeyViolation) {
			return objectkey.ErrCollectionHasObjects
		}
		return fmt.Errorf("delete collection: %w", err)
	}
	if rows == 0 {
		return objectkey.ErrVersionMismatch
	}
	return nil
}

// Holding counts a collection's live objects and those in its trash —
// CountObjects's two readings of the same rows, which the foreign key from
// objects holds the collection by alike.
func (r *CollectionRepo) Holding(ctx context.Context, tenantID uuid.UUID, collection string) (objectkey.CollectionHolding, error) {
	live, err := r.q.CountObjects(ctx, pgUUID(tenantID), collection, nil, true)
	if err != nil {
		return objectkey.CollectionHolding{}, fmt.Errorf("count live objects: %w", err)
	}
	trashed := sqlc.ObjectStateDELETED
	inTrash, err := r.q.CountObjects(ctx, pgUUID(tenantID), collection, &trashed, false)
	if err != nil {
		return objectkey.CollectionHolding{}, fmt.Errorf("count trashed objects: %w", err)
	}
	return objectkey.CollectionHolding{Live: live, Trashed: inTrash}, nil
}

func (r *CollectionRepo) Rebind(ctx context.Context, tenantID uuid.UUID, collection, backendID, bucketName string, expectedVersion int64) error {
	rows, err := r.q.BindCollectionToBucket(ctx, pgUUID(tenantID), collection, backendID, bucketName, expectedVersion)
	if err != nil {
		if rule := publicRuleError(err); rule != nil {
			return rule
		}
		if refused := tenancyError(err); refused != nil {
			return refused
		}
		return fmt.Errorf("rebind collection: %w", err)
	}
	if rows == 0 {
		return objectkey.ErrVersionMismatch
	}
	return nil
}

func (r *CollectionRepo) List(ctx context.Context, args objectkey.ListCollectionsArgs) ([]objectkey.Collection, string, error) {
	pageSize := pageSizeOrDefault(args.PageSize)

	// Fast path: the sqlc-generated query handles (tenant_id, after,
	// page_size) — used when no (backend, bucket) filter is set. The
	// filter is rare enough (only the storage-first browser hits it)
	// that hand-writing a parameterised query here keeps the sqlc
	// surface lean. When the filter is present we go through the
	// raw pool with a parameterised UPDATE-safe statement.
	if args.BackendID == "" && args.BucketName == "" {
		var after *string
		var afterTenant pgtype.UUID
		if args.PageToken != "" {
			tenant, name := decodeCollectionCursor(args.PageToken, args.TenantID)
			after, afterTenant = &name, pgUUID(tenant)
		}
		// Pushdown: see admin_bucket.go — the handler's CEL pass over the
		// page stays authoritative, these only narrow the scan.
		pd := hints(cel.CollectionSchema, args.Filter)
		nameIn, nameLike := pd.StringHint("collection")
		displayIn, displayLike := pd.StringHint("display_name")
		backendIn, _ := pd.StringHint("storage_backend")

		createdGTE, createdLTE := createdBounds(pd)
		// Only the `like` half — see admin_bucket.go.
		_, searchLike := pd.StringHint("search")
		rows, err := r.q.ListCollections(ctx, pgUUIDOptional(args.TenantID), after, afterTenant,
			nameIn, nameLike, displayIn, displayLike, searchLike, backendIn,
			createdGTE, createdLTE, pageSize)
		if err != nil {
			return nil, "", fmt.Errorf("list collections: %w", err)
		}
		out := make([]objectkey.Collection, 0, len(rows))
		for _, row := range rows {
			out = append(out, collectionFromSQLC(row.Collection, row.BackendName, row.BucketName))
		}
		return out, nextCollectionCursor(out, pageSize), nil
	}

	// Filtered path: (backend, bucket) narrow. Tenant filter is
	// optional here — the storage-first browser passes uuid.Nil to
	// get cross-tenant results on a specific bucket. Cursor is on
	// (tenant_id, collection) so pagination stays deterministic
	// across tenants.
	const filteredQ = `
		SELECT c.tenant_id, c.name, c.display_name, c.bucket_id,
		       c.cedar_policy, c.lifecycle_rules, c.public_read, c.cache_control,
		       c.resource_version, c.created_at, c.updated_at,
		       sb.name AS backend_name, b.name AS bucket_name
		  FROM collections c
		  JOIN buckets b           ON b.id = c.bucket_id
		  JOIN storage_backends sb ON sb.id = b.backend_id
		 WHERE sb.name = $1
		   AND b.name  = $2
		   AND ($3::uuid IS NULL OR c.tenant_id = $3)
		   AND ($4::text IS NULL OR (c.tenant_id, c.name) > ($7::uuid, $4))
		   -- The derived search field, spelled to match cel.SearchText.
		   -- (no backticks in this comment: the Go literal around it is one.)
		   -- This path narrowed on nothing from the filter before, so the same
		   -- search pushed down on the tenant-wide list and not here — one
		   -- query fast, its sibling reading every page, for reasons invisible
		   -- from the caller.
		   AND ($6::text IS NULL
		        OR lower(c.name COLLATE "C") || chr(10) || lower(coalesce(c.display_name, '') COLLATE "C")
		           LIKE $6)
		 ORDER BY c.tenant_id, c.name
		 LIMIT $5
	`
	var tenantFilter any
	if args.TenantID != uuid.Nil {
		tenantFilter = pgUUID(args.TenantID)
	}
	var afterTok, afterTenantTok any
	if args.PageToken != "" {
		tenant, name := decodeCollectionCursor(args.PageToken, args.TenantID)
		afterTok, afterTenantTok = name, pgUUID(tenant)
	}
	var searchTok any
	if _, like := hints(cel.CollectionSchema, args.Filter).StringHint("search"); like != nil {
		searchTok = *like
	}
	rows, err := r.pool.Query(ctx, filteredQ,
		args.BackendID, args.BucketName, tenantFilter, afterTok, pageSize, searchTok, afterTenantTok)
	if err != nil {
		return nil, "", fmt.Errorf("list collections (filtered): %w", err)
	}
	defer rows.Close()
	out := make([]objectkey.Collection, 0)
	for rows.Next() {
		var row sqlc.Collection
		var backendName, bucketName string
		if err := rows.Scan(
			&row.TenantID, &row.Name, &row.DisplayName,
			&row.BucketID,
			&row.CedarPolicy, &row.LifecycleRules, &row.PublicRead, &row.CacheControl,
			&row.ResourceVersion, &row.CreatedAt, &row.UpdatedAt,
			&backendName, &bucketName,
		); err != nil {
			return nil, "", fmt.Errorf("list collections: scan: %w", err)
		}
		out = append(out, collectionFromSQLC(row, backendName, bucketName))
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("list collections: rows err: %w", err)
	}
	return out, nextCollectionCursor(out, pageSize), nil
}

// A collection list's page token is "<tenant id>:<collection>": the rows run
// in (tenant_id, name) order, and a name alone cannot say where a page that
// ends in one tenant resumes in the next. A token without the tenant — the
// shape issued before — resumes within the listed tenant.
const collectionCursorSep = ":"

// nextCollectionCursor is the token for the page after out, or "" when out
// is the last one.
func nextCollectionCursor(out []objectkey.Collection, pageSize int32) string {
	if len(out) == 0 || len(out) < int(pageSize) {
		return ""
	}
	last := out[len(out)-1]
	return last.TenantID.String() + collectionCursorSep + last.Collection
}

// decodeCollectionCursor splits a page token into the tenant and collection
// the next page starts after; a bare name belongs to fallback.
func decodeCollectionCursor(tok string, fallback uuid.UUID) (uuid.UUID, string) {
	head, name, ok := strings.Cut(tok, collectionCursorSep)
	if ok {
		if tenant, err := uuid.Parse(head); err == nil {
			return tenant, name
		}
	}
	return fallback, tok
}

// Stats runs a single grouped aggregation. Returning (AVAILABLE, PENDING,
// DELETED) counts keeps the row count O(1) regardless of collection size.
func (r *CollectionRepo) Usage(ctx context.Context, keys []objectkey.CollectionKey) (map[objectkey.CollectionKey]objectkey.CollectionUsage, error) {
	// One row per collection with an available object, from
	// objects_collection_available_usage_idx (095); the pairs are unnested
	// so a page across tenants is still one query.
	const q = `
		SELECT c.tenant_id, c.name, COUNT(*), COALESCE(SUM(o.size_bytes), 0), MAX(o.committed_at)
		FROM unnest($1::uuid[], $2::text[]) AS k(tenant_id, name)
		JOIN collections c ON c.tenant_id = k.tenant_id AND c.name = k.name
		JOIN objects o ON o.collection_id = c.id AND o.state = 'AVAILABLE'
		GROUP BY c.tenant_id, c.name
	`
	out := make(map[objectkey.CollectionKey]objectkey.CollectionUsage, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	tenants := make([]pgtype.UUID, len(keys))
	names := make([]string, len(keys))
	for i, k := range keys {
		tenants[i] = pgUUID(k.TenantID)
		names[i] = k.Collection
	}
	rows, err := r.pool.Query(ctx, q, tenants, names)
	if err != nil {
		return nil, fmt.Errorf("collection usage: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			tenant pgtype.UUID
			name   string
			u      objectkey.CollectionUsage
			last   pgtype.Timestamptz
		)
		if err := rows.Scan(&tenant, &name, &u.ObjectCount, &u.TotalBytes, &last); err != nil {
			return nil, fmt.Errorf("collection usage: %w", err)
		}
		if last.Valid {
			t := last.Time
			u.LastCommittedAt = &t
		}
		out[objectkey.CollectionKey{TenantID: uuidFrom(tenant), Collection: name}] = u
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("collection usage: %w", err)
	}
	return out, nil
}

// Named for what it converts, not for what it used to: backend and bucket
// names come from the caller, because sqlc.Collection carries bucket_id.
func collectionFromSQLC(b sqlc.Collection, backendName, bucketName string) objectkey.Collection {
	return objectkey.Collection{
		TenantID:        uuidFrom(b.TenantID),
		Collection:      b.Name,
		DisplayName:     b.DisplayName,
		BackendID:       backendName,
		BucketName:      bucketName,
		CedarPolicy:     b.CedarPolicy,
		LifecycleRules:  b.LifecycleRules,
		PublicRead:      b.PublicRead,
		CacheControl:    b.CacheControl,
		ResourceVersion: b.ResourceVersion,
		CreatedAt:       timeFrom(b.CreatedAt),
		UpdatedAt:       timeFrom(b.UpdatedAt),
	}
}

// publicRuleError turns a violation of a public-collection rule the schema
// enforces (049_public_collections.sql) into publicread.ErrRule, and returns
// nil for any other error.
func publicRuleError(err error) error {
	if !pgerr.Is(err, pgerr.CheckViolation) {
		return nil
	}
	switch {
	case pgerr.ConstraintIs(err, schema.CollectionsBucketVisibility):
		return publicread.Rulef("a public collection binds only to a public bucket, and a private one only to a private bucket")
	case pgerr.ConstraintIs(err, schema.CollectionsPublicBucketFixed):
		return publicread.Rulef("a public collection never moves to another bucket: its objects' URLs name it")
	case pgerr.ConstraintIs(err, schema.CollectionsPublicReadFixed):
		return publicread.Rulef("a collection's access and cache_control are fixed at creation")
	}
	return nil
}

// tenancyError turns the binding trigger's refusal — a bucket the tenant
// neither owns nor holds a grant on (ADR-0032) — into ErrBucketNotGranted,
// and returns nil for any other error.
func tenancyError(err error) error {
	if pgerr.Is(err, pgerr.CheckViolation) && pgerr.ConstraintIs(err, schema.CollectionsBucketTenancy) {
		return fmt.Errorf("%w: %w", admindomain.ErrBucketNotGranted, err)
	}
	return nil
}
