package auth

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"strings"

	"github.com/oleg-tkachuk/paladin/internal/config"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
)

type Principal struct {
	TenantID uuid.UUID
	Email    string
	Subject  string
	Issuer   string
}

type Authorizer interface {
	Authenticate(r *http.Request) (Principal, error)
	AuthenticateToken(ctx context.Context, token string) (Principal, error)
}

type JWTAuthorizer struct {
	cfg      config.Config
	verifier *oidc.IDTokenVerifier
	provider *oidc.Provider
}

func NewJWTAuthorizer(ctx context.Context, cfg config.Config) (*JWTAuthorizer, error) {
	if cfg.Auth.Mode == "disabled" {
		return &JWTAuthorizer{cfg: cfg}, nil
	}

	provider, err := oidc.NewProvider(ctx, cfg.Auth.OIDC.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create OIDC provider: %w", err)
	}

	verifier := provider.Verifier(&oidc.Config{
		ClientID:          cfg.Auth.OIDC.Audience,
		SkipClientIDCheck: cfg.Auth.OIDC.Audience == "",
		SkipExpiryCheck:   false,
	})

	return &JWTAuthorizer{
		cfg:      cfg,
		provider: provider,
		verifier: verifier,
	}, nil
}

func (a *JWTAuthorizer) Authenticate(r *http.Request) (Principal, error) {
	if a.cfg.Auth.Mode == "disabled" && a.cfg.Auth.DevPrincipalEnabled {
		return a.devPrincipal()
	}

	token, err := extractToken(r)
	if err != nil {
		return Principal{}, err
	}

	return a.AuthenticateToken(r.Context(), token)
}

func (a *JWTAuthorizer) AuthenticateToken(ctx context.Context, token string) (Principal, error) {
	if a.cfg.Auth.Mode == "disabled" && a.cfg.Auth.DevPrincipalEnabled {
		return a.devPrincipal()
	}

	idToken, err := a.verifier.Verify(ctx, token)
	if err != nil {
		return Principal{}, fmt.Errorf("failed to verify token: %w", err)
	}

	var claims map[string]interface{}
	if err := idToken.Claims(&claims); err != nil {
		return Principal{}, fmt.Errorf("failed to parse claims: %w", err)
	}

	tenantID, err := extractTenantID(claims, a.cfg.Auth.OIDC.TenantClaim)
	if err != nil {
		return Principal{}, err
	}

	email := extractString(claims, a.cfg.Auth.OIDC.EmailClaim, "")
	subject := extractString(claims, a.cfg.Auth.OIDC.SubjectClaim, "")

	return Principal{
		TenantID: tenantID,
		Email:    email,
		Subject:  subject,
		Issuer:   idToken.Issuer,
	}, nil
}

func (a *JWTAuthorizer) devPrincipal() (Principal, error) {
	dp := a.cfg.Auth.DevPrincipal
	tid, err := uuid.Parse(dp.TenantID)
	if err != nil {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			return Principal{}, fmt.Errorf("failed to generate random tenant id: %w", err)
		}
		tid = uuid.NewSHA1(uuid.Nil, b[:])
	}

	return Principal{
		TenantID: tid,
		Email:    dp.Email,
		Subject:  dp.Subject,
		Issuer:   "dev",
	}, nil
}

func extractToken(r *http.Request) (string, error) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", fmt.Errorf("missing authorization header")
	}
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", fmt.Errorf("invalid authorization header format")
	}
	return h[len(prefix):], nil
}

func extractTenantID(claims map[string]interface{}, claimName string) (uuid.UUID, error) {
	tidStr := extractString(claims, claimName, "")
	if tidStr == "" {
		return uuid.Nil, fmt.Errorf("missing %s claim", claimName)
	}
	tid, err := uuid.Parse(tidStr)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid tenant_id format: %w", err)
	}
	return tid, nil
}

func extractString(claims map[string]interface{}, key, fallback string) string {
	if v, ok := claims[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return fallback
}

// Context helpers

type ctxKey string

const principalKey ctxKey = "principal"

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey).(Principal)
	return p, ok
}
