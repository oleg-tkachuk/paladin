package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/biscuit"
	"github.com/oleg-tkachuk/limes/dpop"
	"github.com/oleg-tkachuk/limes/jwks"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/limes/otelmetric"
	"github.com/oleg-tkachuk/limes/pgstore"
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
	CopyUsage meter.CopyUsageReader
	Usage     meter.UsageStore
	// Ledger reads the charges ledger for billing, and removes its old
	// charges for the capability purger when asked to.
	Ledger *pgstore.Ledger
	Issuer *limes.Issuer
	// Delegation issues the capabilities the MCP server exchanges callers'
	// tokens for, which act for their user (ADR-0041).
	Delegation *limes.Issuer
	Verifier   *limes.Verifier
	Keys       *jwks.Static

	// Revocations is the verifier's revocation cache, which the watcher
	// keeps in step; its Stats say whether a revocation reaches this
	// replica at once (the health page's capability-revocations).
	Revocations *limes.RevocationCache

	// Records reads capability records with their dispositions: a tenant's
	// for List, and one for Get.
	Records limes.RecordReporter

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

	key, generated, err := capabilityKey(cfg)
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
			zap.String("kid", key.KeyID),
			zap.String("env", deps.Cfg.App.Env),
		)
	}

	store, err := capabilitypg.New(deps.Pool)
	if err != nil {
		return nil, fmt.Errorf("app: capability store: %w", err)
	}
	records, err := capabilitypg.NewCensus(deps.Pool)
	if err != nil {
		return nil, fmt.Errorf("app: capability records: %w", err)
	}

	// The capability issuer, and the delegation issuer the MCP server
	// exchanges callers' tokens through (ADR-0041).
	issuers, err := newCapabilityIssuers(key, store, pgstore.ScopedIssuance(store), cfg.IssuerName, cfg.TrustedIssuers, cfg.DefaultTTL)
	if err != nil {
		return nil, err
	}

	// One cache answers for capabilities and for Biscuit copies alike. It
	// serves an answer for SyncedTTL while the watcher below listens, and
	// for TTL alone before and between its connections. The store must name
	// each answer's ancestors, so a revocation drops only what it outdates:
	// a decorator of it that forgot to forward RevocationLineage would turn
	// every revocation into a whole clear, and is refused here instead.
	// limes refuses a SyncedTTL shorter than the TTL; a deployment that
	// raised the TTL above the synced default gets the TTL for both, as it
	// had before the synced one existed, rather than a refusal to start.
	cache, err := limes.NewRevocationCache(limes.RevocationCacheConfig{
		Capabilities:   store,
		Biscuits:       store,
		TTL:            cfg.RevocationCacheTTL,
		SyncedTTL:      max(cfg.RevocationCacheSyncedTTL, cfg.RevocationCacheTTL),
		RequireLineage: true,
	})
	if err != nil {
		return nil, fmt.Errorf("app: capability revocation cache: %w", err)
	}
	// Its hits, misses, lookups, evictions and clears, and the state of the
	// revocation signal — synced, its connections, losses and
	// notifications — read when the provider collects. The registration
	// lives as long as the cache: the process.
	if _, err := otelmetric.InstrumentRevocationCache(cache); err != nil {
		return nil, fmt.Errorf("app: capability revocation cache metrics: %w", err)
	}

	// Revocations made on any replica clear this one's cache at once. The
	// watcher keeps the cache in step with its connection: it tells it when
	// it listens (a clear, then SyncedTTL) and when it stops (TTL alone, at
	// once). It holds a connection for the process lifetime, so it runs on
	// its own context, which StopWatchers cancels before the pool is closed
	// — the same arrangement as the Cedar watcher. It reconnects on its own.
	watchCtx, stopWatch := context.WithCancel(context.Background())
	watchLog := deps.Logger.Named("capability-revocation-watch")
	watcher := capabilitypg.NewRevocationWatcher(deps.Pool, func(err error) {
		watchLog.Warn("revocation watch reconnecting; revocation answers cached for the short TTL until it listens again", zap.Error(err))
	}, cache)
	go func() { _ = watcher.Run(watchCtx) }()
	deps.RegisterWatcherStop(stopWatch)
	replay, err := capabilitypg.NewReplayCache(capabilitypg.ReplayCacheConfig{
		Pool: deps.Pool, Log: deps.Logger.Named("dpop-replay"),
	})
	if err != nil {
		return nil, fmt.Errorf("app: DPoP replay cache: %w", err)
	}

	nonces, err := dpopNonces(cfg.DPoPNonce, deps.Cfg.App.Env, deps.Logger)
	if err != nil {
		return nil, err
	}

	// A key-bound capability must arrive with an RFC 9449 proof from its
	// key. The planes compare the procedure path only: behind a proxy they
	// cannot know the scheme and host clients address them by.
	proofs, err := dpop.NewVerifier(dpop.VerifierConfig{Replay: replay, MatchPathOnly: true, Nonces: nonces})
	if err != nil {
		return nil, fmt.Errorf("app: DPoP verifier: %w", err)
	}

	verifier, err := limes.NewVerifier(limes.VerifierConfig{
		Keys:           issuers.keys,
		Revocations:    cache,
		TrustedIssuers: issuers.trusted,
		KeyIssuers:     issuers.keyIssuers,
		Leeway:         cfg.VerifierLeeway,
		// Every capability is also handed out as a Biscuit its holder can
		// narrow offline (CapabilityService.Issue), so every plane takes one.
		// The capability interceptor passes a copy's own limits to the usage
		// store, which counts them (limes.copy_usage).
		Biscuit: &limes.BiscuitConfig{Form: biscuit.Form(), Revocations: cache, MeterCopies: true},
		DPoP:    proofs,
	})
	if err != nil {
		return nil, fmt.Errorf("app: capability verifier: %w", err)
	}

	// Metering decorator: Bump / Charge / Refund emit OTel
	// metrics for the runtime counters. Pure pass-through on cold
	// MeterProvider so test paths and sidecar tools don't pay for
	// instrument lookups.
	// Labelled by tenant, as every Paladin meter of tenant activity is.
	usageStore, err := capabilitypg.NewUsageStore(deps.Pool, capabilitypg.WithDefaultUnit(cfg.Currency))
	if err != nil {
		return nil, fmt.Errorf("app: capability usage store: %w", err)
	}
	usage, err := meteredUsage(usageStore)
	if err != nil {
		return nil, fmt.Errorf("app: capability metering: %w", err)
	}
	copyUsage, ok := usage.(meter.CopyUsageReader)
	if !ok {
		return nil, errors.New("app: the capability usage store reads no Biscuit copy counters")
	}
	ledger, err := capabilitypg.NewLedger(deps.Pool)
	if err != nil {
		return nil, fmt.Errorf("app: charges ledger: %w", err)
	}

	return &CapabilityBundle{
		Store:        store,
		Revocations:  cache,
		Copies:       store,
		CopyUsage:    copyUsage,
		Records:      records,
		Usage:        usage,
		Ledger:       ledger,
		Issuer:       issuers.issuer,
		Delegation:   issuers.delegation,
		Verifier:     verifier,
		Keys:         issuers.keys,
		Replay:       replay,
		PublicKeys:   map[string]ed25519.PublicKey{key.KeyID: key.Public()},
		IssuerName:   cfg.IssuerName,
		EphemeralKey: generated,
	}, nil
}

