package oauth

import (
	"errors"
	"strings"
	"testing"
)

// A valid 43-char verifier (minimum length) from the unreserved set.
const sampleVerifier = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ" // 43 chars

func TestComputeS256Challenge_Known(t *testing.T) {
	// RFC 7636 appendix B test vector.
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	const want = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if got := ComputeS256Challenge(verifier); got != want {
		t.Fatalf("S256 challenge = %q, want %q (RFC 7636 vector)", got, want)
	}
}

func TestValidateVerifier(t *testing.T) {
	cases := []struct {
		name     string
		verifier string
		wantErr  error
	}{
		{"min length ok", sampleVerifier, nil},
		{"too short", strings.Repeat("a", 42), ErrPKCEVerifierLength},
		{"max length ok", strings.Repeat("a", 128), nil},
		{"too long", strings.Repeat("a", 129), ErrPKCEVerifierLength},
		{"bad charset", strings.Repeat("a", 42) + "!", ErrPKCEVerifierCharset},
		{"unreserved tilde/dot/dash/underscore", strings.Repeat("a", 39) + "-._~", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateVerifier(tc.verifier); !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateVerifier(%q) err = %v, want %v", tc.verifier, err, tc.wantErr)
			}
		})
	}
}

func TestVerifyChallenge_S256(t *testing.T) {
	challenge := ComputeS256Challenge(sampleVerifier)

	if err := VerifyChallenge(PKCEMethodS256, challenge, sampleVerifier, false); err != nil {
		t.Errorf("matching S256 verifier rejected: %v", err)
	}
	wrong := "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz" // 43 chars, wrong value
	if err := VerifyChallenge(PKCEMethodS256, challenge, wrong, false); !errors.Is(err, ErrPKCEMismatch) {
		t.Errorf("wrong verifier err = %v, want ErrPKCEMismatch", err)
	}
}

func TestVerifyChallenge_PlainGatedByAllowPlain(t *testing.T) {
	// plain: challenge == verifier.
	if err := VerifyChallenge(PKCEMethodPlain, sampleVerifier, sampleVerifier, true); err != nil {
		t.Errorf("allowed plain rejected: %v", err)
	}
	if err := VerifyChallenge(PKCEMethodPlain, sampleVerifier, sampleVerifier, false); !errors.Is(err, ErrPKCEMethodUnsupported) {
		t.Errorf("plain when disallowed err = %v, want ErrPKCEMethodUnsupported (public clients must use S256)", err)
	}
}

func TestVerifyChallenge_UnsupportedMethod(t *testing.T) {
	if err := VerifyChallenge("RS256", "x", sampleVerifier, true); !errors.Is(err, ErrPKCEMethodUnsupported) {
		t.Errorf("err = %v, want ErrPKCEMethodUnsupported", err)
	}
}

func TestNormalizeMethod(t *testing.T) {
	if NormalizeMethod("") != PKCEMethodPlain {
		t.Error("empty method should default to plain (RFC 7636)")
	}
	if NormalizeMethod("S256") != PKCEMethodS256 {
		t.Error("S256 must pass through unchanged (case-sensitive)")
	}
}
