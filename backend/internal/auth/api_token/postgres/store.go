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
// intentionally NOT persisted — only prefix (display) + token_hmac (the
// HMAC-SHA256 lookup digest). The legacy argon2 token_hash column was dropped
// in migration 062.
//
// Runs under the request principal's tenant (the RLS PrepareConn hook stamps
// paladin.tenant_id from the admin caller), so the tenant_isolation WITH CHECK on
// api_tokens is satisfied automatically.
func (s *Store) Insert(ctx context.Context, t api_token.Token, digest []byte) error {
	const stmt = `
INSERT INTO api_tokens (
    id, tenant_id, name, prefix, token_hmac,
    scopes, audience, expires_at, rate_limit_rpm,
    created_by, created_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9,
    $10, $11
);
`
	if _, err := s.pool.Exec(ctx, stmt,
		t.ID,
		t.TenantID,
		t.Name,
		t.Prefix,
		digest,
		t.Scopes,
		t.Audience,
		t.ExpiresAt,
		t.RateLimitRPM,
		t.CreatedBy,
		t.CreatedAt,
	); err != nil {
		return fmt.Errorf("api_token/postgres: insert: %w", err)
	}
	return nil
}

// FindByDigest implements api_token.Store. O(1) exact-match lookup on the
// UNIQUE token_hmac index. Reached pre-authentication (the token is what
// establishes the tenant), so it relies on the api_tokens permissive
// SELECT policy — the tenant_isolation policy alone would filter it out
// because no paladin.tenant_id GUC is set yet.
func (s *Store) FindByDigest(ctx context.Context, digest []byte) (api_token.Token, error) {
	const stmt = `
SELECT id, tenant_id, name, prefix,
       scopes, audience, expires_at, rate_limit_rpm,
       revoked_at, last_used_at, created_by, created_at
FROM   api_tokens
WHERE  token_hmac = $1;
`
	var t api_token.Token
	var revokedAt, lastUsedAt *time.Time
	err := s.pool.QueryRow(ctx, stmt, digest).Scan(
		&t.ID, &t.TenantID, &t.Name, &t.Prefix,
		&t.Scopes, &t.Audience, &t.ExpiresAt, &t.RateLimitRPM,
		&revokedAt, &lastUsedAt, &t.CreatedBy, &t.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api_token.Token{}, api_token.ErrTokenNotFound
		}
		return api_token.Token{}, fmt.Errorf("api_token/postgres: query by digest: %w", err)
	}
	t.RevokedAt = revokedAt
	t.LastUsedAt = lastUsedAt
	return t, nil
}

// Get implements api_token.Store. Returns ErrTokenNotFound when no
// row matches.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (api_token.Token, error) {
	const stmt = `
SELECT id, tenant_id, name, prefix,
       scopes, audience, expires_at, rate_limit_rpm,
       revoked_at, last_used_at, created_by, created_at
FROM   api_tokens
WHERE  id = $1;
`
	var t api_token.Token
	var revokedAt, lastUsedAt *time.Time
	err := s.pool.QueryRow(ctx, stmt, id).Scan(
		&t.ID, &t.TenantID, &t.Name, &t.Prefix,
		&t.Scopes, &t.Audience, &t.ExpiresAt, &t.RateLimitRPM,
		&revokedAt, &lastUsedAt, &t.CreatedBy, &t.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api_token.Token{}, api_token.ErrTokenNotFound
		}
		return api_token.Token{}, fmt.Errorf("api_token/postgres: get: %w", err)
	}
	t.RevokedAt = revokedAt
	t.LastUsedAt = lastUsedAt
	return t, nil
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

// TouchLastUsed implements api_token.Store. Best-effort write — callers
// (verifier) ignore errors so a failed touch doesn't fail the verify.
//
// Runs pre-authentication (the verify path has no principal, so the pool's
// PrepareConn hook stamped an empty paladin.tenant_id). The api_tokens
// tenant_isolation RLS policy would therefore filter this UPDATE to zero
// rows, so we open a transaction and SET LOCAL the verified token's tenant
// first — same pattern the capability store uses. The GUC is LOCAL to the
// tx and reverts on commit/rollback.
func (s *Store) TouchLastUsed(ctx context.Context, id, tenantID uuid.UUID, at time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("api_token/postgres: touch begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after commit

	if _, err := tx.Exec(ctx, `SELECT set_config('paladin.tenant_id', $1, true)`, tenantID.String()); err != nil {
		return fmt.Errorf("api_token/postgres: touch set tenant: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE api_tokens SET last_used_at = $2 WHERE id = $1`, id, at); err != nil {
		return fmt.Errorf("api_token/postgres: touch: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("api_token/postgres: touch commit: %w", err)
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
SELECT id, tenant_id, name, prefix,
       scopes, audience, expires_at, rate_limit_rpm,
       revoked_at, last_used_at, created_by, created_at
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
			&t.Scopes, &t.Audience, &t.ExpiresAt, &t.RateLimitRPM,
			&revokedAt, &lastUsedAt, &t.CreatedBy, &t.CreatedAt,
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
