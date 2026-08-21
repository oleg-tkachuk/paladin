// Package api_token is Paladin's machine-to-machine token primitive.
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
//   - Storage: digest only. The token is shown to the caller exactly once
//     at issuance; the database holds HMAC-SHA256(server_key, token) in the
//     token_hmac column. Compromise of the DB does not reveal usable tokens,
//     and without the server key an attacker cannot forge a digest for a
//     guessed token. A fast keyed hash (not a slow KDF like argon2/bcrypt) is
//     correct here: the token body is 32 bytes of CSPRNG output, so there is
//     no brute-force surface, and a deterministic digest can be UNIQUE-indexed
//     for O(1) lookup.
//
//   - Verification: HMAC the incoming token → single indexed lookup by
//     token_hmac → revocation gate → time gate → audience gate. A wrong
//     token hashes to a digest that matches no row, so the failure is
//     indistinguishable from "no such token".
//
//   - TTL: `expires_at` is NOT NULL at the schema level (the schema baseline (001_initial_schema.sql)).
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
	// G101 reads `paladin_pat_` as a hardcoded credential. It is the
	// opposite: a public, documented format marker that secret scanners
	// pin so they can FIND real tokens. Suppressed per-site rather than
	// repo-wide so a genuine literal secret still trips the linter.
	TokenPrefix = "paladin_pat_" //nolint:gosec // G101: public token prefix, not a credential
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
	ID       uuid.UUID
	TenantID uuid.UUID
	Name     string
	Prefix   string   // first PrefixLen base32 chars after `paladin_pat_`
	Scopes   []string // coarse-grained: api:read, api:write, admin:*
	// Roles the derived principal carries. Empty for an ordinary service
	// token, which is what every token was until a consumer needed to satisfy
	// a role-gated policy (minting capabilities for the tenants it serves)
	// without logging in as a human and holding a session. Granting one is
	// gated to platform.admin at the handler.
	Roles      []string
	Audience   []string // subset of {data, admin, iam, mcp}
	ExpiresAt  time.Time
	RevokedAt  *time.Time
	LastUsedAt *time.Time
	CreatedBy  string
	CreatedAt  time.Time

	// RateLimitRPM caps the requests-per-minute the bearer can submit.
	// 0 = unlimited (the Limiter short-circuits without touching its
	// counters). Enforced by the APITokenInterceptor calling into a
	// Limiter implementation; see internal/auth/api_token/ratelimit.
	RateLimitRPM int

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
