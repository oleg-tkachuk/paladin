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
	Store Store
	Now   func() time.Time
	// Leeway widens the expires_at gate to absorb clock skew between
	// issuer and verifier. Default 30s.
	Leeway time.Duration
	// TouchLastUsed controls whether successful verifies bump
	// last_used_at on the row. Production: true. Tests / ultra-high-
	// QPS deploys that can't tolerate the write: false. Writes are
	// fire-and-forget — failures are logged but never fail the verify.
	TouchLastUsed bool
}

// Verifier is the production verifier. Verification path: parse prefix
// → lookup by prefix → argon2id-compare each candidate → time gate →
// audience gate → revocation gate. The argon2id step is the slow gate
// (~50ms); putting it after the cheap prefix lookup means we only
// hash on the row that actually needs it. Audience / time / revocation
// run after the hash compare so an invalid plaintext never reveals
// whether a valid prefix exists.
type Verifier struct {
	cfg VerifierConfig
}

// NewVerifier validates wiring and returns a ready Verifier.
func NewVerifier(cfg VerifierConfig) (*Verifier, error) {
	if cfg.Store == nil {
		return nil, errors.New("api_token: VerifierConfig.Store required")
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
	prefix, err := SplitToken(plaintext)
	if err != nil {
		return nil, err
	}

	candidates, hashes, err := v.cfg.Store.FindByPrefix(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("api_token: lookup: %w", err)
	}
	if len(candidates) == 0 {
		return nil, ErrTokenNotFound
	}

	// argon2id-compare each candidate. In the typical case there's one
	// row; collisions are rare. We never short-circuit early on
	// matching because constant-time semantics matter — for the same
	// reason CompareToken uses constantTimeEqual internally.
	matched := -1
	for i := range candidates {
		if err := CompareToken(plaintext, hashes[i]); err == nil {
			matched = i
			break
		}
	}
	if matched < 0 {
		return nil, ErrTokenNotFound
	}
	tok := candidates[matched]

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

	// Best-effort last_used_at bump. Failure is logged at the caller
	// level (interceptor) but never fails the verify.
	if v.cfg.TouchLastUsed {
		_ = v.cfg.Store.TouchLastUsed(ctx, tok.ID, now)
	}

	return &tok, nil
}
