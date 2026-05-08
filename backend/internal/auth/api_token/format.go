package api_token

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters. Values picked to take ~50ms on a 2025-era
// server-class CPU — slow enough to make brute force expensive, fast
// enough that a well-scaled API plane can verify hundreds of tokens per
// second per pod. Parameters are encoded into the PHC output string so
// per-row rotation of params is transparent to the verifier.
//
// `argon2idTime` is the iteration count; `argon2idMemory` is in KiB
// (= 64 MiB); `argon2idParallel` keeps it single-threaded for
// predictability under high concurrency. SaltLen / KeyLen follow the
// argon2id reference recommendations.
const (
	argon2idTime     = 3
	argon2idMemory   = 64 * 1024 // 64 MiB
	argon2idParallel = 1
	argon2idSaltLen  = 16
	argon2idKeyLen   = 32
)

// base32NoPad is RFC 4648 base32 alphabet without padding. We strip
// padding so token strings are URL-safe-ish and easy to put in env
// vars; the literal `paladin_pat_` prefix is unmistakable.
var base32NoPad = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateToken mints a fresh `paladin_pat_…` token plus its display prefix
// and argon2id PHC hash. Caller persists prefix + hash; the plaintext
// is returned exactly once.
//
// Returns:
//   - plaintext: the full `paladin_pat_…` string the caller ships to the
//     consuming service.
//   - prefix:    PrefixLen-char display token used for indexed lookup.
//   - hash:      argon2id PHC string suitable for storage.
func GenerateToken() (plaintext, prefix, hash string, err error) {
	raw := make([]byte, SecretBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", "", fmt.Errorf("api_token: read random: %w", err)
	}
	encoded := base32NoPad.EncodeToString(raw)
	plaintext = TokenPrefix + encoded
	prefix = encoded[:PrefixLen]

	salt := make([]byte, argon2idSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", "", "", fmt.Errorf("api_token: read salt: %w", err)
	}
	digest := argon2.IDKey([]byte(plaintext), salt, argon2idTime, argon2idMemory, argon2idParallel, argon2idKeyLen)

	hash = encodePHC(digest, salt, argon2idTime, argon2idMemory, argon2idParallel)
	return plaintext, prefix, hash, nil
}

// CompareToken verifies a candidate plaintext token against a stored
// PHC hash. Constant-time comparison via argon2.IDKey + bytes equality.
// Returns nil on match, ErrTokenMalformed on parse failure, or a
// non-typed error on mismatch.
func CompareToken(plaintext, phc string) error {
	digest, salt, time, memory, parallel, keyLen, err := decodePHC(phc)
	if err != nil {
		return ErrTokenMalformed
	}
	candidate := argon2.IDKey([]byte(plaintext), salt, time, memory, parallel, keyLen)
	if !constantTimeEqual(candidate, digest) {
		return ErrTokenNotFound
	}
	return nil
}

// SplitToken pulls the display prefix off a `paladin_pat_…` plaintext for
// the indexed lookup phase of verification. Returns ErrTokenMalformed
// when the input doesn't start with the literal prefix or is too
// short to extract PrefixLen characters of body.
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

// PHC string format: `$argon2id$v=19$m=<memory>,t=<time>,p=<parallel>$<salt-b64>$<digest-b64>`.
// We use std base64 (with padding) per the de-facto PHC convention.

func encodePHC(digest, salt []byte, time, memory uint32, parallel uint8) string {
	saltB64 := base64StdEncode(salt)
	digestB64 := base64StdEncode(digest)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		memory, time, parallel, saltB64, digestB64)
}

func decodePHC(phc string) (digest, salt []byte, time, memory uint32, parallel uint8, keyLen uint32, err error) {
	// Field order: ["", "argon2id", "v=19", "m=...,t=...,p=...", salt, digest]
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("argon2id PHC: unexpected shape")
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &parallel); err != nil {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("argon2id PHC: bad params: %w", err)
	}
	salt, err = base64StdDecode(parts[4])
	if err != nil {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("argon2id PHC: bad salt: %w", err)
	}
	digest, err = base64StdDecode(parts[5])
	if err != nil {
		return nil, nil, 0, 0, 0, 0, fmt.Errorf("argon2id PHC: bad digest: %w", err)
	}
	keyLen = uint32(len(digest))
	return digest, salt, time, memory, parallel, keyLen, nil
}

// Tiny base64 helpers — we use stdlib but keep the call sites compact.
// We accept padding on decode and emit without padding on encode for
// shorter strings; encoded output uses std (URL-unsafe) alphabet
// because PHC strings are commonly stored in env vars or DB rows
// where + and / are unproblematic.

// base64Std is the std base64 alphabet without padding — PHC
// convention is unpadded, but decode accepts padding for robustness
// against older encoders.
var base64Std = base64.RawStdEncoding

func base64StdEncode(b []byte) string {
	return base64Std.EncodeToString(b)
}

func base64StdDecode(s string) ([]byte, error) {
	s = strings.TrimRight(s, "=")
	return base64Std.DecodeString(s)
}

// constantTimeEqual is the same as crypto/subtle.ConstantTimeCompare
// but returns bool instead of int. Used for hash comparison so timing
// attacks don't leak partial digest bits.
func constantTimeEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
