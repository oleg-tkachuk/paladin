// Package api_token is PALADIN's machine-to-machine token primitive.
//
// Distinct from capability tokens (capability JWTs — short-lived, signed,
// delegable, agent-runtime) and from JWT user-authn (federated OIDC).
// API tokens fill the middle ground: long-lived service-to-service
// secrets that follow the hashed-bearer pattern Hatchet / GitHub PATs /
// Stripe / GitLab use.
//
// Design points captured here (rationale for the choices below):
//
//   - Token format: `paladin_pat_<base32-no-padding(32 random bytes)>`. The
//     literal prefix is recognised by secret scanners (gitleaks / GitHub
//     leak detection) and lets `grep` find tokens leaked in logs without
//     false positives.
//
//   - Storage: hash only. The token is shown to the caller exactly once
//     at issuance; the database holds argon2id(token) in PHC string
//     format. Compromise of the DB does not reveal usable tokens.
//
//   - Verification: hash incoming → look up by prefix → argon2id verify
//     → time gate → revocation check. The prefix index keeps the lookup
//     to one row in the typical case; collisions are statistically rare
//     and resolved by the hash compare.
//
//   - TTL: `expires_at` is NOT NULL at the schema level (migration 017).
//     Application-level cap: ≤1 year for service tokens.
//
//   - Scopes: coarse-grained text[] mirror of auth.Scope — fine-grained
//     authorisation still flows through Cedar.
//
//   - Audience: subset of {data, admin, iam, mcp}. Interceptor on each
//     plane rejects tokens whose audience doesn't include that plane.
//
// Verification path is documented in verifier.go; issuance in issuer.go;
// the Postgres backing in postgres/store.go; the Connect interceptor in
// the parent auth package as APITokenInterceptor.
package api_token

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Token format. The literal `paladin_pat_` prefix MUST stay stable — secret
// scanners pin it. PrefixLen is how many base32 chars after the literal
// prefix end up in the display column (8 chars = 5 bytes of entropy →
// 1 in 2^40 collisions on the index lookup).
const (
	TokenPrefix = "paladin_pat_"
	PrefixLen   = 8
	// SecretBytes is the random-byte length encoded after the literal
	// prefix. 32 bytes = 256 bits of entropy = sufficient even with
	// argon2id's pessimistic adversary assumptions.
	SecretBytes = 32
)

// Errors returned by Verifier.Verify; the interceptor maps them to
// connect.Error codes. Exposed here so tests / admin tooling can match.
var (
	ErrTokenMalformed   = errors.New("api_token: malformed token")
	ErrTokenNotFound    = errors.New("api_token: token not found")
	ErrTokenExpired     = errors.New("api_token: token expired")
	ErrTokenRevoked     = errors.New("api_token: token revoked")
	ErrAudienceMismatch = errors.New("api_token: audience mismatch")
)

// Token is the in-memory shape of an issued or verified API token.
// Stored representation lives in the api_tokens table; this struct
// mirrors that schema 1:1, plus a Plaintext field that's populated
// only on Issue (the only moment the caller ever sees the secret).
type Token struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	Name       string
	Prefix     string   // first PrefixLen base32 chars after `paladin_pat_`
	Scopes     []string // coarse-grained: api:read, api:write, admin:*
	Audience   []string // subset of {data, admin, iam, mcp}
	ExpiresAt  time.Time
	RevokedAt  *time.Time
	LastUsedAt *time.Time
	CreatedBy  string
	CreatedAt  time.Time

	// Plaintext is the full `paladin_pat_…` string. Populated by Issuer.Issue
	// before the row is stored; cleared as soon as the caller has it.
	// Verifier.Verify does NOT populate this field — by definition the
	// verifier never reconstructs the plaintext.
	Plaintext string
}

// IsActive reports whether the token would pass time / revocation gates
// at `now`. Convenience for admin tooling that lists tokens with their
// status; not a substitute for Verifier.Verify.
func (t Token) IsActive(now time.Time) bool {
	if t.RevokedAt != nil {
		return false
	}
	return now.Before(t.ExpiresAt)
}
