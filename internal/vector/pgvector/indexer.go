// Package pgvector is PALADIN's day-1 vector.Indexer implementation backed by
// the same Postgres the rest of PALADIN already uses.
//
// Schema rationale (see migrations/014_pgvector.sql):
//
//   - One physical table `vector_records` shared across all kinds. Per-kind
//     partitioning is added later via PARTITION BY LIST (kind) when a
//     tenant grows past ~10M vectors and HNSW build time bites.
//   - `(tenant_id, object_uri, chunk_ref, model)` is the natural key and
//     drives idempotent Upsert via ON CONFLICT.
//   - `embedding vector(N)` — N is fixed per deploy because pgvector cannot
//     mix dimensions in one column. Multi-model deploys add columns or
//     partition by model; we expose `EmbeddingModel` in SearchRequest so
//     the adapter can route correctly when that lands.
//   - HNSW index with cosine distance; the adapter inverts to "higher is
//     better" before returning so callers don't deal with metric direction.
//
// SQL is hand-written rather than sqlc-generated: pgvector's parameter
// shape (vector literal `'[1,2,3]'::vector`) is awkward through sqlc and
// the surface is small enough to maintain by hand.
package pgvector

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/internal/vector"
)

// Config is the pgvector-specific wiring shape.
type Config struct {
	// Dimension of the embeddings column. Must match the column type
	// defined in migration 014. Mismatched callers get rejected before
	// hitting Postgres.
	Dimension int
	// DefaultModel name — used when SearchRequest.EmbeddingModel is empty
	// and the tenant only has one model registered. Optional.
	DefaultModel string
}

// Indexer implements vector.Indexer over a *pgxpool.Pool.
type Indexer struct {
	pool *pgxpool.Pool
	cfg  Config
}

// New builds an Indexer. The caller owns the pool's lifetime — Indexer
// does not Close it.
func New(pool *pgxpool.Pool, cfg Config) (*Indexer, error) {
	if pool == nil {
		return nil, errors.New("pgvector: pool required")
	}
	if cfg.Dimension <= 0 {
		return nil, errors.New("pgvector: Dimension must be > 0")
	}
	return &Indexer{pool: pool, cfg: cfg}, nil
}

// Name implements vector.Indexer.
func (i *Indexer) Name() string { return "pgvector" }

