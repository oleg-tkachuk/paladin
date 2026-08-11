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
	// the plaintext on the row — only the prefix (display) + the HMAC
	// digest (lookup key). The Token struct passed in carries Plaintext
	// for caller convenience but the implementation must drop it before
	// storage.
	Insert(ctx context.Context, t Token, digest []byte) error

	// FindByDigest loads the single token row whose `token_hmac` equals
	// the supplied HMAC-SHA256 digest. The digest column is UNIQUE, so
	// this is an O(1) indexed exact-match lookup (no prefix scan, no
	// per-candidate compare). Returns ErrTokenNotFound when no row
	// matches — the verifier maps that to CodeUnauthenticated without
	// revealing whether a prefix happened to exist.
	FindByDigest(ctx context.Context, digest []byte) (Token, error)

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
	//
	// tenantID is the verified token's tenant. The verify path runs
	// pre-authentication (no principal, so the RLS PrepareConn hook
	// stamps an empty paladin.tenant_id), so this UPDATE must set the
	// tenant GUC itself to satisfy the tenant_isolation policy on
	// api_tokens — otherwise RLS filters the row and the touch silently
	// no-ops. Best-effort: the verifier ignores the error.
	TouchLastUsed(ctx context.Context, id, tenantID uuid.UUID, at time.Time) error

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
