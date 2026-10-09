package app

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/limes"
	capabilitypg "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

// CapabilityBundle bundles the ready-to-use issuer / verifier / store
// plus the JWKS document the admin plane serves. Lives on SharedDeps
// when capability is enabled; nil otherwise. Callers (interceptor,
// admin handler) pull the pieces they need.
type CapabilityBundle struct {
	Store limes.Store
	// Copies records revoked copies of a capability's Biscuit.
	Copies limes.BiscuitRevocationStore
	// CopyUsage reads the counters of Biscuit copies with limits of their own.
	CopyUsage limes.CopyUsageReader
	Usage     limes.UsageStore[pgx.Tx]
	Issuer    *limes.Issuer
	Verifier  *limes.StandardVerifier
	Keys      *limes.StaticKeyResolver

	// DPoP checks the RFC 9449 proof a key-bound capability must arrive
	// with, against Replay.
	DPoP *limes.DPoPVerifier

	// Replay records the DPoP proof ids every replica has accepted, so a
	// proof replayed against any of them is refused. The capability
	// purger drops the ids that have expired.
	Replay *capabilitypg.ReplayCache

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

// ephemeralKeyEnvs are the environments where running the capability issuer
// on a generated, non-persisted signing key is acceptable — throwaway ones
// where losing every token on restart costs nothing.
//
// Everything not listed here refuses to start rather than degrade quietly.
var ephemeralKeyEnvs = map[string]bool{
	"local": true,
	"dev":   true,
	"test":  true,
	"ci":    true,
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

	kid, pub, priv, generated, err := limes.MustLoadOrGenerate(cfg.SigningKeyPath, cfg.SigningKeyKID)
	if err != nil {
		return nil, fmt.Errorf("app: capability key: %w", err)
	}
	if generated {
		// An ephemeral key means every pod restart silently invalidates every
		// capability agents are still holding. That is fine for a laptop and
		// catastrophic anywhere else, and a startup warning is the wrong
		// mechanism: nobody reads pod logs on a green rollout.
		//
		// Allow-listed rather than deny-listed on purpose — a new environment
		// name defaults to REFUSING to start, not to shipping ephemeral keys.
		// The failure mode of guessing wrong here is asymmetric.
		if !ephemeralKeyEnvs[deps.Cfg.App.Env] {
			return nil, fmt.Errorf(
				"app: capability.enabled=true with no signing_key_path in env %q — "+
					"an ephemeral key invalidates every issued token on restart; "+
					"mount a persistent key or add %q to ephemeralKeyEnvs if this "+
					"environment is genuinely disposable",
				deps.Cfg.App.Env, deps.Cfg.App.Env)
		}
		deps.Logger.Warn(
			"capability: ephemeral signing key (no signing_key_path); restarts WILL invalidate every issued token",
			zap.String("kid", kid),
			zap.String("env", deps.Cfg.App.Env),
		)
	}

	signer, err := limes.NewEd25519Signer(kid, priv)
	if err != nil {
		return nil, fmt.Errorf("app: capability signer: %w", err)
	}

	store, err := capabilitypg.New(deps.Pool)
	if err != nil {
		return nil, fmt.Errorf("app: capability store: %w", err)
	}

	issuer, err := limes.NewIssuer(limes.IssuerConfig{
		Signer:     signer,
		Store:      store,
		IssuerName: cfg.IssuerName,
		DefaultTTL: cfg.DefaultTTL,
	})
	if err != nil {
		return nil, fmt.Errorf("app: capability issuer: %w", err)
	}

	keys := limes.NewStaticKeyResolver(map[string]ed25519.PublicKey{kid: pub})

	trusted := cfg.TrustedIssuers
	if len(trusted) == 0 {
		trusted = []string{cfg.IssuerName}
	}

	cache := limes.NewCachedRevocationChecker(store, cfg.RevocationCacheTTL)
	copies := limes.NewCachedBiscuitRevocationChecker(store, cfg.RevocationCacheTTL)

	// Revocations made on any replica clear this one's cache at once; the
	// TTL above is only the fallback for while the LISTEN connection is
	// down. The watcher holds a pooled connection for the process lifetime,
	// so it runs on its own context, which StopWatchers cancels before the
	// pool is closed — the same arrangement as the Cedar watcher.
	watchCtx, stopWatch := context.WithCancel(context.Background())
	clearCaches := func() { cache.Clear(); copies.Clear() }
	if err := capabilitypg.NewRevocationWatcher(deps.Pool, clearCaches).Start(watchCtx); err != nil {
		stopWatch()
		return nil, fmt.Errorf("app: capability revocation watch: %w", err)
	}
	deps.RegisterWatcherStop(stopWatch)
	verifier, err := limes.NewStandardVerifier(limes.VerifierConfig{
		Keys:           keys,
		Revocations:    cache,
		TrustedIssuers: trusted,
		Leeway:         cfg.VerifierLeeway,
		// Every capability is also handed out as a Biscuit its holder can
		// narrow offline (CapabilityService.Issue), so every plane takes one.
		AcceptBiscuit:      true,
		BiscuitRevocations: copies,
		// The capability interceptor passes a copy's own limits to the usage
		// store, which counts them (capability_copy_usage).
		MeterCopies: true,
	})
	if err != nil {
		return nil, fmt.Errorf("app: capability verifier: %w", err)
	}

	// Metering decorator: Bump / Charge / Refund emit OTel
	// metrics for the runtime counters. Pure pass-through on cold
	// MeterProvider so test paths and sidecar tools don't pay for
	// instrument lookups.
	usage := limes.WithMetering[pgx.Tx](capabilitypg.NewUsageStore(deps.DB.Queries, deps.Pool, deps.Logger.Named("capability-usage")))
	copyUsage, ok := usage.(limes.CopyUsageReader)
	if !ok {
		return nil, errors.New("app: the capability usage store reads no Biscuit copy counters")
	}

	replay, err := capabilitypg.NewReplayCache(deps.Pool, deps.Logger.Named("dpop-replay"))
	if err != nil {
		return nil, fmt.Errorf("app: DPoP replay cache: %w", err)
	}

	return &CapabilityBundle{
		Store:        store,
		Copies:       store,
		CopyUsage:    copyUsage,
		Usage:        usage,
		Issuer:       issuer,
		Verifier:     verifier,
		Keys:         keys,
		DPoP:         &limes.DPoPVerifier{Replay: replay},
		Replay:       replay,
		PublicKeys:   map[string]ed25519.PublicKey{kid: pub},
		IssuerName:   cfg.IssuerName,
		EphemeralKey: generated,
	}, nil
}