// Upsert implements vector.Indexer. Uses ON CONFLICT on the natural key.
// Records are written in a single transaction; partial failures roll back.
func (i *Indexer) Upsert(ctx context.Context, records []vector.Record) error {
	if len(records) == 0 {
		return nil
	}
	for idx, r := range records {
		if r.TenantID == uuid.Nil {
			return fmt.Errorf("pgvector: record[%d] missing tenant_id", idx)
		}
		if len(r.Vector) != i.cfg.Dimension {
			return fmt.Errorf("pgvector: record[%d] dimension %d != configured %d", idx, len(r.Vector), i.cfg.Dimension)
		}
	}

	const stmt = `
INSERT INTO vector_records
    (id, tenant_id, kind, object_uri, chunk_ref, model, embedding, payload, created_at, updated_at)
VALUES
    ($1, $2, $3, $4, $5, $6, $7::vector, $8, NOW(), NOW())
ON CONFLICT (tenant_id, object_uri, chunk_ref, model) DO UPDATE
SET embedding = EXCLUDED.embedding,
    payload   = EXCLUDED.payload,
    updated_at = NOW();
`

	tx, err := i.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("pgvector: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for idx, r := range records {
		id := r.ID
		if id == uuid.Nil {
			id = uuid.New()
		}
		if _, err := tx.Exec(ctx, stmt,
			id,
			r.TenantID,
			string(r.Kind),
			r.ObjectURI,
			r.ChunkRef,
			r.Model,
			vectorLiteral(r.Vector),
			r.Payload,
		); err != nil {
			return fmt.Errorf("pgvector: upsert[%d]: %w", idx, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("pgvector: commit: %w", err)
	}
	return nil
}

// Delete implements vector.Indexer.
func (i *Indexer) Delete(ctx context.Context, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	const stmt = `DELETE FROM vector_records WHERE id = ANY($1::uuid[])`
	if _, err := i.pool.Exec(ctx, stmt, ids); err != nil {
		return fmt.Errorf("pgvector: delete: %w", err)
	}
	return nil
}

// DeleteByObject implements vector.Indexer.
func (i *Indexer) DeleteByObject(ctx context.Context, tenantID uuid.UUID, objectURI string) error {
	if tenantID == uuid.Nil {
		return vector.ErrTenantRequired
	}
	const stmt = `DELETE FROM vector_records WHERE tenant_id = $1 AND object_uri = $2`
	if _, err := i.pool.Exec(ctx, stmt, tenantID, objectURI); err != nil {
		return fmt.Errorf("pgvector: delete by object: %w", err)
	}
	return nil
}

// Search implements vector.Indexer using cosine distance ordered ascending,
// then inverted so higher score = closer match.
func (i *Indexer) Search(ctx context.Context, req vector.SearchRequest) ([]vector.Match, error) {
	if req.TenantID == uuid.Nil {
		return nil, vector.ErrTenantRequired
	}
	if len(req.Query) != i.cfg.Dimension {
		return nil, fmt.Errorf("pgvector: query dimension %d != configured %d", len(req.Query), i.cfg.Dimension)
	}
	if req.TopK <= 0 {
		req.TopK = 10
	}

	model := req.EmbeddingModel
	if model == "" {
		model = i.cfg.DefaultModel
	}
	if model == "" {
		return nil, errors.New("pgvector: EmbeddingModel required (no default configured)")
	}

	// Build WHERE clause incrementally; bind params follow.
	args := []any{req.TenantID, vectorLiteral(req.Query), model}
	where := []string{"tenant_id = $1", "model = $3"}
	if req.Kind != "" {
		args = append(args, string(req.Kind))
		where = append(where, fmt.Sprintf("kind = $%d", len(args)))
	}
	for k, v := range req.MustMatch {
		// payload is JSONB; use the @> containment operator with a tiny
		// JSON literal. SQL injection-safe because k goes through
		// jsonb_build_object as a parameter, not literal interpolation.
		args = append(args, k)
		args = append(args, v)
		where = append(where, fmt.Sprintf("payload @> jsonb_build_object($%d::text, $%d::text)", len(args)-1, len(args)))
	}

	args = append(args, req.TopK)
	stmt := fmt.Sprintf(`
SELECT id, tenant_id, kind, object_uri, chunk_ref, model, payload,
       1.0 - (embedding <=> $2::vector) AS score
FROM vector_records
WHERE %s
ORDER BY embedding <=> $2::vector ASC
LIMIT $%d
`, strings.Join(where, " AND "), len(args))

	rows, err := i.pool.Query(ctx, stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("pgvector: search: %w", err)
	}
	defer rows.Close()

	out := make([]vector.Match, 0, req.TopK)
	for rows.Next() {
		var m vector.Match
		var kind string
		if err := rows.Scan(
			&m.Record.ID,
			&m.Record.TenantID,
			&kind,
			&m.Record.ObjectURI,
			&m.Record.ChunkRef,
			&m.Record.Model,
			&m.Record.Payload,
			&m.Score,
		); err != nil {
			return nil, fmt.Errorf("pgvector: scan: %w", err)
		}
		m.Record.Kind = vector.Kind(kind)
		if m.Score < req.MinScore {
			continue
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Stats implements vector.Indexer.
func (i *Indexer) Stats(ctx context.Context, tenantID uuid.UUID) (vector.Stats, error) {
	if tenantID == uuid.Nil {
		return vector.Stats{}, vector.ErrTenantRequired
	}
	const stmt = `
SELECT kind, model, COUNT(*) AS n
FROM vector_records
WHERE tenant_id = $1
GROUP BY kind, model
`
	rows, err := i.pool.Query(ctx, stmt, tenantID)
	if err != nil {
		return vector.Stats{}, fmt.Errorf("pgvector: stats: %w", err)
	}
	defer rows.Close()

	out := vector.Stats{
		ByKind:  map[vector.Kind]int64{},
		ByModel: map[string]int64{},
	}
	for rows.Next() {
		var kind, model string
		var n int64
		if err := rows.Scan(&kind, &model, &n); err != nil {
			return vector.Stats{}, fmt.Errorf("pgvector: stats scan: %w", err)
		}
		out.VectorCount += n
		out.ByKind[vector.Kind(kind)] += n
		out.ByModel[model] += n
	}
	return out, rows.Err()
}

// vectorLiteral encodes a []float32 as the pgvector text input format
// `[1,2,3]`. Reflection-based encoding via pgx is possible with the
// pgvector-go binding, but keeping this dependency-free saves a module
// and makes the SQL trivially debuggable.
func vectorLiteral(v []float32) string {
	if len(v) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		// %g gives the shortest representation that round-trips for
		// IEEE-754; pgvector accepts both fixed and scientific notation.
		fmt.Fprintf(&b, "%g", f)
	}
	b.WriteByte(']')
	return b.String()
}
