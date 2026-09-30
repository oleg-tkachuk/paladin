package auth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
)

// JWTVerifier is a minimal JWT verifier that supports ES256/RS256/HS256 and
// exposes only the claims Paladin needs. Intentionally small: swap for a full
// library (go-jose, lestrrat-go/jwx) when multi-key rotation / JWKS caching
// is required in production.
type JWTVerifier struct {
	// Key may be *rsa.PublicKey, *ecdsa.PublicKey, or []byte (HS256 secret).
	Key any
	// ExpectedIssuer must match the `iss` claim.
	ExpectedIssuer string
	// ExpectedAudience matches against the `aud` claim (string or array).
	ExpectedAudience string
	// Leeway tolerates small clock skew on exp/nbf.
	Leeway time.Duration
	// Now is injectable for tests.
	Now func() time.Time
}

type jwtClaims struct {
	Iss string          `json:"iss"`
	Sub string          `json:"sub"`
	Aud json.RawMessage `json:"aud"`
	Exp int64           `json:"exp"`
	Nbf int64           `json:"nbf"`
	// Tenant — canonical claim name. Paladin's own issuer always emits this.
	Tenant string `json:"tenant"`
	// TenantAlt — accepted alias for compatibility with third-party IdPs
	// (Auth0, Keycloak, custom dev tooling) that conventionally emit
	// `tenant_id`. The verifier prefers `tenant` when both are set.
	TenantAlt  string            `json:"tenant_id"`
	TenantSlug string            `json:"tenant_slug"`
	Roles      []string          `json:"roles"`
	Scopes     []string          `json:"scopes"`
	Kind       string            `json:"kind"`
	Labels     map[string]string `json:"labels"`
}

// tenantClaim returns the effective tenant identifier from the claim
// set, preferring `tenant` over `tenant_id` when both are present.
// Centralised so future aliases (e.g. an OIDC-mandated `tid`) land in
// one place.
func (c *jwtClaims) tenantClaim() string {
	if c.Tenant != "" {
		return c.Tenant
	}
	return c.TenantAlt
}

// Verify validates the token and returns a Principal. The signature-algorithm
// dispatch is intentionally strict — "none" and HS-on-RSA-key attacks are
// rejected at the algorithm check.
func (v *JWTVerifier) Verify(_ context.Context, token string) (*Principal, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("jwt: malformed token")
	}
	headerB, err := b64Decode(parts[0])
	if err != nil {
		return nil, fmt.Errorf("jwt: header decode: %w", err)
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerB, &hdr); err != nil {
		return nil, fmt.Errorf("jwt: header parse: %w", err)
	}

	signingInput := parts[0] + "." + parts[1]
	sigB, err := b64Decode(parts[2])
	if err != nil {
		return nil, fmt.Errorf("jwt: signature decode: %w", err)
	}

	if err := verifySignature(hdr.Alg, v.Key, []byte(signingInput), sigB); err != nil {
		return nil, err
	}

	claimsB, err := b64Decode(parts[1])
	if err != nil {
		return nil, fmt.Errorf("jwt: claims decode: %w", err)
	}
	var c jwtClaims
	if err := json.Unmarshal(claimsB, &c); err != nil {
		return nil, fmt.Errorf("jwt: claims parse: %w", err)
	}

	now := time.Now
	if v.Now != nil {
		now = v.Now
	}
	if c.Exp > 0 && now().After(time.Unix(c.Exp, 0).Add(v.Leeway)) {
		return nil, errors.New("jwt: token expired")
	}
	if c.Nbf > 0 && now().Before(time.Unix(c.Nbf, 0).Add(-v.Leeway)) {
		return nil, errors.New("jwt: token not yet valid")
	}
	if v.ExpectedIssuer != "" && c.Iss != v.ExpectedIssuer {
		return nil, errors.New("jwt: issuer mismatch")
	}
	if v.ExpectedAudience != "" && !audienceMatches(c.Aud, v.ExpectedAudience) {
		return nil, errors.New("jwt: audience mismatch")
	}

	scopes, err := ParseScopes(c.Scopes)
	if err != nil {
		return nil, fmt.Errorf("jwt: scopes claim: %w", err)
	}
	p := &Principal{
		Subject:    c.Sub,
		Roles:      c.Roles,
		Scopes:     scopes,
		Audience:   v.ExpectedAudience,
		Audiences:  audiences(c.Aud),
		Kind:       parseKind(c.Kind),
		Labels:     c.Labels,
		TenantSlug: c.TenantSlug,
	}
	if c.Exp > 0 {
		p.ExpiresAt = time.Unix(c.Exp, 0)
	}
	if t := c.tenantClaim(); t != "" {
		id, err := uuid.Parse(t)
		if err != nil {
			return nil, fmt.Errorf("jwt: tenant claim: %w", err)
		}
		p.TenantID = id
	}
	return p, nil
}

func parseKind(s string) PrincipalKind {
	switch s {
	case "user":
		return PrincipalKindUser
	case "api_key":
		return PrincipalKindApiKey
	case "service_account":
		return PrincipalKindServiceAccount
	}
	return PrincipalKindUnspecified
}

func b64Decode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

// audiences reads the `aud` claim, which RFC 7519 allows as one string or an
// array of them.
func audiences(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []string{s}
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	return nil
}

func audienceMatches(raw json.RawMessage, want string) bool {
	if len(raw) == 0 {
		return false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s == want
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		for _, a := range arr {
			if a == want {
				return true
			}
		}
	}
	return false
}

// verifySignature dispatches on `alg` and validates the signature against the
// configured key. Strict algorithm-to-key binding rejects alg-confusion attacks
// (e.g. "none", HS256 with an RSA public key).
func verifySignature(alg string, key any, signingInput, sig []byte) error {
	digest := sha256.Sum256(signingInput)
	switch alg {
	case "RS256":
		pub, ok := key.(*rsa.PublicKey)
		if !ok {
			return errors.New("jwt: RS256 requires *rsa.PublicKey")
		}
		if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
			return fmt.Errorf("jwt: RS256 verify: %w", err)
		}
		return nil
	case "ES256":
		pub, ok := key.(*ecdsa.PublicKey)
		if !ok {
			return errors.New("jwt: ES256 requires *ecdsa.PublicKey")
		}
		// JWS ES256 signatures are R||S concatenated (each 32 bytes for P-256),
		// not ASN.1. See RFC 7518 §3.4.
		if len(sig) != 64 {
			return fmt.Errorf("jwt: ES256 signature length %d, want 64", len(sig))
		}
		r := new(big.Int).SetBytes(sig[:32])
		s := new(big.Int).SetBytes(sig[32:])
		if !ecdsa.Verify(pub, digest[:], r, s) {
			return errors.New("jwt: ES256 verify failed")
		}
		return nil
	case "HS256":
		secret, ok := key.([]byte)
		if !ok {
			return errors.New("jwt: HS256 requires []byte")
		}
		mac := hmac.New(sha256.New, secret)
		mac.Write(signingInput)
		if !hmac.Equal(mac.Sum(nil), sig) {
			return errors.New("jwt: HS256 verify failed")
		}
		return nil
	default:
		return fmt.Errorf("jwt: unsupported alg %q", alg)
	}
}
