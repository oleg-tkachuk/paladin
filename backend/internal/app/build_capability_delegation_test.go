package app

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/memstore"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
)

const (
	testIssuerName  = "paladin-capability"
	testCapAudience = auth.TokenPlaneData
	testCapTTL      = time.Minute
)

func delegationFixture(t *testing.T) (*capabilityIssuers, limes.SigningKey, *memstore.Store, *limes.Verifier) {
	t.Helper()
	key, err := limes.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store, err := memstore.NewStore(memstore.StoreConfig{})
	if err != nil {
		t.Fatal(err)
	}
	issuers, err := newCapabilityIssuers(key, store, store, testIssuerName, nil, testCapTTL)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := limes.NewVerifier(limes.VerifierConfig{
		Keys: issuers.keys, Revocations: store, TrustedIssuers: issuers.trusted, KeyIssuers: issuers.keyIssuers,
		Biscuit: &limes.BiscuitConfig{Revocations: store},
	})
	if err != nil {
		t.Fatal(err)
	}
	return issuers, key, store, verifier
}

func issueWith(t *testing.T, issuer *limes.Issuer) (string, error) {
	t.Helper()
	tenant := uuid.New()
	res, err := issuer.Issue(t.Context(), limes.IssueRequest{
		Subject:  limes.Principal{Type: limes.PrincipalTypeAgent, TenantID: tenant, Subject: "agent"},
		IssuedBy: auth.DelegationIssuer(),
		Audience: []string{testCapAudience},
		Caveats:  limes.Caveats{Ops: []limes.Op{limes.OpGet}, Requests: limes.RequestCeiling{Unlimited: true}},
		ClientID: "agent",
	})
	return res.Token, err
}

// The planes accept both issuers' capabilities and read which one issued
// each: the delegation issuer's `iss` is what makes a capability act for a
// user.
func TestCapabilityIssuersAreTold(t *testing.T) {
	issuers, _, _, verifier := delegationFixture(t)
	for _, tc := range []struct {
		issuer *limes.Issuer
		want   string
	}{
		{issuers.issuer, testIssuerName},
		{issuers.delegation, auth.DelegationIssuerName(testIssuerName)},
	} {
		token, err := issueWith(t, tc.issuer)
		if err != nil {
			t.Fatal(err)
		}
		v, err := verifier.Verify(t.Context(), limes.VerifyRequest{Token: token, Audience: testCapAudience})
		if err != nil {
			t.Fatalf("%s: Verify: %v", tc.want, err)
		}
		if got := v.Capability().Issuer; got != tc.want {
			t.Errorf("issuer = %q, want %q", got, tc.want)
		}
	}
}

// A capability naming the delegation issuer but signed under the capability
// issuer's kid is refused: nothing but the delegation issuer writes its iss.
func TestDelegationIssuerIsBoundToItsKid(t *testing.T) {
	issuers, key, store, verifier := delegationFixture(t)
	forger, err := newCapabilityIssuer(key.KeyID, key, store, auth.DelegationIssuerName(testIssuerName), testCapTTL)
	if err != nil {
		t.Fatal(err)
	}
	token, err := issueWith(t, forger)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(t.Context(), limes.VerifyRequest{Token: token, Audience: testCapAudience}); !errors.Is(err, limes.ErrInvalidToken) {
		t.Errorf("a delegation iss under the capability kid: err = %v, want a refusal", err)
	}
	if issuers.keyIssuers[key.KeyID] != testIssuerName {
		t.Errorf("the capability kid signs for %q", issuers.keyIssuers[key.KeyID])
	}
}

// usersFixed answers GetByID from a fixed set, or fails.
type usersFixed struct {
	users map[uuid.UUID]authstore.User
	err   error
}

func (u usersFixed) GetByID(_ context.Context, id uuid.UUID) (authstore.User, error) {
	if u.err != nil {
		return authstore.User{}, u.err
	}
	if user, ok := u.users[id]; ok {
		return user, nil
	}
	return authstore.User{}, authstore.ErrNotFound
}

// The plane reads the user as IAM holds them; a user IAM no longer has is
// gone, and a store that fails says so.
func TestDelegatingUsersReadsIAM(t *testing.T) {
	id := uuid.New()
	stored := authstore.User{UserID: id, TenantID: uuid.New(), Roles: []string{"tenant.admin"},
		Scopes: []auth.Scope{{Type: auth.ScopeTenant}}, Disabled: true}
	lookup := delegatingUsers(usersFixed{users: map[uuid.UUID]authstore.User{id: stored}})

	got, err := lookup(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got.TenantID != stored.TenantID || !slices.Equal(got.Roles, stored.Roles) ||
		!slices.Equal(got.Scopes, stored.Scopes) || !got.Disabled {
		t.Errorf("user = %+v, want %+v", got, stored)
	}
	if _, err := lookup(t.Context(), uuid.New()); !errors.Is(err, auth.ErrDelegatingUserGone) {
		t.Errorf("an unknown user: err = %v, want ErrDelegatingUserGone", err)
	}
	down := errors.New("pool closed")
	if _, err := delegatingUsers(usersFixed{err: down})(t.Context(), id); !errors.Is(err, down) || errors.Is(err, auth.ErrDelegatingUserGone) {
		t.Errorf("a failing store: err = %v, want its failure", err)
	}
}

// A token exchange with the capability subsystem off has no issuer to
// delegate through, and refuses to start rather than serve nothing.
func TestTokenExchangeNeedsCapability(t *testing.T) {
	_, err := withDelegationExchange(t.Context(), nil, &SharedDeps{}, map[string]bool{"mcp": true}, usersFixed{})
	if !errors.Is(err, errExchangeNeedsCapability) {
		t.Errorf("err = %v, want errExchangeNeedsCapability", err)
	}
}

// A capability issuer's Biscuit copy, which every capability is also handed
// out as, verifies under the kid binding the delegation issuer brought.
func TestCapabilityBiscuitsVerifyUnderKeyBinding(t *testing.T) {
	issuers, _, _, verifier := delegationFixture(t)
	token, err := issueWith(t, issuers.issuer)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := verifier.Verify(t.Context(), limes.VerifyRequest{Token: token, Audience: testCapAudience})
	if err != nil {
		t.Fatal(err)
	}
	b, err := issuers.issuer.Biscuit(t.Context(), limes.BiscuitRequest{Capability: verified})
	if err != nil {
		t.Fatalf("Biscuit: %v", err)
	}
	if _, err := verifier.Verify(t.Context(), limes.VerifyRequest{Token: b.Token, Audience: testCapAudience}); err != nil {
		t.Errorf("the Biscuit copy is refused: %v", err)
	}
}