// capabilityKey is the issuer's signing key: the one at signing_key_path,
// published under signing_key_kid when it names one and under the kid
// derived from the key otherwise; or, with no path, a key generated for this
// process alone, reported as generated so the caller decides whether that is
// acceptable here.
func capabilityKey(cfg config.Capability) (key limes.SigningKey, generated bool, err error) {
	if cfg.SigningKeyPath == "" {
		key, err = limes.GenerateKey()
		return key, true, err
	}
	if key, err = limes.LoadKey(cfg.SigningKeyPath); err != nil {
		return limes.SigningKey{}, false, err
	}
	if cfg.SigningKeyKID != "" {
		key.KeyID = cfg.SigningKeyKID
	}
	return key, false, nil
}

// dpopNonces builds the nonces a key-bound capability's proof must carry, or
// none when they are off. Every replica must sign with the same key, or a
// nonce one issues is refused by the next; so a missing key refuses to start,
// except where an ephemeral key is acceptable, as for the signing key.
func dpopNonces(cfg config.DPoPNonce, env string, log *zap.Logger) (dpop.Nonces, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	key := []byte(cfg.Key)
	if len(key) == 0 {
		if !ephemeralKeyEnvs[env] {
			return nil, fmt.Errorf("app: capability.dpop_nonce.enabled with no key in env %q — "+
				"replicas with keys of their own refuse each other's nonces; set key_secret", env)
		}
		key = make([]byte, dpop.HMACNonceKeyBytes)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("app: DPoP nonce key: %w", err)
		}
		log.Warn("capability: ephemeral DPoP nonce key (no dpop_nonce.key); only one replica can check its nonces",
			zap.String("env", env))
	}
	nonces, err := dpop.NewHMACNonces(dpop.HMACNoncesConfig{Keys: dpop.SealNonceKeys(key), Lifetime: cfg.Lifetime})
	if err != nil {
		return nil, fmt.Errorf("app: DPoP nonces: %w", err)
	}
	return nonces, nil
}

// meteredUsage is store counted by limes's own instruments, by tenant: each
// charge, bump and reservation by outcome, and the amounts charged and
// refunded. Paladin counts none of them itself.
func meteredUsage(store meter.UsageStore) (meter.UsageStore, error) {
	return otelmetric.Instrument(store, otelmetric.WithTenantAttribute())
}
