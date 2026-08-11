package api_token

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"fmt"
	"strings"
)

// base32NoPad is RFC 4648 base32 without padding. Padding is stripped so
// token strings are URL-safe-ish and easy to put in env vars; the literal
// `paladin_pat_` prefix is unmistakable to secret scanners.
var base32NoPad = base32.StdEncoding.WithPadding(base32.NoPadding)

// Hasher computes the deterministic lookup digest for an API token:
// HMAC-SHA256(server_key, plaintext). Rationale for a keyed FAST hash
// instead of a slow KDF (argon2/bcrypt):
//
//   - The token body is 32 bytes (256 bits) of CSPRNG output. There is no
//     brute-force surface — 2^256 is unsearchable — so the slow-KDF cost
//     that protects LOW-entropy passwords buys nothing here. GitHub, Stripe
//     and AWS hash high-entropy API tokens with a fast (HMAC-)SHA-256 for
//     exactly this reason.
//
//   - Deterministic (keyed, no per-row salt) means the digest can carry a
//     UNIQUE index and be looked up in O(1) by exact match, instead of the
//     old prefix-scan + per-candidate argon2 compare. Verification drops
//     from ~50ms + 64MiB per request to microseconds with no per-request
//     memory pressure — removing a real DoS/latency surface.
//
//   - The server key (pepper) means a stolen DB dump is not enough to forge
//     a token: without the key an attacker cannot compute the digest for a
//     guessed plaintext. Combined with 256-bit entropy this is strictly
//     stronger than the old unsalted-per-row argon2 (which had no pepper).
//
// The key is held once at process start (config.APIToken.HMACKey, resolved
// from a Secret) and shared by the Issuer and Verifier.
type Hasher struct{ key []byte }

// MinHMACKeyLen is the minimum accepted server-key length (256 bits).
const MinHMACKeyLen = 32

// NewHasher validates the key length and returns a Hasher. The key is
// copied so later mutation of the caller's slice can't change hashing.
func NewHasher(key []byte) (*Hasher, error) {
	if len(key) < MinHMACKeyLen {
		return nil, fmt.Errorf("api_token: HMAC key must be >= %d bytes, got %d", MinHMACKeyLen, len(key))
	}
	return &Hasher{key: append([]byte(nil), key...)}, nil
}

// Digest returns HMAC-SHA256(key, plaintext) — the value stored in
// api_tokens.token_hmac and used for the indexed lookup.
func (h *Hasher) Digest(plaintext string) []byte {
	m := hmac.New(sha256.New, h.key)
	_, _ = m.Write([]byte(plaintext))
	return m.Sum(nil)
}

// GenerateToken mints a fresh `paladin_pat_…` token plus its display prefix and
// HMAC digest. The plaintext is returned exactly once; the caller persists
// prefix + digest only.
func (h *Hasher) GenerateToken() (plaintext, prefix string, digest []byte, err error) {
	raw := make([]byte, SecretBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", nil, fmt.Errorf("api_token: read random: %w", err)
	}
	encoded := base32NoPad.EncodeToString(raw)
	plaintext = TokenPrefix + encoded
	prefix = encoded[:PrefixLen]
	digest = h.Digest(plaintext)
	return plaintext, prefix, digest, nil
}

// SplitToken validates the `paladin_pat_` literal and returns the display prefix
// (first PrefixLen base32 chars of the body). Retained for admin display and
// audit; the verification lookup itself keys on the HMAC digest, not the
// prefix. Returns ErrTokenMalformed when the input isn't a well-formed token.
func SplitToken(plaintext string) (prefix string, err error) {
	if !strings.HasPrefix(plaintext, TokenPrefix) {
		return "", ErrTokenMalformed
	}
	body := plaintext[len(TokenPrefix):]
	if len(body) < PrefixLen {
		return "", ErrTokenMalformed
	}
	return body[:PrefixLen], nil
}

// DigestsEqual is a constant-time compare of two digests. The verify path
// itself relies on the DB unique-index equality (WHERE token_hmac = $1), so
// this is a utility for callers/tests that compare digests without leaking
// timing on the bytes.
func DigestsEqual(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}
