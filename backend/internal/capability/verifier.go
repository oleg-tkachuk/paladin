package capability

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

// KeyResolver resolves a JWKS `kid` to the public key the verifier
// should use. Production: fetches keys from a JWKS endpoint with a
// short TTL cache; tests: in-memory map. The interface is here so
// production wiring lands in a follow-up commit without touching
// verifier callers.
type KeyResolver interface {
	// PublicKey returns the Ed25519 public key for the given kid.
	// Returns ErrUnknownKID if the key set does not contain the kid;
	// the verifier maps that to ErrInvalidSignature so an unknown
	// kid never short-circuits to "trusted".
	PublicKey(ctx context.Context, kid string) (ed25519.PublicKey, error)
}

// ErrUnknownKID is the typed not-found return from KeyResolver. The
// verifier converts it to ErrInvalidSignature for caller-facing errors;
// keeping the typed return lets ops dashboards distinguish "we don't
// know this kid" from "signature mismatch".
var ErrUnknownKID = errors.New("capability: unknown kid")

// StaticKeyResolver is the trivial implementation used in tests and
// single-node deploys. Maps kid → public key; concurrency-safe via a
// sync.RWMutex so rotation can swap a kid without restarting callers.
type StaticKeyResolver struct {
	mu   sync.RWMutex
	keys map[string]ed25519.PublicKey
}

// NewStaticKeyResolver builds a resolver from a kid → public key map.
// The map is copied to defend against caller mutation.
func NewStaticKeyResolver(keys map[string]ed25519.PublicKey) *StaticKeyResolver {
	out := &StaticKeyResolver{keys: make(map[string]ed25519.PublicKey, len(keys))}
	for k, v := range keys {
		out.keys[k] = v
	}
	return out
}

// PublicKey implements KeyResolver.
func (r *StaticKeyResolver) PublicKey(_ context.Context, kid string) (ed25519.PublicKey, error) {
	r.mu.RLock()
	k, ok := r.keys[kid]
	r.mu.RUnlock()
	if !ok {
		return nil, ErrUnknownKID
	}
	return k, nil
}

// SetKey adds or replaces a kid → key mapping. Used during rotation.
func (r *StaticKeyResolver) SetKey(kid string, key ed25519.PublicKey) {
	r.mu.Lock()
	r.keys[kid] = key
	r.mu.Unlock()
}

// VerifierConfig wires the verifier. All fields are required.
type VerifierConfig struct {
	// Keys resolves JWKS kids to Ed25519 public keys.
	Keys KeyResolver

	// Revocations is the cached / direct revocation lookup. Production
	// wraps the Postgres store in a CachedRevocationChecker; tests can
	// pass the bare lookup.
	Revocations RevocationLookup

	// TrustedIssuers is the set of issuer values the verifier accepts.
	// Tokens whose `iss` claim is not in the set are rejected with
	// ErrInvalidSignature — refusing to verify under an unknown issuer
	// is the correct behaviour for a multi-tenant control plane that
	// deploys multiple PALADIN instances.
	TrustedIssuers []string

	// Now is the time function the verifier uses for nbf/exp. Tests
	// override it; production passes time.Now.
	Now func() time.Time

	// Leeway widens the nbf / exp window to absorb clock skew between
	// issuer and verifier. Default 30s when zero.
	Leeway time.Duration
}

// StandardVerifier is the production verifier — token decode + signature
// + revocation cache + audience / time / generation gates.
//
// Verification is independent of the persistence layer (no Get on the
// hot path) so the verifier scales horizontally without contention on
// the capability_records table. The only Postgres touch is the
// revocation cache, which short-TTLs through to a single-row index hit.
type StandardVerifier struct {
	cfg VerifierConfig
}

// NewStandardVerifier validates wiring and builds the verifier.
func NewStandardVerifier(cfg VerifierConfig) (*StandardVerifier, error) {
	if cfg.Keys == nil {
		return nil, errors.New("capability: VerifierConfig.Keys required")
	}
	if cfg.Revocations == nil {
		return nil, errors.New("capability: VerifierConfig.Revocations required")
	}
	if len(cfg.TrustedIssuers) == 0 {
		return nil, errors.New("capability: VerifierConfig.TrustedIssuers required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Leeway == 0 {
		cfg.Leeway = 30 * time.Second
	}
	return &StandardVerifier{cfg: cfg}, nil
}

// Verify implements Verifier. Returns the typed Capability on success.
// Errors return one of the package's typed sentinels so the interceptor
// layer can branch on the failure mode.
func (v *StandardVerifier) Verify(ctx context.Context, token string, audience string) (*Capability, error) {
	// 1) Decode the compact form. Failures here are malformed tokens.
	cap, err := Decode(token)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}

	// 2) Issuer must be trusted. Defends against tokens from rogue
	// PALADIN instances that share a Postgres user pool.
	if !slices.Contains(v.cfg.TrustedIssuers, cap.Issuer) {
		return nil, fmt.Errorf("%w: untrusted issuer %q", ErrInvalidSignature, cap.Issuer)
	}

	// 3) Resolve the kid (we re-decode the header to avoid leaking
	// internal claim shape; the Decode result throws away header info).
	kid, err := decodeKID(token)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}
	pub, err := v.cfg.Keys.PublicKey(ctx, kid)
	if err != nil {
		// Surface unknown-kid as invalid signature so callers don't
		// branch on the failure mode in security-sensitive paths.
		return nil, fmt.Errorf("%w: kid %q", ErrInvalidSignature, kid)
	}

	// 4) Signature verification. This is the cryptographic gate; every
	// downstream check assumes the token is intact.
	if err := VerifySignature(token, pub); err != nil {
		return nil, err
	}

	// 5) Time window. Leeway absorbs clock skew on both ends.
	now := v.cfg.Now()
	if !cap.NotBefore.IsZero() && now.Add(v.cfg.Leeway).Before(cap.NotBefore) {
		return nil, ErrNotYetValid
	}
	if cap.ExpiresAt.IsZero() || now.Add(-v.cfg.Leeway).After(cap.ExpiresAt) {
		return nil, ErrExpired
	}

	// 6) Audience match. The interceptor passes the plane it's running
	// on; tokens whose audience does not include it are rejected.
	if !slices.Contains(cap.Audience, audience) {
		return nil, fmt.Errorf("%w: token audience %v lacks %q",
			ErrAudienceMismatch, cap.Audience, audience)
	}

	// 7) Revocation check. Last so cheap rejections short-circuit before
	// hitting the cache / DB.
	revoked, err := v.cfg.Revocations.IsRevoked(ctx, cap.ID)
	if err != nil {
		return nil, fmt.Errorf("capability: revocation lookup: %w", err)
	}
	if revoked {
		return nil, ErrRevoked
	}

	return cap, nil
}

// decodeKID reads only the `kid` field from the JWT header. Avoids
// pulling the whole header struct into Verify just to read one field.
// Errors mirror the Decode path so callers can map them uniformly.
func decodeKID(token string) (string, error) {
	// Rather than re-implement segment splitting here, use Decode's
	// helpers — but Decode discards header. Inline a tiny header
	// extractor to keep the API surface clean.
	header, err := decodeHeader(token)
	if err != nil {
		return "", err
	}
	if header.Kid == "" {
		return "", errors.New("capability: header missing kid")
	}
	return header.Kid, nil
}
