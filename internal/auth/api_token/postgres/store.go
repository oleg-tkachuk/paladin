// Package postgres is the api_token.Store implementation backed by
// migration 017. SQL is hand-written rather than sqlc-generated because
// the surface is small (six methods) and the array-typed columns
// (`scopes`, `audience`) flow more cleanly through pgx than through
// sqlc's typed-mapping path.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/internal/auth/api_token"
)

// Store implements api_token.Store against api_tokens (migration 017).
type Store struct {
	pool *pgxpool.Pool
}

// New constructs a Store. Caller owns the pool's lifecycle.
func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("api_token/postgres: pool required")
	}
	return &Store{pool: pool}, nil
}

// Insert implements api_token.Store. The Token's Plaintext field is
// intentionally NOT persisted — only prefix + hash.
func (s *Store) Insert(ctx context.Context, t api_token.Token, hash string) error {
	const stmt = `
INSERT INTO api_tokens (
    id, tenant_id, name, prefix, token_hash,
    scopes, audience, expires_at, created_by, created_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10
);
`
	if _, err := s.pool.Exec(ctx, stmt,
		t.ID,
		t.TenantID,
		t.Name,
		t.Prefix,
		hash,
		t.Scopes,
		t.Audience,
		t.ExpiresAt,
		t.CreatedBy,
		t.CreatedAt,
	); err != nil {
		return fmt.Errorf("api_token/postgres: insert: %w", err)
	}
	return nil
}

// FindByPrefix implements api_token.Store.
func (s *Store) FindByPrefix(ctx context.Context, prefix string) ([]api_token.Token, []string, error) {
	const stmt = `
SELECT id, tenant_id, name, prefix, token_hash,
       scopes, audience, expires_at, revoked_at, last_used_at,
       created_by, created_at
FROM   api_tokens
WHERE  prefix = $1;
`
	rows, err := s.pool.Query(ctx, stmt, prefix)
	if err != nil {
		return nil, nil, fmt.Errorf("api_token/postgres: query by prefix: %w", err)
	}
	defer rows.Close()

	var tokens []api_token.Token
	var hashes []string
	for rows.Next() {
		var t api_token.Token
		var hash string
		var revokedAt, lastUsedAt *time.Time
		err := rows.Scan(
			&t.ID, &t.TenantID, &t.Name, &t.Prefix, &hash,
			&t.Scopes, &t.Audience, &t.ExpiresAt, &revokedAt, &lastUsedAt,
			&t.CreatedBy, &t.CreatedAt,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("api_token/postgres: scan: %w", err)
		}
		t.RevokedAt = revokedAt
		t.LastUsedAt = lastUsedAt
		tokens = append(tokens, t)
		hashes = append(hashes, hash)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("api_token/postgres: iterate: %w", err)
	}
	return tokens, hashes, nil
}

// Revoke implements api_token.Store. Idempotent — a re-revoke leaves
// revoked_at unchanged (we only update when it's NULL).
func (s *Store) Revoke(ctx context.Context, id uuid.UUID) error {
	const stmt = `
UPDATE api_tokens
SET    revoked_at = NOW()
WHERE  id = $1
   AND revoked_at IS NULL;
`
	if _, err := s.pool.Exec(ctx, stmt, id); err != nil {
		return fmt.Errorf("api_token/postgres: revoke: %w", err)
	}
	return nil
}

// TouchLastUsed implements api_token.Store. Best-effort write —
// callers (verifier) ignore errors so a failed touch doesn't fail
// the verify.
func (s *Store) TouchLastUsed(ctx context.Context, id uuid.UUID, at time.Time) error {
	const stmt = `UPDATE api_tokens SET last_used_at = $2 WHERE id = $1`
	if _, err := s.pool.Exec(ctx, stmt, id, at); err != nil {
		return fmt.Errorf("api_token/postgres: touch: %w", err)
	}
	return nil
}

// ListByTenant implements api_token.Store with cursor-based pagination
// (cursor = last seen id). Limit defaults to 50, capped at 500.
func (s *Store) ListByTenant(ctx context.Context, args api_token.ListByTenantArgs) ([]api_token.Token, string, error) {
	if args.TenantID == uuid.Nil {
		return nil, "", errors.New("api_token/postgres: tenant_id required")
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	whereExtra := ""
	bindArgs := []any{args.TenantID}
	if !args.IncludeRevoked {
		whereExtra += " AND revoked_at IS NULL"
	}
	if !args.IncludeExpired {
		whereExtra += " AND expires_at > NOW()"
	}
	if args.Cursor != "" {
		bindArgs = append(bindArgs, args.Cursor)
		whereExtra += fmt.Sprintf(" AND id > $%d::uuid", len(bindArgs))
	}
	bindArgs = append(bindArgs, limit+1) // +1 to detect next page
	stmt := fmt.Sprintf(`
SELECT id, tenant_id, name, prefix, token_hash,
       scopes, audience, expires_at, revoked_at, last_used_at,
       created_by, created_at
FROM   api_tokens
WHERE  tenant_id = $1
  %s
ORDER  BY id
LIMIT  $%d;
`, whereExtra, len(bindArgs))

	rows, err := s.pool.Query(ctx, stmt, bindArgs...)
	if err != nil {
		return nil, "", fmt.Errorf("api_token/postgres: list: %w", err)
	}
	defer rows.Close()

	out := make([]api_token.Token, 0, limit)
	for rows.Next() {
		var t api_token.Token
		var hash string
		var revokedAt, lastUsedAt *time.Time
		err := rows.Scan(
			&t.ID, &t.TenantID, &t.Name, &t.Prefix, &hash,
			&t.Scopes, &t.Audience, &t.ExpiresAt, &revokedAt, &lastUsedAt,
			&t.CreatedBy, &t.CreatedAt,
		)
		if err != nil {
			return nil, "", fmt.Errorf("api_token/postgres: scan: %w", err)
		}
		_ = hash // not surfaced to admin tooling
		t.RevokedAt = revokedAt
		t.LastUsedAt = lastUsedAt
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("api_token/postgres: list iterate: %w", err)
	}

	nextCursor := ""
	if int32(len(out)) > limit {
		nextCursor = out[limit].ID.String()
		out = out[:limit]
	}
	return out, nextCursor, nil
}

// PurgeExpired implements api_token.Store.
func (s *Store) PurgeExpired(ctx context.Context, expiredFor time.Duration) (int64, error) {
	const stmt = `
DELETE FROM api_tokens
WHERE expires_at < NOW() - ($1::bigint || ' microseconds')::interval;
`
	tag, err := s.pool.Exec(ctx, stmt, expiredFor.Microseconds())
	if err != nil {
		return 0, fmt.Errorf("api_token/postgres: purge: %w", err)
	}
	return tag.RowsAffected(), nil
}

// silence pgx import when unused build modes strip the package.
var _ = pgx.ErrNoRows
