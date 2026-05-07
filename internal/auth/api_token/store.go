package api_token

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Store is the persistence seam. Production wires the Postgres
// implementation; tests substitute an in-memory stub. Methods return
// typed errors (ErrTokenNotFound) where applicable so the verifier
// can branch without string-matching.
type Store interface {
	// Insert persists a freshly issued token. Caller must NOT include
	// the plaintext on the row — only the prefix + hash. The Token
	// struct passed in carries Plaintext for caller convenience but
	// the implementation must drop it before storage.
	Insert(ctx context.Context, t Token, hash string) error

	// FindByPrefix loads token rows whose `prefix` matches the supplied
	// 8-char display string. Typical case: one row. Collisions handled
	// by the caller via hash compare.
	//
	// Returns the token rows AND their stored hashes — verification
	// needs the hash to argon2id-compare. Hash is intentionally NOT
	// part of Token (which is the public-facing struct) to keep
	// callers from accidentally logging it.
	FindByPrefix(ctx context.Context, prefix string) ([]Token, []string, error)

	// Get loads one token row by id. Used by admin tooling
	// (GetUsage RPC) to surface metadata + rate-limit cap without
	// scanning the table. Returns ErrTokenNotFound when no row.
	Get(ctx context.Context, id uuid.UUID) (Token, error)

	// Revoke marks a token as revoked at NOW(). Idempotent: re-revoking
	// a revoked token returns nil with no row update.
	Revoke(ctx context.Context, id uuid.UUID) error

	// TouchLastUsed bumps last_used_at for the given id. Verifier
	// invokes this on a successful verify; production batches /
	// debounces under high QPS to avoid per-request UPDATE pressure.
	TouchLastUsed(ctx context.Context, id uuid.UUID, at time.Time) error

	// ListByTenant returns active + (optionally) revoked tokens for an
	// admin UI. Cursor-paginated.
	ListByTenant(ctx context.Context, args ListByTenantArgs) ([]Token, string, error)

	// PurgeExpired drops rows whose expires_at lies past the supplied
	// grace window. Bounds the table over time. Verifier correctness
	// is unchanged — an expired token can never satisfy the time gate.
	PurgeExpired(ctx context.Context, expiredFor time.Duration) (int64, error)
}

// ListByTenantArgs is the input shape for Store.ListByTenant.
type ListByTenantArgs struct {
	TenantID       uuid.UUID
	IncludeRevoked bool
	IncludeExpired bool
	Cursor         string
	Limit          int32
}
