package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"

	"golang.org/x/crypto/bcrypt"
)

// HashPassword returns a bcrypt hash with the default cost. Empty input is
// rejected — there is no legitimate reason to store an empty-password hash.
func HashPassword(plaintext string) ([]byte, error) {
	if plaintext == "" {
		return nil, errors.New("auth: empty password")
	}
	return bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
}

// CheckPassword returns nil iff `plaintext` matches `hash`. Errors are
// intentionally opaque (no leaking whether the user existed vs. the password
// was wrong).
func CheckPassword(hash []byte, plaintext string) error {
	return bcrypt.CompareHashAndPassword(hash, []byte(plaintext))
}

// GenerateApiKeySecret returns a fresh API-key secret of the form
// "paladin_pat_<base64>". The first 8 chars after the prefix are the
// `display_prefix` stored alongside the hash for lookup.
func GenerateApiKeySecret() (secret, displayPrefix string, err error) {
	const prefix = "paladin_pat_"
	var buf [24]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", "", err
	}
	enc := base64.RawURLEncoding.EncodeToString(buf[:])
	if len(enc) < 8 {
		return "", "", errors.New("auth: encoded key too short")
	}
	return prefix + enc, enc[:8], nil
}

// HashApiKeySecret hashes an API-key secret with bcrypt. Same primitive as
// passwords; we keep them in separate helpers so future migration to argon2id
// can swap one without the other.
func HashApiKeySecret(secret string) ([]byte, error) {
	if secret == "" {
		return nil, errors.New("auth: empty api key secret")
	}
	return bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
}

// CheckApiKeySecret returns nil iff the presented secret matches the hash.
func CheckApiKeySecret(hash []byte, secret string) error {
	return bcrypt.CompareHashAndPassword(hash, []byte(secret))
}
