package app

import (
	"errors"
	"fmt"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token"
	apitokenpg "github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token/postgres"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

// APITokenBundle bundles the issuer + verifier + store + rate limiter
// for the hashed-bearer M2M auth path. Lives on SharedDeps when the
// subsystem is enabled; nil otherwise. Callers (interceptor wiring,
// admin handler, purger) pull the pieces they need.
type APITokenBundle struct {
	Store    api_token.Store
	Issuer   *api_token.Issuer
	Verifier *api_token.Verifier
}

// BuildAPITokenBundle wires the api_token subsystem from cfg.APIToken
// + the SharedDeps Postgres pool. Returns nil with no error when the
// subsystem is disabled — callers treat absence as "no api_token auth
// path active" and fall through to JWT / capability.
func BuildAPITokenBundle(cfg config.APIToken, deps *SharedDeps) (*APITokenBundle, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	// The pepper is api_token.hmac_key alone (config validation requires
	// it); the session signing key no longer seeds it (ADR-0047).
	hasher, err := api_token.NewHasher([]byte(cfg.HMACKey))
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

	return &APITokenBundle{
		Store:    store,
		Issuer:   issuer,
		Verifier: verifier,
	}, nil
}

// _ avoids importing errors only for this file when the package is
// trimmed to no callers in some build mode.
var _ = errors.New
