package adapters

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	objectkey "github.com/oleg-tkachuk/paladin/internal/api/v1/collection"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
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
// the handler uses to write an collection mutation and its outbox rows
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
		strPtr(args.DisplayName),
		args.BackendID,
		args.BucketName,
		args.CedarPolicy,
		rules,
	); err != nil {
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
	return bucketFromSQLC(row.Collection), nil
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
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23001", "23503":
				return objectkey.ErrCollectionHasObjects
			}
		}
		return fmt.Errorf("delete collection: %w", err)
	}
	if rows == 0 {
		return objectkey.ErrVersionMismatch
	}
	return nil
}

func (r *CollectionRepo) Rebind(ctx context.Context, tenantID uuid.UUID, collection, backendID, bucketName string, expectedVersion int64) error {
	rows, err := r.q.BindCollectionToBucket(ctx, pgUUID(tenantID), collection, backendID, bucketName, expectedVersion)
	if err != nil {
		return fmt.Errorf("rebind collection: %w", err)
	}
	if rows == 0 {
		return objectkey.ErrVersionMismatch
	}
	return nil
}

func (r *CollectionRepo) List(ctx context.Context, args objectkey.ListCollectionsArgs) ([]objectkey.Collection, string, error) {
	pageSize := args.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}

	// Fast path: the sqlc-generated query handles (tenant_id, after,
	// page_size) — used when no (backend, bucket) filter is set. The
	// filter is rare enough (only the storage-first browser hits it)
	// that hand-writing a parameterised query here keeps the sqlc
	// surface lean. When the filter is present we go through the
	// raw pool with a parameterised UPDATE-safe statement.
	if args.BackendID == "" && args.BucketName == "" {
		var after *string
		if args.PageToken != "" {
			tok := args.PageToken
			after = &tok
		}
		rows, err := r.q.ListCollections(ctx, pgUUID(args.TenantID), after, pageSize)
		if err != nil {
			return nil, "", fmt.Errorf("list collections: %w", err)
		}
		out := make([]objectkey.Collection, 0, len(rows))
		for _, row := range rows {
			out = append(out, bucketFromSQLC(row.Collection))
		}
		var next string
		if int32(len(out)) == pageSize && len(out) > 0 {
			next = out[len(out)-1].Collection
		}
		return out, next, nil
	}

	// Filtered path: (backend, bucket) narrow. Tenant filter is
	// optional here — the storage-first browser passes uuid.Nil to
	// get cross-tenant results on a specific bucket. Cursor is on
	// (tenant_id, collection) so pagination stays deterministic
	// across tenants.
	const filteredQ = `
		SELECT tenant_id, collection, display_name, backend_id, bucket_name,
		       cedar_policy, lifecycle_rules, resource_version,
		       created_at, updated_at
		  FROM collections
		 WHERE backend_id  = $1
		   AND bucket_name = $2
		   AND ($3::uuid IS NULL OR tenant_id = $3)
		   AND ($4::text IS NULL OR collection > $4)
		 ORDER BY tenant_id, collection
		 LIMIT $5
	`
	var tenantFilter any
	if args.TenantID != uuid.Nil {
		tenantFilter = pgUUID(args.TenantID)
	}
	var afterTok any
	if args.PageToken != "" {
		afterTok = args.PageToken
	}
	rows, err := r.pool.Query(ctx, filteredQ,
		args.BackendID, args.BucketName, tenantFilter, afterTok, pageSize)
	if err != nil {
		return nil, "", fmt.Errorf("list collections (filtered): %w", err)
	}
	defer rows.Close()
	out := make([]objectkey.Collection, 0)
	for rows.Next() {
		var row sqlc.Collection
		if err := rows.Scan(
			&row.TenantID, &row.Collection, &row.DisplayName,
			&row.BackendID, &row.BucketName,
			&row.CedarPolicy, &row.LifecycleRules,
			&row.ResourceVersion, &row.CreatedAt, &row.UpdatedAt,
		); err != nil {
			return nil, "", fmt.Errorf("list collections: scan: %w", err)
		}
		out = append(out, bucketFromSQLC(row))
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("list collections: rows err: %w", err)
	}
	var next string
	if int32(len(out)) == pageSize && len(out) > 0 {
		next = out[len(out)-1].Collection
	}
	return out, next, nil
}

// Stats runs a single grouped aggregation. Returning (AVAILABLE, PENDING,
// DELETED) counts keeps the row count O(1) regardless of collection size.
func (r *CollectionRepo) Stats(ctx context.Context, tenantID uuid.UUID, collection string) (objectkey.CollectionStats, error) {
	const q = `
		SELECT
			COALESCE(SUM(CASE WHEN state = 'AVAILABLE' THEN 1 ELSE 0 END), 0) AS available,
			COALESCE(SUM(CASE WHEN state = 'PENDING'   THEN 1 ELSE 0 END), 0) AS pending,
			COALESCE(SUM(CASE WHEN state = 'DELETED'   THEN 1 ELSE 0 END), 0) AS deleted,
			COALESCE(SUM(CASE WHEN state = 'AVAILABLE' THEN size_bytes ELSE 0 END), 0) AS size_bytes
		FROM objects
		WHERE tenant_id = $1 AND collection = $2
	`
	var s objectkey.CollectionStats
	err := r.pool.QueryRow(ctx, q, pgUUID(tenantID), collection).Scan(
		&s.ObjectCountAvailable,
		&s.ObjectCountPending,
		&s.ObjectCountDeleted,
		&s.SizeBytesAvailable,
	)
	if err != nil {
		return objectkey.CollectionStats{}, fmt.Errorf("collection stats: %w", err)
	}
	return s, nil
}

func bucketFromSQLC(b sqlc.Collection) objectkey.Collection {
	return objectkey.Collection{
		TenantID:        uuidFrom(b.TenantID),
		Collection:      b.Collection,
		DisplayName:     derefStr(b.DisplayName),
		BackendID:       b.BackendID,
		BucketName:      b.BucketName,
		CedarPolicy:     b.CedarPolicy,
		LifecycleRules:  b.LifecycleRules,
		ResourceVersion: b.ResourceVersion,
		CreatedAt:       timeFrom(b.CreatedAt),
		UpdatedAt:       timeFrom(b.UpdatedAt),
	}
}
