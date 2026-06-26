// Package oauth implements the PALADIN IAM OAuth 2.1 Authorization Server
// (ADR-0009): the /authorize + /token + /register endpoints that mint the
// bearers the MCP Resource Server (ADR-0008) validates. This file is the
// PKCE (RFC 7636) primitive — the cryptographic heart of the public-client
// auth-code flow, kept dependency-free so it is exhaustively unit-testable
// without a DB or HTTP layer.
package oauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"
)

// PKCE challenge methods (RFC 7636 §4.2). S256 is mandatory for public
// clients; "plain" is accepted only when explicitly allowed by the caller.
const (
	PKCEMethodS256  = "S256"
	PKCEMethodPlain = "plain"
)

// code_verifier length bounds (RFC 7636 §4.1): 43–128 chars from the
// unreserved set [A-Z a-z 0-9 - . _ ~].
const (
	pkceVerifierMinLen = 43
	pkceVerifierMaxLen = 128
)

var (
	// ErrPKCEVerifierLength — verifier outside the RFC 7636 43–128 range.
	ErrPKCEVerifierLength = errors.New("oauth: code_verifier length out of range (43-128)")
	// ErrPKCEVerifierCharset — verifier contains a non-unreserved char.
	ErrPKCEVerifierCharset = errors.New("oauth: code_verifier contains an invalid character")
	// ErrPKCEMethodUnsupported — challenge method is neither S256 nor an
	// explicitly-allowed "plain".
	ErrPKCEMethodUnsupported = errors.New("oauth: unsupported code_challenge_method")
	// ErrPKCEMismatch — the verifier does not match the stored challenge.
	ErrPKCEMismatch = errors.New("oauth: code_verifier does not match code_challenge")
)

// ComputeS256Challenge returns base64url(sha256(verifier)) — the value a
// client sends as code_challenge with method=S256. Exposed for tests and for
// any server-side client helper.
func ComputeS256Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// ValidateVerifier checks the verifier conforms to RFC 7636 §4.1 (length +
// charset). Callers validate the verifier shape before comparing so a
// malformed verifier is a clear 400, not a silent mismatch.
func ValidateVerifier(verifier string) error {
	if len(verifier) < pkceVerifierMinLen || len(verifier) > pkceVerifierMaxLen {
		return ErrPKCEVerifierLength
	}
	for i := 0; i < len(verifier); i++ {
		if !isUnreserved(verifier[i]) {
			return ErrPKCEVerifierCharset
		}
	}
	return nil
}

// VerifyChallenge checks a presented code_verifier against the stored
// code_challenge + method. S256 is always accepted; "plain" only when
// allowPlain is true (public clients must use S256). The comparison is
// constant-time to avoid leaking the challenge via timing.
func VerifyChallenge(method, challenge, verifier string, allowPlain bool) error {
	if err := ValidateVerifier(verifier); err != nil {
		return err
	}
	switch method {
	case PKCEMethodS256:
		computed := ComputeS256Challenge(verifier)
		if subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) != 1 {
			return ErrPKCEMismatch
		}
		return nil
	case PKCEMethodPlain:
		if !allowPlain {
			return ErrPKCEMethodUnsupported
		}
		if subtle.ConstantTimeCompare([]byte(verifier), []byte(challenge)) != 1 {
			return ErrPKCEMismatch
		}
		return nil
	default:
		return ErrPKCEMethodUnsupported
	}
}

// NormalizeMethod maps an empty/missing code_challenge_method to its RFC 7636
// default ("plain") and lowercases nothing — S256 is case-sensitive per spec.
// Returns the method unchanged otherwise so the caller can reject unsupported
// values via VerifyChallenge.
func NormalizeMethod(method string) string {
	if strings.TrimSpace(method) == "" {
		return PKCEMethodPlain
	}
	return method
}

func isUnreserved(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z':
		return true
	case c >= 'a' && c <= 'z':
		return true
	case c >= '0' && c <= '9':
		return true
	case c == '-' || c == '.' || c == '_' || c == '~':
		return true
	default:
		return false
	}
}
