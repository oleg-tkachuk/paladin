// Package postgres is the api_token.Store implementation over api_tokens,
// through the sqlc queries in internal/store/postgres/queries/api_tokens.sql.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token"
	"github.com/oleg-tkachuk/paladin/backend/internal/safecast"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// Store implements api_token.Store against api_tokens (the schema baseline (001_initial_schema.sql)).
type Store struct {
	pool *pgxpool.Pool
	q    *sqlc.Queries
}

// New constructs a Store. Caller owns the pool's lifecycle.
func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("api_token/postgres: pool required")
	}
	return &Store{pool: pool, q: sqlc.New(pool)}, nil
}

// Insert implements api_token.Store. The Token's Plaintext field is
// intentionally NOT persisted — only prefix (display) + token_hmac (the
// HMAC-SHA256 lookup digest). The legacy argon2 token_hash column was dropped
// in the schema baseline (001_initial_schema.sql).
//
// The INSERT runs in a transaction scoped to the TOKEN's tenant, not the
// caller's.
//
// The pool's PrepareConn hook stamps the GUC from the request principal, and
// api_tokens' tenant_isolation policy checks the new row against it. That
// combination made a platform admin unable to mint a token for any tenant but
// their own — which is the entire purpose of an admin RPC that takes
// `tenant_id` as a parameter. The RPC accepted the argument, then RLS refused
// the write with a bare 42501, so provisioning a new tenant's service
// credential was impossible through the API.
//
// Authorization for a cross-tenant mint belongs to the handler (Cedar, plus an
// explicit platform.admin check for a tenant other than the caller's) and is
// enforced there before this is ever reached. RLS is not the gate here; it
// cannot be, because the caller is legitimately acting on somebody else's
// tenant.
func (s *Store) Insert(ctx context.Context, t api_token.Token, digest []byte) error {
	return s.inTenant(ctx, t.TenantID, "insert", func(q *sqlc.Queries) error {
		return q.InsertAPIToken(ctx,
			pgUUID(t.ID), pgUUID(t.TenantID), t.Name, t.Prefix, digest,
			t.Scopes, t.Roles, t.Audience, pgTime(t.ExpiresAt), rpmToDB(t.RateLimitRPM),
			t.CreatedBy, pgTime(t.CreatedAt))
	})
}

// FindByDigest implements api_token.Store. O(1) exact-match lookup on the
// UNIQUE token_hmac index. Reached pre-authentication (the token is what
// establishes the tenant), so it relies on the api_tokens permissive
// SELECT policy — the tenant_isolation policy alone would filter it out
// because no paladin.tenant_id GUC is set yet.
func (s *Store) FindByDigest(ctx context.Context, digest []byte) (api_token.Token, error) {
	row, err := s.q.APITokenByDigest(ctx, digest)
	if err != nil {
		return api_token.Token{}, notFoundOr(err, "query by digest")
	}
	return tokenFrom(row), nil
}

// Get implements api_token.Store. Returns ErrTokenNotFound when no
// row matches.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (api_token.Token, error) {
	row, err := s.q.GetAPIToken(ctx, pgUUID(id))
	if err != nil {
		return api_token.Token{}, notFoundOr(err, "get")
	}
	return tokenFrom(sqlc.APITokenByDigestRow(row)), nil
}

