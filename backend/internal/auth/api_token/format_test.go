package api_token

import (
	"bytes"
	"strings"
	"testing"
)

// testKey is a fixed ≥32-byte HMAC key for deterministic tests.
var testKey = []byte("test-hmac-key-0123456789-abcdefgh")

func mustHasher(t *testing.T) *Hasher {
	t.Helper()
	h, err := NewHasher(testKey)
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}
	return h
}

// TestNewHasher_RejectsShortKey enforces the 32-byte minimum.
func TestNewHasher_RejectsShortKey(t *testing.T) {
	t.Parallel()
	if _, err := NewHasher([]byte("too-short")); err == nil {
		t.Fatal("expected error for short key")
	}
}

// TestGenerateToken_Shape covers the format guarantees: prefix literal,
// body length, prefix-display extraction, and digest presence.
func TestGenerateToken_Shape(t *testing.T) {
	t.Parallel()
	h := mustHasher(t)

	plaintext, prefix, digest, err := h.GenerateToken()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.HasPrefix(plaintext, TokenPrefix) {
		t.Errorf("plaintext missing literal prefix: %q", plaintext)
	}
	body := strings.TrimPrefix(plaintext, TokenPrefix)
	// 32 random bytes → ⌈32 * 8 / 5⌉ = 52 chars in base32-no-padding.
	if want := 52; len(body) != want {
		t.Errorf("body length: got %d, want %d", len(body), want)
	}
	if len(prefix) != PrefixLen {
		t.Errorf("prefix length: got %d, want %d", len(prefix), PrefixLen)
	}
	if !strings.HasPrefix(body, prefix) {
		t.Errorf("prefix not at body start: prefix=%q body=%q", prefix, body)
	}
	// HMAC-SHA256 digest is 32 bytes and equals Digest(plaintext).
	if len(digest) != 32 {
		t.Errorf("digest length: got %d, want 32", len(digest))
	}
	if !bytes.Equal(digest, h.Digest(plaintext)) {
		t.Error("GenerateToken digest != Digest(plaintext)")
	}
}

// TestGenerateToken_Unique confirms each generation yields a fresh token.
func TestGenerateToken_Unique(t *testing.T) {
	t.Parallel()
	h := mustHasher(t)
	seen := map[string]struct{}{}
	for i := 0; i < 100; i++ {
		p, _, _, err := h.GenerateToken()
		if err != nil {
			t.Fatalf("generate[%d]: %v", i, err)
		}
		if _, dup := seen[p]; dup {
			t.Fatalf("duplicate token at iteration %d", i)
		}
		seen[p] = struct{}{}
	}
}

// TestDigest_Deterministic: the same key + plaintext always yields the same
// digest (required for the indexed lookup), and a different key yields a
// different digest (the pepper actually participates).
func TestDigest_Deterministic(t *testing.T) {
	t.Parallel()
	h := mustHasher(t)
	plaintext, _, _, err := h.GenerateToken()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !bytes.Equal(h.Digest(plaintext), h.Digest(plaintext)) {
		t.Fatal("Digest not deterministic")
	}
	other, err := NewHasher([]byte("different-key-0123456789-abcdefgh!"))
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}
	if bytes.Equal(h.Digest(plaintext), other.Digest(plaintext)) {
		t.Fatal("digest independent of key — pepper not applied")
	}
}

// TestDigest_WrongPlaintext: a different plaintext yields a different digest,
// so a wrong token cannot match the stored row.
func TestDigest_WrongPlaintext(t *testing.T) {
	t.Parallel()
	h := mustHasher(t)
	a, _, digestA, _ := h.GenerateToken()
	b, _, _, _ := h.GenerateToken()
	if a == b {
		t.Fatal("two generations collided")
	}
	if bytes.Equal(h.Digest(b), digestA) {
		t.Fatal("distinct plaintexts produced equal digests")
	}
}

// TestDigestsEqual exercises the constant-time compare helper.
func TestDigestsEqual(t *testing.T) {
	t.Parallel()
	h := mustHasher(t)
	p, _, d, _ := h.GenerateToken()
	if !DigestsEqual(d, h.Digest(p)) {
		t.Fatal("equal digests reported unequal")
	}
	if DigestsEqual(d, h.Digest(p+"x")) {
		t.Fatal("unequal digests reported equal")
	}
}

// TestSplitToken validates prefix extraction and malformed-input rejection.
func TestSplitToken(t *testing.T) {
	t.Parallel()
	h := mustHasher(t)
	plaintext, prefix, _, _ := h.GenerateToken()
	got, err := SplitToken(plaintext)
	if err != nil {
		t.Fatalf("split valid token: %v", err)
	}
	if got != prefix {
		t.Errorf("split prefix: got %q, want %q", got, prefix)
	}

	cases := []string{
		"",
		"random-string",
		TokenPrefix,         // prefix only, body too short
		TokenPrefix + "ABC", // body shorter than PrefixLen
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			if _, err := SplitToken(in); err == nil {
				t.Fatalf("expected error for %q", in)
			}
		})
	}
}
