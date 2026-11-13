// Package issuer mints Paladin-internal JWT access + refresh tokens.
//
// Two token types:
//
//   - Access — short-lived (default 15min), audience matches the target
//     plane (paladin-data | paladin-admin | paladin-iam). Carries roles/scopes/tenant
//     and the principal kind.
//
//   - Refresh — longer-lived (default 7d), audience is always `paladin-iam`,
//     subject is the user_id, includes a token_id (jti) so the IAM store
//     can revoke it on rotate.
//
// Both are signed ES256 by the session key set's active key, under its kid
// (ADR-0047). Verifiers hold the set's public keys only: the issuer's own
// process through auth.KeySetVerifier, every other role through the JWKS the
// set serves. A rotation adds a key to the set and keeps the previous one
// published, so tokens it signed still verify until they expire.
package issuer

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"uuid"

	"github.com/lestrrat-go/jwx/v4/jwa"
	"github.com/lestrrat-go/jwx/v4/jws"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/sessionkey"
)

// Config bundles the static parameters for an Issuer.
type Config struct {
	Issuer            string          // "iss" claim value
	Keys              *sessionkey.Set // signs with its active key
	AccessTokenTTL    time.Duration   // typical 15m
	RefreshTokenTTL   time.Duration   // typical 168h (7d)
	ScopedTokenMaxTTL time.Duration   // upper bound on MintScopedToken (typical 24h)
	Now               func() time.Time
}

// Issuer signs tokens. Stateless — refresh-token revocation lives in the
// IAM store; this struct only emits values.
type Issuer struct {
	cfg Config
}

// What New takes for a TTL the config leaves at zero or below.
const (
	DefaultAccessTokenTTL    = 15 * time.Minute
	DefaultRefreshTokenTTL   = 7 * 24 * time.Hour
	DefaultScopedTokenMaxTTL = 24 * time.Hour
)

