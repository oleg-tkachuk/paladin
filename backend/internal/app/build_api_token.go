package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin-private/internal/auth/api_token"
	apitokenpg "github.com/oleg-tkachuk/paladin-private/internal/auth/api_token/postgres"
	"github.com/oleg-tkachuk/paladin-private/internal/auth/api_token/ratelimit"
	ratelimitpg "github.com/oleg-tkachuk/paladin-private/internal/auth/api_token/ratelimit/postgres"
	"github.com/oleg-tkachuk/paladin-private/internal/config"
)

// hmacKeyDerivationLabel domain-separates the api-token HMAC key derived
// from the auth signing key, so the derived pepper can never collide with
// the raw JWT signing key even though both come from the same secret.
const hmacKeyDerivationLabel = "paladin-api-token-hmac-v1"

// resolveAPITokenHMACKey returns the server-side HMAC key for token digests.
// Precedence: an explicit cfg.HMACKey (already resolved from HMACKeySecret by
// the config resolver), else a key DERIVED from the auth signing key via a
// domain-separated HMAC so dev deployments work without a dedicated secret.
// Returns an error only when neither source is available.
func resolveAPITokenHMACKey(cfg config.APIToken, authSigningKey string, log *zap.Logger) ([]byte, error) {
	if cfg.HMACKey != "" {
		return []byte(cfg.HMACKey), nil
	}
	if authSigningKey == "" {
		return nil, errors.New("api_token: no hmac_key and no auth.signing_key to derive from")
	}
	mac := hmac.New(sha256.New, []byte(authSigningKey))
	_, _ = mac.Write([]byte(hmacKeyDerivationLabel))
	if log != nil {
		log.Warn("api_token: deriving HMAC key from auth.signing_key; " +
			"set api_token.hmac_key_secret for key separation in production")
	}
	return mac.Sum(nil), nil
}

// APITokenBundle bundles the issuer + verifier + store + rate limiter
// for the hashed-bearer M2M auth path. Lives on SharedDeps when the
// subsystem is enabled; nil otherwise. Callers (interceptor wiring,
// admin handler, purger) pull the pieces they need.
type APITokenBundle struct {
	Store    api_token.Store
	Issuer   *api_token.Issuer
	Verifier *api_token.Verifier

	// Limiter enforces the per-token rate limit when a token's
	// RateLimitRPM > 0. Always populated when the subsystem is
	// enabled — Postgres-backed sliding window. Tokens with
	// RateLimitRPM = 0 are unrestricted (the limiter short-circuits).
	Limiter ratelimit.Limiter
}

// BuildAPITokenBundle wires the api_token subsystem from cfg.APIToken
// + the SharedDeps Postgres pool. Returns nil with no error when the
// subsystem is disabled — callers treat absence as "no api_token auth
// path active" and fall through to JWT / capability.
func BuildAPITokenBundle(cfg config.APIToken, authSigningKey string, log *zap.Logger, deps *SharedDeps) (*APITokenBundle, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	keyBytes, err := resolveAPITokenHMACKey(cfg, authSigningKey, log)
	if err != nil {
		return nil, fmt.Errorf("app: api_token hmac key: %w", err)
	}
	hasher, err := api_token.NewHasher(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("app: api_token hasher: %w", err)
	}

	store, err := apitokenpg.New(deps.Pool)
	if err != nil {
		return nil, fmt.Errorf("app: api_token store: %w", err)
	}

	issuer, err := api_token.NewIssuer(api_token.IssuerConfig{
		Store:  store,
		Hasher: hasher,
		MaxTTL: cfg.MaxTTL,
	})
	if err != nil {
		return nil, fmt.Errorf("app: api_token issuer: %w", err)
	}
	verifier, err := api_token.NewVerifier(api_token.VerifierConfig{
		Store:         store,
		Hasher:        hasher,
		Leeway:        cfg.VerifierLeeway,
		TouchLastUsed: cfg.TouchLastUsed,
	})
	if err != nil {
		return nil, fmt.Errorf("app: api_token verifier: %w", err)
	}

	limiter, err := ratelimitpg.New(deps.Pool)
	if err != nil {
		return nil, fmt.Errorf("app: api_token rate limiter: %w", err)
	}

	return &APITokenBundle{
		Store:    store,
		Issuer:   issuer,
		Verifier: verifier,
		Limiter:  limiter,
	}, nil
}

// _ avoids importing errors only for this file when the package is
// trimmed to no callers in some build mode.
var _ = errors.New
