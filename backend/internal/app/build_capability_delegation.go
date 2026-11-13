package app

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"slices"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/biscuit"
	"github.com/oleg-tkachuk/limes/jwks"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/delegation"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/oauth"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"github.com/oleg-tkachuk/paladin/backend/internal/metrics"
)

// capabilityIssuers are the capability issuer and the delegation issuer the
// MCP server exchanges tokens through (ADR-0041), and what a verifier needs
// to tell their capabilities apart. Both sign with the capability key, each
// under a kid of its own that the verifier binds to it alone, so a delegated
// capability's `iss` is written by the delegation issuer and nothing else.
type capabilityIssuers struct {
	issuer, delegation *limes.Issuer
	// keys resolves both kids of the signing key and of every previous
	// one; trusted and keyIssuers are the verifier's TrustedIssuers and
	// KeyIssuers.
	keys       *jwks.Static
	trusted    []string
	keyIssuers map[string]string
	// published is the kid → key set the JWKS endpoint serves: the signing
	// key and the previous ones, each under its capability kid.
	published map[string]ed25519.PublicKey
}

// delegationKeyID is the kid the delegation issuer signs under.
func delegationKeyID(kid string) string { return kid + auth.DelegationIssuerSuffix }

// newCapabilityIssuers builds both issuers: the capability issuer over
// store, and the delegation issuer over delegated, which must record a
// capability under its own tenant, since the token endpoint it issues
// behind names none (pgstore.ScopedIssuance). extraTrusted are the
// issuers the operator also trusts (capability.trusted_issuers), kept as
// configured; with none, the capability issuer alone. previous are keys
// rotated out of signing (capability.previous_public_keys): verified under
// both of their kids, as the signing key is, and published, but never
// signed with.
func newCapabilityIssuers(key limes.SigningKey, previous []limes.PublicKey, store, delegated limes.IssuanceStore, issuerName string, extraTrusted []string, ttl time.Duration, log *zap.Logger) (*capabilityIssuers, error) {
	delegationName := auth.DelegationIssuerName(issuerName)
	delegationKID := delegationKeyID(key.KeyID)

	issuer, err := newCapabilityIssuer(key.KeyID, key, store, issuerName, ttl, log)
	if err != nil {
		return nil, err
	}
	delegation, err := newCapabilityIssuer(delegationKID, key, delegated, delegationName, ttl, log)
	if err != nil {
		return nil, err
	}
	set, err := newCapabilityKeySet(append([]limes.PublicKey{{KeyID: key.KeyID, Key: key.Public()}}, previous...), issuerName, delegationName)
	if err != nil {
		return nil, err
	}
	keys, err := jwks.NewStatic(jwks.StaticConfig{Keys: set.verified})
	if err != nil {
		return nil, fmt.Errorf("app: capability keys: %w", err)
	}
	trusted := slices.Clone(extraTrusted)
	if len(trusted) == 0 {
		trusted = []string{issuerName}
	}
	return &capabilityIssuers{
		issuer: issuer, delegation: delegation, keys: keys,
		trusted:    append(trusted, delegationName),
		keyIssuers: set.keyIssuers,
		published:  set.published,
	}, nil
}

// capabilityKeySet is every kid a verifier resolves, the issuer each is bound
// to, and the kids the JWKS publishes.
type capabilityKeySet struct {
	verified   map[string]ed25519.PublicKey
	keyIssuers map[string]string
	published  map[string]ed25519.PublicKey
}

// newCapabilityKeySet binds each key's capability kid to issuerName and its
// delegation kid to delegationName. keys[0] is the signing key. It refuses a
// kid two keys share, and a key listed twice under different kids: either
// would leave which key a token names a matter of configuration order.
func newCapabilityKeySet(keys []limes.PublicKey, issuerName, delegationName string) (capabilityKeySet, error) {
	set := capabilityKeySet{
		verified:   make(map[string]ed25519.PublicKey, 2*len(keys)),
		keyIssuers: make(map[string]string, 2*len(keys)),
		published:  make(map[string]ed25519.PublicKey, len(keys)),
	}
	for _, k := range keys {
		for kid := range set.published {
			if set.published[kid].Equal(k.Key) {
				return capabilityKeySet{}, fmt.Errorf("app: capability key %q is the key already listed as %q", k.KeyID, kid)
			}
		}
		for kid, name := range map[string]string{k.KeyID: issuerName, delegationKeyID(k.KeyID): delegationName} {
			if _, taken := set.verified[kid]; taken {
				return capabilityKeySet{}, fmt.Errorf("app: capability kid %q names two keys", kid)
			}
			set.verified[kid] = k.Key
			set.keyIssuers[kid] = name
		}
		set.published[k.KeyID] = k.Key
	}
	return set, nil
}

