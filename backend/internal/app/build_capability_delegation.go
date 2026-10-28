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
	"github.com/oleg-tkachuk/limes/jwks"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
)

// capabilityIssuers are the capability issuer and the delegation issuer the
// MCP server exchanges tokens through (ADR-0041), and what a verifier needs
// to tell their capabilities apart. Both sign with the capability key, each
// under a kid of its own that the verifier binds to it alone, so a delegated
// capability's `iss` is written by the delegation issuer and nothing else.
type capabilityIssuers struct {
	issuer, delegation *limes.Issuer
	// keys resolves both kids; trusted and keyIssuers are the verifier's
	// TrustedIssuers and KeyIssuers.
	keys       *jwks.Static
	trusted    []string
	keyIssuers map[string]string
}

// delegationKeyID is the kid the delegation issuer signs under.
func delegationKeyID(kid string) string { return kid + auth.DelegationIssuerSuffix }

// newCapabilityIssuers builds both issuers over store. extraTrusted are the
// issuers the operator also trusts (capability.trusted_issuers), kept as
// configured; with none, the capability issuer alone.
func newCapabilityIssuers(key limes.SigningKey, store limes.IssuanceStore, issuerName string, extraTrusted []string, ttl time.Duration) (*capabilityIssuers, error) {
	delegationName := auth.DelegationIssuerName(issuerName)
	delegationKID := delegationKeyID(key.KeyID)

	issuer, err := newCapabilityIssuer(key.KeyID, key, store, issuerName, ttl)
	if err != nil {
		return nil, err
	}
	delegation, err := newCapabilityIssuer(delegationKID, key, store, delegationName, ttl)
	if err != nil {
		return nil, err
	}
	keys, err := jwks.NewStatic(jwks.StaticConfig{Keys: map[string]ed25519.PublicKey{
		key.KeyID: key.Public(), delegationKID: key.Public(),
	}})
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
		keyIssuers: map[string]string{key.KeyID: issuerName, delegationKID: delegationName},
	}, nil
}

func newCapabilityIssuer(kid string, key limes.SigningKey, store limes.IssuanceStore, name string, ttl time.Duration) (*limes.Issuer, error) {
	signer, err := limes.NewSigner(limes.SignerConfig{KeyID: kid, Key: key.Private})
	if err != nil {
		return nil, fmt.Errorf("app: capability signer %s: %w", kid, err)
	}
	issuer, err := limes.NewIssuer(limes.IssuerConfig{Signer: signer, Store: store, IssuerName: name, DefaultTTL: ttl})
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
	return auth.WithDelegation(auth.DelegationIssuerName(bundle.IssuerName), delegatingUsers(users))
}
