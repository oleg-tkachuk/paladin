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
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

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

func New(cfg Config) (*Issuer, error) {
	if cfg.Issuer == "" {
		return nil, errors.New("issuer: Issuer claim is required")
	}
	if len(cfg.SigningKey) < 32 {
		return nil, errors.New("issuer: SigningKey must be at least 32 bytes")
	}
	if cfg.AccessTokenTTL <= 0 {
		cfg.AccessTokenTTL = 15 * time.Minute
	}
	if cfg.RefreshTokenTTL <= 0 {
		cfg.RefreshTokenTTL = 7 * 24 * time.Hour
	}
	if cfg.ScopedTokenMaxTTL <= 0 {
		cfg.ScopedTokenMaxTTL = 24 * time.Hour
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
		"jti":    uuid.Must(uuid.NewV7()).String(),
		"roles":  c.Roles,
		"scopes": scopes,
		"kind":   kindString(c.Kind),
	}
	if c.TenantID != uuid.Nil {
		claims["tenant"] = c.TenantID.String()
	}
	if c.TenantSlug != "" {
		claims["tenant_slug"] = c.TenantSlug
	}
	if len(c.Labels) > 0 {
		claims["labels"] = c.Labels
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
}

// MintRefresh returns a signed refresh token bound to paladin-iam audience.
func (i *Issuer) MintRefresh(c RefreshClaims) (string, time.Time, error) {
	if c.TokenID == uuid.Nil {
		return "", time.Time{}, errors.New("issuer: RefreshClaims.TokenID is required")
	}
	now := i.cfg.Now()
	exp := now.Add(i.cfg.RefreshTokenTTL)
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
	if c.TenantID != uuid.Nil {
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

func signHS256(secret []byte, claims map[string]any) (string, error) {
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	hb, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("issuer: header: %w", err)
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("issuer: claims: %w", err)
	}
	enc := base64.RawURLEncoding
	signing := enc.EncodeToString(hb) + "." + enc.EncodeToString(cb)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signing))
	return signing + "." + enc.EncodeToString(mac.Sum(nil)), nil
}