// Each issuer logs every token it mints, refuses or fails to (limes
// IssuerConfig.Logger), under its own name.
func newCapabilityIssuer(kid string, key limes.SigningKey, store limes.IssuanceStore, name string, ttl time.Duration, log *zap.Logger) (*limes.Issuer, error) {
	signer, err := limes.NewSigner(limes.SignerConfig{KeyID: kid, Key: key.Private})
	if err != nil {
		return nil, fmt.Errorf("app: capability signer %s: %w", kid, err)
	}
	issuer, err := limes.NewIssuer(limes.IssuerConfig{
		Biscuit: biscuit.Form(), Signer: signer, Store: store, IssuerName: name, DefaultTTL: ttl,
		Logger: logger.Slog(log, "capability-issuer"),
		Events: issuerEvents(metrics.RecordCapabilityCall),
	})
	if err != nil {
		return nil, fmt.Errorf("app: capability issuer %s: %w", name, err)
	}
	return issuer, nil
}

// userByID is the IAM user store as a delegated capability's lookup needs it.
type userByID interface {
	GetByID(ctx context.Context, id uuid.UUID) (authstore.User, error)
}

// delegatingUsers reads the user a delegated capability acts for from the
// IAM user store, on every call.
func delegatingUsers(users userByID) auth.DelegatingUserLookup {
	return func(ctx context.Context, id uuid.UUID) (auth.DelegatingUser, error) {
		u, err := users.GetByID(ctx, id)
		switch {
		case errors.Is(err, authstore.ErrNotFound):
			return auth.DelegatingUser{}, fmt.Errorf("%w: %s", auth.ErrDelegatingUserGone, id)
		case err != nil:
			return auth.DelegatingUser{}, err
		}
		return auth.DelegatingUser{TenantID: u.TenantID, Roles: u.Roles, Scopes: u.Scopes, Disabled: u.Disabled}, nil
	}
}

// delegationOption makes a plane's capability interceptor accept the
// delegation issuer's capabilities as their users.
func delegationOption(bundle *CapabilityBundle, users userByID) auth.CapabilityOption {
	return auth.WithDelegation(auth.DelegationIssuerName(bundle.Auth.IssuerName), delegatingUsers(users))
}

// errExchangeNeedsCapability is a token exchange configured with the
// capability subsystem off: there is no issuer to delegate through.
var errExchangeNeedsCapability = errors.New("app: an oauth seed client with token_exchange needs capability.enabled")

// withDelegationExchange serves the token-exchange grant on as to exchangers
// (ADR-0041): a paladin-mcp token, verified as the MCP edge does, for the
// one capability of the delegation issuer that stands in for it, recorded in
// the capability store and living as long as the token.
func withDelegationExchange(ctx context.Context, as *oauth.Handler, deps *SharedDeps, exchangers map[string]bool, users userByID) (*oauth.Handler, error) {
	if deps.Capability == nil {
		return nil, errExchangeNeedsCapability
	}
	cfg := deps.Cfg.Auth
	tokens, err := BuildVerifier(ctx, cfg, deps.SessionKeys, auth.AudienceMCP, deps.Logger)
	if err != nil {
		return nil, fmt.Errorf("oauth token exchange verifier: %w", err)
	}
	ex, err := delegation.NewExchanger(deps.Capability.Auth.Delegation, delegation.NewSubjectVerifier(tokens, cfg.Issuer),
		deps.Capability.Exchanges, users, deps.Cfg.Capability.Currency, logger.Slog(deps.Logger, "delegation"))
	if err != nil {
		return nil, err
	}
	return as.WithTokenExchange(ex, exchangers, deps.Capability.Auth.BulkRevocation)
}
