package api_token

import (
	"strings"
	"testing"
)

// TestGenerateToken_Shape covers the format guarantees: prefix literal,
// length of generated body, prefix-display extraction, hash format.
func TestGenerateToken_Shape(t *testing.T) {
	t.Parallel()

	plaintext, prefix, hash, err := GenerateToken()
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
	if !strings.HasPrefix(hash, "$argon2id$v=19$") {
		t.Errorf("hash not in PHC argon2id form: %q", hash)
	}
}

// TestGenerateToken_Unique confirms each generation yields a fresh
// token. Two collisions in 100 generations would imply a CSPRNG bug.
func TestGenerateToken_Unique(t *testing.T) {
	t.Parallel()
	seen := map[string]struct{}{}
	for i := 0; i < 100; i++ {
		p, _, _, err := GenerateToken()
		if err != nil {
			t.Fatalf("generate[%d]: %v", i, err)
		}
		if _, dup := seen[p]; dup {
			t.Fatalf("duplicate token at iteration %d", i)
		}
		seen[p] = struct{}{}
	}
}

// TestCompareToken_Match verifies a valid plaintext matches its hash.
func TestCompareToken_Match(t *testing.T) {
	t.Parallel()
	plaintext, _, hash, err := GenerateToken()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if err := CompareToken(plaintext, hash); err != nil {
		t.Fatalf("expected match, got %v", err)
	}
}

// TestCompareToken_WrongPlaintext returns ErrTokenNotFound for any
// plaintext that wasn't the original. We use a different valid-shaped
// token to exercise the argon2id miss path (length / format identical).
func TestCompareToken_WrongPlaintext(t *testing.T) {
	t.Parallel()
	_, _, hashA, _ := GenerateToken()
	plaintextB, _, _, _ := GenerateToken()
	if err := CompareToken(plaintextB, hashA); err == nil {
		t.Fatal("expected error on mismatch")
	}
}

// TestCompareToken_MalformedHash rejects PHC strings we can't parse.
func TestCompareToken_MalformedHash(t *testing.T) {
	t.Parallel()
	plaintext, _, _, _ := GenerateToken()
	cases := []string{
		"",
		"not-a-phc-string",
		"$argon2id$v=99$m=1024,t=1,p=1$YWFh$YmJi", // wrong version
		"$bcrypt$v=19$$YWFh$YmJi",                 // wrong scheme
	}
	for _, h := range cases {
		t.Run(h, func(t *testing.T) {
			err := CompareToken(plaintext, h)
			if err == nil {
				t.Fatalf("expected error for hash %q", h)
			}
		})
	}
}

// TestSplitToken validates prefix extraction and the malformed-input
// rejection paths.
func TestSplitToken(t *testing.T) {
	t.Parallel()
	plaintext, prefix, _, _ := GenerateToken()
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
