package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"uuid"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

// JWTVerifier verifies a JWT signed with one key — HS256 by an HMAC secret,
// RS256 or ES256 by a public key — through jwx, and returns the Principal its
// claims name.
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
	// ClientID and Scope are the RFC 9068 claims of a token an OAuth client
	// holds (ADR-0035).
	ClientID string `json:"client_id"`
	Scope    string `json:"scope"`
	// Act is the RFC 8693 actor of an exchanged token (ADR-0036).
	Act *struct {
		Sub string `json:"sub"`
	} `json:"act"`
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

// Verify validates the token and returns a Principal. The key decides the
// algorithm, never the token's header: an HMAC secret verifies HS256 only, an
// RSA key RS256 and an EC key ES256, so "none" and HS-on-RSA-key tokens fail.
func (v *JWTVerifier) Verify(_ context.Context, token string) (*Principal, error) {
	alg, err := signatureAlgorithm(v.Key)
	if err != nil {
		return nil, err
	}
	payload, err := jws.Verify([]byte(token), jws.WithKey(alg, v.Key))
	if err != nil {
		return nil, fmt.Errorf("jwt: signature: %w", err)
	}
	c, err := acceptedClaims(payload, v.ExpectedIssuer, v.ExpectedAudience, v.Leeway, v.Now)
	if err != nil {
		return nil, err
	}
	return principalFromClaims(c, v.ExpectedAudience)
}

// signatureAlgorithm is the one algorithm key verifies.
func signatureAlgorithm(key any) (jwa.SignatureAlgorithm, error) {
	switch key.(type) {
	case []byte:
		return jwa.HS256(), nil
	case *rsa.PublicKey:
		return jwa.RS256(), nil
	case *ecdsa.PublicKey:
		return jwa.ES256(), nil
	}
	return jwa.EmptySignatureAlgorithm(), fmt.Errorf("jwt: unsupported key %T", key)
}

// acceptedClaims reads a verified token's payload and accepts it at now: exp
// and nbf within leeway, and the issuer and audience when set. iat is not
// checked, as it never was here: a token is valid from nbf, not from when it
// was minted.
func acceptedClaims(payload []byte, issuer, audience string, leeway time.Duration, now func() time.Time) (jwtClaims, error) {
	if now == nil {
		now = time.Now
	}
	opts := []jwt.ParseOption{
		jwt.WithVerify(false),
		jwt.WithResetValidators(true),
		jwt.WithValidator(jwt.IsExpirationValid()),
		jwt.WithValidator(jwt.IsNbfValid()),
		jwt.WithAcceptableSkew(leeway),
		jwt.WithClock(jwt.ClockFunc(now)),
	}
	if issuer != "" {
		opts = append(opts, jwt.WithIssuer(issuer))
	}
	if audience != "" {
		opts = append(opts, jwt.WithAudience(audience))
	}
	if _, err := jwt.Parse(payload, opts...); err != nil {
		return jwtClaims{}, claimRefusal(err)
	}
	var c jwtClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return jwtClaims{}, fmt.Errorf("jwt: claims parse: %w", err)
	}
	return c, nil
}

// claimRefusal names why the claims were refused, as callers and logs read it.
func claimRefusal(err error) error {
	switch {
	case errors.Is(err, jwt.TokenExpiredError()):
		return fmt.Errorf("jwt: token expired: %w", err)
	case errors.Is(err, jwt.TokenNotYetValidError()):
		return fmt.Errorf("jwt: token not yet valid: %w", err)
	case errors.Is(err, jwt.InvalidIssuerError()):
		return fmt.Errorf("jwt: issuer mismatch: %w", err)
	case errors.Is(err, jwt.InvalidAudienceError()):
		return fmt.Errorf("jwt: audience mismatch: %w", err)
	}
	return fmt.Errorf("jwt: claims: %w", err)
}

// principalFromClaims is the Principal a verified token's claims name, the
// token having been accepted for audience.
func principalFromClaims(c jwtClaims, audience string) (*Principal, error) {
	scopes, err := ParseScopes(c.Scopes)
	if err != nil {
		return nil, fmt.Errorf("jwt: scopes claim: %w", err)
	}
	p := &Principal{
		Subject:    c.Sub,
		Roles:      c.Roles,
		Scopes:     scopes,
		Audience:   audience,
		Audiences:  audiences(c.Aud),
		Kind:       parseKind(c.Kind),
		Labels:     c.Labels,
		TenantSlug: c.TenantSlug,
	}
	if c.ClientID != "" {
		p.OAuthClientID = c.ClientID
		p.OAuthScopes = strings.Fields(c.Scope)
	}
	if c.Act != nil {
		p.Actor = c.Act.Sub
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
