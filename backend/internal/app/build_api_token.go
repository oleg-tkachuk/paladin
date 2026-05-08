package app

import (
	"errors"
	"fmt"

	"github.com/oleg-tkachuk/paladin/internal/auth/api_token"
	apitokenpg "github.com/oleg-tkachuk/paladin/internal/auth/api_token/postgres"
	"github.com/oleg-tkachuk/paladin/internal/auth/api_token/ratelimit"
	ratelimitpg "github.com/oleg-tkachuk/paladin/internal/auth/api_token/ratelimit/postgres"
	"github.com/oleg-tkachuk/paladin/internal/config"
)

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
func BuildAPITokenBundle(cfg config.APIToken, deps *SharedDeps) (*APITokenBundle, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	store, err := apitokenpg.New(deps.Pool)
	if err != nil {
		return nil, fmt.Errorf("app: api_token store: %w", err)
	}

	issuer, err := api_token.NewIssuer(api_token.IssuerConfig{
		Store:  store,
		MaxTTL: cfg.MaxTTL,
	})
	if err != nil {
		return nil, fmt.Errorf("app: api_token issuer: %w", err)
	}
	verifier, err := api_token.NewVerifier(api_token.VerifierConfig{
		Store:         store,
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