func New(cfg Config) (*Issuer, error) {
	if cfg.Issuer == "" {
		return nil, errors.New("issuer: Issuer claim is required")
	}
	if cfg.Keys == nil {
		return nil, errors.New("issuer: Keys is required")
	}
	if cfg.AccessTokenTTL <= 0 {
		cfg.AccessTokenTTL = DefaultAccessTokenTTL
	}
	if cfg.RefreshTokenTTL <= 0 {
		cfg.RefreshTokenTTL = DefaultRefreshTokenTTL
	}
	if cfg.ScopedTokenMaxTTL <= 0 {
		cfg.ScopedTokenMaxTTL = DefaultScopedTokenMaxTTL
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Issuer{cfg: cfg}, nil
}

// AccessClaims is the populated claim set for a freshly-minted access token.
type AccessClaims struct {
	Subject    string
	TenantID   uuid.UUID
	TenantSlug string
	Audience   string
	Roles      []string
	Scopes     []auth.Scope
	Kind       auth.PrincipalKind
	Labels     map[string]string
	// OAuthClientID and OAuthScopes are the grant an OAuth client holds
	// (ADR-0035), minted as the RFC 9068 `client_id` and `scope` claims.
	// Empty for every other token.
	OAuthClientID string
	OAuthScopes   []string
	// SessionID is the session the token is issued in, minted as `sid`: an
	// OAuth grant's refresh-token family, which revoking ends. Empty for a
	// token issued in none.
	SessionID string
	// Optional explicit TTL override (must be ≤ scoped_token_max_ttl).
	TTL time.Duration
}

// MintAccess returns a signed access token + its expiry. ttl == 0 → use
// the configured AccessTokenTTL.
func (i *Issuer) MintAccess(c AccessClaims) (string, time.Time, error) {
	if c.Audience == "" {
		return "", time.Time{}, errors.New("issuer: AccessClaims.Audience is required")
	}
	ttl := c.TTL
	if ttl <= 0 {
		ttl = i.cfg.AccessTokenTTL
	}
	if ttl > i.cfg.ScopedTokenMaxTTL {
		ttl = i.cfg.ScopedTokenMaxTTL
	}
	now := i.cfg.Now()
	exp := now.Add(ttl)

	scopes := make([]string, 0, len(c.Scopes))
	for _, s := range c.Scopes {
		scopes = append(scopes, s.String())
	}
	claims := map[string]any{
		"iss":    i.cfg.Issuer,
		"sub":    c.Subject,
		"aud":    c.Audience,
		"iat":    now.Unix(),
		"nbf":    now.Unix(),
		"exp":    exp.Unix(),
		"jti":    uuid.NewV7().String(),
		"roles":  c.Roles,
		"scopes": scopes,
		"kind":   kindString(c.Kind),
	}
	if c.TenantID != uuid.Nil() {
		claims["tenant"] = c.TenantID.String()
	}
	if c.TenantSlug != "" {
		claims["tenant_slug"] = c.TenantSlug
	}
	if len(c.Labels) > 0 {
		claims["labels"] = c.Labels
	}
	if c.OAuthClientID != "" {
		claims[auth.ClaimClientID] = c.OAuthClientID
		claims[auth.ClaimScope] = strings.Join(c.OAuthScopes, auth.ScopeSeparator)
	}
	if c.SessionID != "" {
		claims[auth.ClaimSessionID] = c.SessionID
	}
	tok, err := sign(i.cfg.Keys, claims)
	if err != nil {
		return "", time.Time{}, err
	}
	return tok, exp, nil
}

// RefreshClaims is the input to MintRefresh.
type RefreshClaims struct {
	Subject  string
	TenantID uuid.UUID
	UserID   uuid.UUID // for IAM store lookup
	// JTI returned by the caller-supplied generator; the IAM store records
	// this so it can revoke individual refresh tokens.
	TokenID uuid.UUID
	// ExpiresAt, when set, is the expiry to sign instead of now + TTL. Set
	// when re-issuing a token that already exists in the store, so the new
	// string expires with the row it stands for.
	ExpiresAt time.Time
}

// MintRefresh returns a signed refresh token bound to paladin-iam audience.
func (i *Issuer) MintRefresh(c RefreshClaims) (string, time.Time, error) {
	if c.TokenID == uuid.Nil() {
		return "", time.Time{}, errors.New("issuer: RefreshClaims.TokenID is required")
	}
	now := i.cfg.Now()
	exp := now.Add(i.cfg.RefreshTokenTTL)
	if !c.ExpiresAt.IsZero() {
		exp = c.ExpiresAt
	}
	claims := map[string]any{
		"iss":     i.cfg.Issuer,
		"sub":     c.Subject,
		"aud":     auth.AudienceIAM,
		"iat":     now.Unix(),
		"nbf":     now.Unix(),
		"exp":     exp.Unix(),
		"jti":     c.TokenID.String(),
		"user_id": c.UserID.String(),
		"refresh": true,
	}
	if c.TenantID != uuid.Nil() {
		claims["tenant"] = c.TenantID.String()
	}
	tok, err := sign(i.cfg.Keys, claims)
	if err != nil {
		return "", time.Time{}, err
	}
	return tok, exp, nil
}

func kindString(k auth.PrincipalKind) string {
	switch k {
	case auth.PrincipalKindUser:
		return "user"
	case auth.PrincipalKindApiKey:
		return "api_key"
	case auth.PrincipalKindServiceAccount:
		return "service_account"
	}
	return ""
}

// tokenType is the typ header of every token the issuer mints.
const tokenType = "JWT"

// sign is claims as a compact JWT signed ES256 by keys' active key, under
// its kid, through jws.
func sign(keys *sessionkey.Set, claims map[string]any) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("issuer: claims: %w", err)
	}
	hdr := jws.NewHeaders()
	if err := hdr.Set(jws.TypeKey, tokenType); err != nil {
		return "", fmt.Errorf("issuer: header: %w", err)
	}
	tok, err := jws.Sign(payload, jws.WithKey(jwa.ES256(), keys.Signer(), jws.WithProtectedHeaders(hdr)))
	if err != nil {
		return "", fmt.Errorf("issuer: sign: %w", err)
	}
	return string(tok), nil
}
