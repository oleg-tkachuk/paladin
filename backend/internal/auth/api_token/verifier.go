package api_token

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

// VerifierConfig wires the verifier.
type VerifierConfig struct {
	Store  Store
	Hasher *Hasher
	Now    func() time.Time
	// Leeway widens the expires_at gate to absorb clock skew between
	// issuer and verifier. Default 30s.
	Leeway time.Duration
	// TouchLastUsed controls whether successful verifies bump
	// last_used_at on the row. Production: true. Tests / ultra-high-
	// QPS deploys that can't tolerate the write: false. Writes are
	// fire-and-forget — failures are logged but never fail the verify.
	TouchLastUsed bool
}

// Verifier is the production verifier. Verification path: validate the
// token shape → compute HMAC-SHA256(key, plaintext) → single indexed
// lookup by that digest → revocation gate → time gate → audience gate.
// The digest lookup is O(1) on a UNIQUE index (no prefix scan, no
// per-candidate hashing); a wrong plaintext yields a digest that matches
// no row, indistinguishable from "no such token".
type Verifier struct {
	cfg VerifierConfig
}

// NewVerifier validates wiring and returns a ready Verifier.
func NewVerifier(cfg VerifierConfig) (*Verifier, error) {
	if cfg.Store == nil {
		return nil, errors.New("api_token: VerifierConfig.Store required")
	}
	if cfg.Hasher == nil {
		return nil, errors.New("api_token: VerifierConfig.Hasher required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Leeway == 0 {
		cfg.Leeway = 30 * time.Second
	}
	return &Verifier{cfg: cfg}, nil
}

// Verify takes a `paladin_pat_…` plaintext + the audience the call is
// hitting and returns the verified Token. Errors are typed so the
// interceptor can map them to connect.Error codes:
//
//   - ErrTokenMalformed   — token doesn't start with `paladin_pat_` or too short.
//   - ErrTokenNotFound    — prefix lookup matched no rows OR every row
//     failed argon2id-compare. Same error to avoid
//     leaking "valid prefix, wrong tail".
//   - ErrTokenExpired     — past expires_at (with leeway).
//   - ErrTokenRevoked     — row.revoked_at is set.
//   - ErrAudienceMismatch — token.Audience does not include the calling
//     plane.
func (v *Verifier) Verify(ctx context.Context, plaintext, audience string) (*Token, error) {
	// Validate the token shape first — rejects non-`paladin_pat_` / too-short
	// input cheaply before we touch the store.
	if _, err := SplitToken(plaintext); err != nil {
		return nil, err
	}

	// Single indexed lookup by HMAC digest. A wrong plaintext hashes to a
	// digest that matches no row → ErrTokenNotFound, indistinguishable from
	// "no such token" (no "valid prefix, wrong tail" leak).
	tok, err := v.cfg.Store.FindByDigest(ctx, v.cfg.Hasher.Digest(plaintext))
	if err != nil {
		if errors.Is(err, ErrTokenNotFound) {
			return nil, ErrTokenNotFound
		}
		return nil, fmt.Errorf("api_token: lookup: %w", err)
	}

	now := v.cfg.Now()
	if tok.RevokedAt != nil {
		return nil, ErrTokenRevoked
	}
	if now.Add(-v.cfg.Leeway).After(tok.ExpiresAt) {
		return nil, ErrTokenExpired
	}
	if !slices.Contains(tok.Audience, audience) {
		return nil, fmt.Errorf("%w: %v lacks %q", ErrAudienceMismatch, tok.Audience, audience)
	}

	// Best-effort last_used_at bump. Runs pre-authentication, so it passes
	// the verified tenant to satisfy the api_tokens RLS policy; failure is
	// ignored (never fails the verify).
	if v.cfg.TouchLastUsed {
		_ = v.cfg.Store.TouchLastUsed(ctx, tok.ID, tok.TenantID, now)
	}

	return &tok, nil
}