// Revoke implements api_token.Store. Idempotent — a re-revoke leaves
// revoked_at unchanged (we only update when it's NULL).
func (s *Store) Revoke(ctx context.Context, id uuid.UUID) error {
	if err := s.q.RevokeAPIToken(ctx, pgUUID(id)); err != nil {
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
// rows, so the transaction is scoped to the verified token's tenant first —
// same pattern the capability store uses. The scope is LOCAL to the
// transaction and reverts on commit or rollback.
func (s *Store) TouchLastUsed(ctx context.Context, id, tenantID uuid.UUID, at time.Time) error {
	return s.inTenant(ctx, tenantID, "touch", func(q *sqlc.Queries) error {
		return q.TouchAPITokenLastUsed(ctx, pgTime(at), pgUUID(id))
	})
}

// ListByTenant implements api_token.Store with cursor-based pagination
// (cursor = last seen id), api_token.ClampListLimit tokens a page.
func (s *Store) ListByTenant(ctx context.Context, args api_token.ListByTenantArgs) ([]api_token.Token, string, error) {
	if args.TenantID == uuid.Nil() {
		return nil, "", errors.New("api_token/postgres: tenant_id required")
	}
	limit := api_token.ClampListLimit(args.Limit)
	cursor, err := args.CursorID()
	if err != nil {
		return nil, "", err
	}
	after := pgtype.UUID{}
	if cursor != nil {
		after = pgUUID(*cursor)
	}
	// One past the page, to learn whether another follows.
	rows, err := s.q.ListAPITokensByTenant(ctx, pgUUID(args.TenantID),
		args.IncludeRevoked, args.IncludeExpired, after, limit+1)
	if err != nil {
		return nil, "", fmt.Errorf("api_token/postgres: list: %w", err)
	}
	out := make([]api_token.Token, 0, len(rows))
	for _, r := range rows {
		out = append(out, tokenFrom(sqlc.APITokenByDigestRow(r)))
	}
	nextCursor := ""
	if len(out) > int(limit) {
		out = out[:limit]
		// Seek past the last row RETURNED, not past the overflow row
		// fetched to detect the next page — the overflow row belongs to
		// the next page and seeking past it drops it entirely.
		nextCursor = out[len(out)-1].ID.String()
	}
	return out, nextCursor, nil
}

// PurgeExpired implements api_token.Store.
func (s *Store) PurgeExpired(ctx context.Context, expiredFor time.Duration) (int64, error) {
	n, err := s.q.PurgeExpiredAPITokens(ctx, expiredFor.Microseconds())
	if err != nil {
		return 0, fmt.Errorf("api_token/postgres: purge: %w", err)
	}
	return n, nil
}

// inTenant runs fn on a transaction scoped to tenantID, for the writes the
// api_tokens tenant_isolation policy must see under the token's own tenant.
func (s *Store) inTenant(ctx context.Context, tenantID uuid.UUID, op string, fn func(*sqlc.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("api_token/postgres: %s begin: %w", op, err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after commit
	q := s.q.WithTx(tx)
	if err := q.SetSessionTenantForAPIToken(ctx, tenantID.String()); err != nil {
		return fmt.Errorf("api_token/postgres: %s set tenant: %w", op, err)
	}
	if err := fn(q); err != nil {
		return fmt.Errorf("api_token/postgres: %s: %w", op, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("api_token/postgres: %s commit: %w", op, err)
	}
	return nil
}

// notFoundOr maps a read with no row to api_token.ErrTokenNotFound.
func notFoundOr(err error, op string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return api_token.ErrTokenNotFound
	}
	return fmt.Errorf("api_token/postgres: %s: %w", op, err)
}

// tokenFrom is an api_tokens row as the domain carries it. Every read
// selects the same columns, so each row type converts to this one.
func tokenFrom(r sqlc.APITokenByDigestRow) api_token.Token {
	return api_token.Token{
		ID:           uuidFrom(r.ID),
		TenantID:     uuidFrom(r.TenantID),
		Name:         r.Name,
		Prefix:       r.Prefix,
		Scopes:       r.Scopes,
		Roles:        r.Roles,
		Audience:     r.Audience,
		ExpiresAt:    r.ExpiresAt.Time,
		RateLimitRPM: rpmFromDB(r.RateLimitRpm),
		RevokedAt:    optionalTime(r.RevokedAt),
		LastUsedAt:   optionalTime(r.LastUsedAt),
		CreatedBy:    r.CreatedBy,
		CreatedAt:    r.CreatedAt.Time,
	}
}

func pgUUID(u uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: u, Valid: true} }

func uuidFrom(p pgtype.UUID) uuid.UUID { return uuid.UUID(p.Bytes) }

func pgTime(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

// optionalTime is a nullable timestamp, nil when NULL.
func optionalTime(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}

// rpmToDB and rpmFromDB carry the per-token rate limit: nil is no cap.
func rpmToDB(rpm *int) *int32 {
	if rpm == nil {
		return nil
	}
	v := safecast.Int32(*rpm)
	return &v
}

func rpmFromDB(rpm *int32) *int {
	if rpm == nil {
		return nil
	}
	v := int(*rpm)
	return &v
}
