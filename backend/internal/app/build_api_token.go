package app

import (
	"fmt"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token"
	apitokenpg "github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token/postgres"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

// APITokenBundle is the hashed-bearer M2M auth path: its store, which every
// role building the shared dependencies holds, and Auth, which issues and
// verifies tokens. Lives on SharedDeps when the subsystem is enabled; nil
// otherwise. Callers (interceptor wiring, admin handler, purger) pull the
// pieces they need.
type APITokenBundle struct {
	Store api_token.Store
	// Auth issues and verifies tokens. Nil in a role that does neither
	// (config.Builds.TokenAuth): such a role reads no pepper.
	Auth *APITokenAuth
}

// APITokenAuth issues and verifies API tokens, hashing them with the pepper.
type APITokenAuth struct {
	Issuer   *api_token.Issuer
	Verifier *api_token.Verifier
}

// BuildAPITokenBundle wires the api_token subsystem from cfg.APIToken
// + the SharedDeps Postgres pool. Returns nil with no error when the
// subsystem is disabled — callers treat absence as "no api_token auth
// path active" and fall through to JWT / capability. With tokenAuth false
// it builds the store alone and reads no pepper.
func BuildAPITokenBundle(cfg config.APIToken, deps *SharedDeps, tokenAuth bool) (*APITokenBundle, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	store, err := apitokenpg.New(deps.Pool)
	if err != nil {
		return nil, fmt.Errorf("app: api_token store: %w", err)
	}
	if !tokenAuth {
		deps.Logger.Info("api_token: store only; this role issues and verifies no token")
		return &APITokenBundle{Store: store}, nil
	}

	// The pepper is api_token.hmac_key alone (config validation requires
	// it); the session signing key no longer seeds it (ADR-0047).
	hasher, err := api_token.NewHasher([]byte(cfg.HMACKey))
	if err != nil {
		return nil, fmt.Errorf("app: api_token hasher: %w", err)
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
		Store: store,
		Auth:  &APITokenAuth{Issuer: issuer, Verifier: verifier},
	}, nil
}
