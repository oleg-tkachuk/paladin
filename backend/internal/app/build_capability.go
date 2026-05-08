package app

import (
	"crypto/ed25519"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/capability"
	capabilitypg "github.com/oleg-tkachuk/paladin/internal/capability/postgres"
	"github.com/oleg-tkachuk/paladin/internal/config"
)

// CapabilityBundle bundles the ready-to-use issuer / verifier / store
// plus the JWKS document the admin plane serves. Lives on SharedDeps
// when capability is enabled; nil otherwise. Callers (interceptor,
// admin handler) pull the pieces they need.
type CapabilityBundle struct {
	Store    capability.Store
	Usage    capability.UsageStore
	Issuer   *capability.Issuer
	Verifier *capability.StandardVerifier
	Keys     *capability.StaticKeyResolver

	// PublicKeys is the kid → public key map exposed via the JWKS
	// endpoint. Refreshed in place when rotation lands; today it
	// holds the single boot-time signer key.
	PublicKeys map[string]ed25519.PublicKey

	// IssuerName is what the bundle's Issuer mints into `iss`. Mirrors
	// cfg.Capability.IssuerName but is convenient on the bundle.
	IssuerName string

	// EphemeralKey is true when the boot path generated the keypair
	// because no `signing_key_path` was set. Logged by the caller as a
	// dev-mode warning so an operator notices on first restart.
	EphemeralKey bool
}

// BuildCapabilityBundle wires the capability subsystem from cfg.Capability
// + the SharedDeps Postgres pool. Returns nil with a nil error when the
// subsystem is disabled — callers should treat absence as "no capability
// auth path active" and fall through to JWT.
func BuildCapabilityBundle(cfg config.Capability, deps *SharedDeps) (*CapabilityBundle, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.IssuerName == "" {
		return nil, errors.New("app: capability.issuer_name required when enabled")
	}

	kid, pub, priv, generated, err := capability.MustLoadOrGenerate(cfg.SigningKeyPath, cfg.SigningKeyKID)
	if err != nil {
		return nil, fmt.Errorf("app: capability key: %w", err)
	}
	if generated {
		deps.Logger.Warn(
			"capability: ephemeral signing key (no signing_key_path); restarts WILL invalidate every issued token",
			zap.String("kid", kid),
		)
	}

	signer, err := capability.NewEd25519Signer(kid, priv)
	if err != nil {
		return nil, fmt.Errorf("app: capability signer: %w", err)
	}

	store, err := capabilitypg.New(deps.Pool)
	if err != nil {
		return nil, fmt.Errorf("app: capability store: %w", err)
	}

	issuer, err := capability.NewIssuer(capability.IssuerConfig{
		Signer:     signer,
		Store:      store,
		IssuerName: cfg.IssuerName,
		DefaultTTL: cfg.DefaultTTL,
	})
	if err != nil {
		return nil, fmt.Errorf("app: capability issuer: %w", err)
	}

	keys := capability.NewStaticKeyResolver(map[string]ed25519.PublicKey{kid: pub})

	trusted := cfg.TrustedIssuers
	if len(trusted) == 0 {
		trusted = []string{cfg.IssuerName}
	}

	cache := capability.NewCachedRevocationChecker(store, cfg.RevocationCacheTTL)
	verifier, err := capability.NewStandardVerifier(capability.VerifierConfig{
		Keys:           keys,
		Revocations:    cache,
		TrustedIssuers: trusted,
		Leeway:         cfg.VerifierLeeway,
	})
	if err != nil {
		return nil, fmt.Errorf("app: capability verifier: %w", err)
	}

	// Metering decorator: BumpRequest / Charge / Refund emit OTel
	// metrics for the runtime counters. Pure pass-through on cold
	// MeterProvider so test paths and sidecar tools don't pay for
	// instrument lookups.
	usage := capability.WithMetering(capabilitypg.NewUsageStore(deps.DB.Queries))

	return &CapabilityBundle{
		Store:        store,
		Usage:        usage,
		Issuer:       issuer,
		Verifier:     verifier,
		Keys:         keys,
		PublicKeys:   map[string]ed25519.PublicKey{kid: pub},
		IssuerName:   cfg.IssuerName,
		EphemeralKey: generated,
	}, nil
}
