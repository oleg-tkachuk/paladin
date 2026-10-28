// Package issuer mints Paladin-internal JWT access + refresh tokens.
//
// Two token types:
//
//   - Access — short-lived (default 15min), audience matches the target
//     plane (paladin-data | paladin-admin | paladin-iam). Carries roles/scopes/tenant
//     and the principal kind. Verified by the standard auth.JWTVerifier.
//
//   - Refresh — longer-lived (default 7d), audience is always `paladin-iam`,
//     subject is the user_id, includes a token_id (jti) so the IAM store
//     can revoke it on rotate.
//
// Issuer is intentionally HS256-only for v1 — keys live in the secret store
// (`auth.signing_key_secret_ref`). Rotation is supported via two-key window:
// the issuer signs with key.Active and the verifier accepts both Active and
// Previous for `key.GracePeriod`.
package issuer

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"uuid"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// Config bundles the static parameters for an Issuer.
type Config struct {
	Issuer            string        // "iss" claim value
	SigningKey        []byte        // HS256 secret
	AccessTokenTTL    time.Duration // typical 15m
	RefreshTokenTTL   time.Duration // typical 168h (7d)
	ScopedTokenMaxTTL time.Duration // upper bound on MintScopedToken (typical 24h)
	Now               func() time.Time
}

// Issuer signs tokens. Stateless — refresh-token revocation lives in the
// IAM store; this struct only emits values.
type Issuer struct {
	cfg Config
}

// MinSigningKeyBytes is the shortest HMAC key New accepts: HS256's own
// output size, below which the key, not the hash, bounds a forgery.
const MinSigningKeyBytes = 32

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
	if len(cfg.SigningKey) < MinSigningKeyBytes {
		return nil, fmt.Errorf("issuer: SigningKey must be at least %d bytes", MinSigningKeyBytes)
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
	// Actor is the OAuth client that exchanged the token on the subject's
	// behalf (RFC 8693), minted as `act`. Empty for a token not exchanged.
	Actor string
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
	if c.Actor != "" {
		claims[auth.ClaimActor] = map[string]string{auth.ClaimActorSubject: c.Actor}
	}
	tok, err := signHS256(i.cfg.SigningKey, claims)
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
	tok, err := signHS256(i.cfg.SigningKey, claims)
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

// signHS256 is claims as a compact JWT signed with secret by HS256, through
// jws.
func signHS256(secret []byte, claims map[string]any) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("issuer: claims: %w", err)
	}
	hdr := jws.NewHeaders()
	if err := hdr.Set(jws.TypeKey, tokenType); err != nil {
		return "", fmt.Errorf("issuer: header: %w", err)
	}
	tok, err := jws.Sign(payload, jws.WithKey(jwa.HS256(), secret, jws.WithProtectedHeaders(hdr)))
	if err != nil {
		return "", fmt.Errorf("issuer: sign: %w", err)
	}
	return string(tok), nil
}
