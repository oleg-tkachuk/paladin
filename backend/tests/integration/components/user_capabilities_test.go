//go:build integration

package components

import (
	"context"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/exchange"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/iam/v1/userh"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/issuer"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	capstore "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// tenantUsers is a user directory of the users it holds; the handler reads
// nothing else of it.
type tenantUsers struct {
	authstore.UserRepository
	users map[uuid.UUID]authstore.User
}

func (u tenantUsers) GetByID(_ context.Context, id uuid.UUID) (authstore.User, error) {
	if user, ok := u.users[id]; ok {
		return user, nil
	}
	return authstore.User{}, authstore.ErrNotFound
}

// A user lists the capabilities exchanged on their behalf — the format the
// exchange writes on_behalf_of in is the one the listing filters by — and
// not a teammate's, over the built-in policy alone.
func TestListUserCapabilitiesListsWhatActsForTheUser(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	pool := rlsPool(t, ctx, admin)
	store := newCapStore(t, pool)
	census, err := capstore.NewCensus(pool)
	if err != nil {
		t.Fatal(err)
	}
	alice := authstore.User{UserID: uuid.New(), TenantID: tenant, Subject: "alice"}
	bob := authstore.User{UserID: uuid.New(), TenantID: tenant, Subject: "bob"}

	key, err := limes.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := limes.NewSigner(limes.SignerConfig{KeyID: key.KeyID, Key: key.Private})
	if err != nil {
		t.Fatal(err)
	}
	minter, err := issuer.New(issuer.Config{
		Issuer: exchangeSubjectIssuer, SigningKey: []byte(exchangeSubjectKey),
		AccessTokenTTL: exchangeSubjectTTL, RefreshTokenTTL: time.Hour, ScopedTokenMaxTTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	exchangeFor := func(t *testing.T, user authstore.User) uuid.UUID {
		t.Helper()
		tok, _, err := minter.MintAccess(issuer.AccessClaims{
			Subject: user.UserID.String(), TenantID: tenant, Audience: auth.AudienceMCP,
			OAuthClientID: exchangeAgentClient, OAuthScopes: []string{auth.OAuthScopeRead},
		})
		if err != nil {
			t.Fatal(err)
		}
		res, err := newReplicaExchanger(t, store, signer, user).Exchange(ctx, exchangeRequest(tok))
		if err != nil {
			t.Fatalf("exchange for %s: %v", user.Subject, err)
		}
		return res.Capability.ID
	}
	alicesCap := exchangeFor(t, alice)
	exchangeFor(t, bob)

	h := userh.NewHandler(tenantUsers{users: map[uuid.UUID]authstore.User{alice.UserID: alice, bob.UserID: bob}},
		cedar.NewEngine(cedar.NewPostgresStore(admin), time.Minute)).WithCapabilities(census)
	asAlice := auth.WithPrincipal(ctx, &auth.Principal{
		Subject: alice.UserID.String(), TenantID: tenant, Kind: auth.PrincipalKindUser, Roles: []string{"tenant.user"},
	})
	list := func(t *testing.T, of authstore.User, dispositions ...limes.RecordDisposition) ([]limes.RecordListing, error) {
		t.Helper()
		page, err := h.ListUserCapabilities(asAlice, userh.UserRef{TenantID: of.TenantID, UserID: of.UserID},
			userh.UserCapabilitiesPage{Dispositions: dispositions})
		return page.Items, err
	}

	got, err := list(t, alice)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Record.Capability.ID != alicesCap {
		t.Fatalf("alice's capabilities = %d, want her one %s", len(got), alicesCap)
	}
	if got[0].Record.Capability.ClientID != exchangeAgentClient {
		t.Errorf("client = %q, want %q", got[0].Record.Capability.ClientID, exchangeAgentClient)
	}
	if _, err := list(t, bob); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a teammate's: code = %v, want PermissionDenied", connect.CodeOf(err))
	}

	if _, err := store.Revoke(auth.WithActingTenant(ctx, tenant), limes.RevokeRequest{CapabilityID: alicesCap}); err != nil {
		t.Fatal(err)
	}
	if active, err := list(t, alice); err != nil || len(active) != 0 {
		t.Errorf("active after revoking: %d (%v), want none", len(active), err)
	}
	if revoked, err := list(t, alice, limes.RecordDispositionRevoked); err != nil || len(revoked) != 1 {
		t.Errorf("revoked: %d (%v), want hers", len(revoked), err)
	}
}

// exchangeRequest is the MCP server exchanging token for a data-plane
// capability.
func exchangeRequest(token string) exchange.Request {
	return exchange.Request{
		Client:       exchange.Client{ID: exchangeRelayClient, SubjectAudiences: []string{auth.AudienceMCP}},
		SubjectToken: token, Audiences: []string{auth.TokenPlaneData},
	}
}
