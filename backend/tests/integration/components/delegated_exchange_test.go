//go:build integration

package components

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/exchange"
	"github.com/oleg-tkachuk/limes/exchange/exchangetest"
	"github.com/oleg-tkachuk/limes/pgstore"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/delegation"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/issuer"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
)

// The IAM issuer the paladin-mcp tokens below come from, the agent they are
// granted to, and the MCP server that exchanges them.
const (
	exchangeSubjectIssuer = "paladin"
	exchangeSubjectKey    = "exchange-test-signing-key-32-bytes-long"
	exchangeAgentClient   = "agent"
	exchangeRelayClient   = "paladin-mcp-server"
	exchangeSubjectTTL    = 10 * time.Minute
)

// exchangeExpiryRounding is how much sooner than its token a capability may
// expire: limes counts the token's remaining life, and the capability's
// expiry, in whole seconds.
const exchangeExpiryRounding = 2 * time.Second

// exchangeUsers is a user directory holding one user.
type exchangeUsers struct{ user authstore.User }

func (u exchangeUsers) GetByID(_ context.Context, id uuid.UUID) (authstore.User, error) {
	if id != u.user.UserID {
		return authstore.User{}, authstore.ErrNotFound
	}
	return u.user, nil
}

// newReplicaExchanger is the exchange one replica of the authorization
// server serves, over store on the role the server runs as, where the token
// endpoint names no tenant: issuance and exchanges are scoped to the
// capability's tenant, as the server wires them.
func newReplicaExchanger(t *testing.T, store *pgstore.Store, signer *limes.Signer, user authstore.User) *exchange.Exchanger {
	t.Helper()
	capIssuer, err := limes.NewIssuer(limes.IssuerConfig{
		Signer: signer, Store: pgstore.ScopedIssuance(store), IssuerName: auth.DelegationIssuerName("paladin-core/test"),
	})
	if err != nil {
		t.Fatal(err)
	}
	subjects := delegation.NewSubjectVerifier(&auth.JWTVerifier{
		Key: []byte(exchangeSubjectKey), ExpectedIssuer: exchangeSubjectIssuer, ExpectedAudience: auth.AudienceMCP,
	}, exchangeSubjectIssuer)
	ex, err := delegation.NewExchanger(capIssuer, subjects, pgstore.ScopedExchanges(store), exchangeUsers{user},
		limes.AbstractUnitCode, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	return ex
}

// A paladin-mcp token is exchanged for one capability whichever replica
// exchanges it, on the application role with no tenant in scope, and that
// capability lives as long as the token.
func TestDelegatedExchangeIssuesOneCapabilityPerToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	store := newCapStore(t, rlsPool(t, ctx, admin))
	user := authstore.User{UserID: uuid.New(), TenantID: tenant}

	key, err := limes.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := limes.NewSigner(limes.SignerConfig{KeyID: key.KeyID, Key: key.Private})
	if err != nil {
		t.Fatal(err)
	}
	one, other := newReplicaExchanger(t, store, signer, user), newReplicaExchanger(t, store, signer, user)
	minter, err := issuer.New(issuer.Config{
		Issuer: exchangeSubjectIssuer, SigningKey: []byte(exchangeSubjectKey),
		AccessTokenTTL: exchangeSubjectTTL, RefreshTokenTTL: time.Hour, ScopedTokenMaxTTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	mint := func(t *testing.T) (string, time.Time) {
		t.Helper()
		tok, exp, err := minter.MintAccess(issuer.AccessClaims{
			Subject: user.UserID.String(), TenantID: tenant, Audience: auth.AudienceMCP,
			OAuthClientID: exchangeAgentClient, OAuthScopes: []string{auth.OAuthScopeRead},
		})
		if err != nil {
			t.Fatal(err)
		}
		return tok, exp
	}
	request := func(token string) exchange.Request {
		return exchange.Request{
			Client:       exchange.Client{ID: exchangeRelayClient, SubjectAudiences: []string{auth.AudienceMCP}},
			SubjectToken: token, Audiences: []string{auth.TokenPlaneData},
		}
	}

	exchangetest.TestOnePerToken(t, exchangetest.OnePerToken{
		Exchanger: one,
		Request: func(t *testing.T) exchange.Request {
			t.Helper()
			tok, _ := mint(t)
			return request(tok)
		},
		Revoke: func(t *testing.T, id uuid.UUID) {
			t.Helper()
			if _, err := store.Revoke(auth.WithActingTenant(ctx, tenant), limes.RevokeRequest{CapabilityID: id}); err != nil {
				t.Fatal(err)
			}
		},
	})

	t.Run("another replica, the same capability for the token's life", func(t *testing.T) {
		tok, exp := mint(t)
		first, err := one.Exchange(ctx, request(tok))
		if err != nil {
			t.Fatalf("first exchange: %v", err)
		}
		again, err := other.Exchange(ctx, request(tok))
		if err != nil {
			t.Fatalf("exchange on another replica: %v", err)
		}
		if again.Capability.ID != first.Capability.ID || !again.Reissued {
			t.Errorf("another replica issued %s (reissued %v), want %s", again.Capability.ID, again.Reissued, first.Capability.ID)
		}
		if got := first.Capability.ExpiresAt; got.After(exp) || exp.Sub(got) > exchangeExpiryRounding {
			t.Errorf("the capability expires %v, want with its token at %v", got, exp)
		}
	})

	t.Run("ending the session revokes its capability", func(t *testing.T) {
		session := uuid.NewV7().String()
		tok, _, err := minter.MintAccess(issuer.AccessClaims{
			Subject: user.UserID.String(), TenantID: tenant, Audience: auth.AudienceMCP,
			OAuthClientID: exchangeAgentClient, OAuthScopes: []string{auth.OAuthScopeRead}, SessionID: session,
		})
		if err != nil {
			t.Fatal(err)
		}
		res, err := one.Exchange(ctx, request(tok))
		if err != nil {
			t.Fatalf("exchange: %v", err)
		}
		acting := auth.WithActingTenant(ctx, tenant)
		if rec, err := store.GetRecord(acting, res.Capability.ID); err != nil || rec.Session != session {
			t.Fatalf("the capability's session = %q (%v), want %q", rec.Session, err, session)
		}
		// On the application role, as the authorization server revokes it.
		ended, err := store.RevokeMatching(acting, limes.RevokeMatchingRequest{TenantID: tenant, Session: session})
		if err != nil || ended.Revoked != 1 {
			t.Fatalf("revoke the session: %+v, %v; want its one capability", ended, err)
		}
		if _, err := other.Exchange(ctx, request(tok)); !errors.Is(err, exchange.ErrInvalidGrant) {
			t.Errorf("the session's token exchanged again: err = %v, want exchange.ErrInvalidGrant", err)
		}
	})

	// Last: it revokes every capability of the client, the ones above too.
	t.Run("deleting the client revokes its capabilities in every tenant", func(t *testing.T) {
		tok, _ := mint(t)
		res, err := one.Exchange(ctx, request(tok))
		if err != nil {
			t.Fatalf("exchange: %v", err)
		}
		// As a platform admin deletes it: acting on the admin's own tenant,
		// which the revocation's every-tenant scope overrides.
		adminCtx := auth.WithActingTenant(ctx, uuid.New())
		bulk := &auth.BulkRevocation{Revoker: store}
		revoked, err := bulk.RevokeMatching(adminCtx, limes.RevokeMatchingRequest{AllTenants: true, ClientID: exchangeAgentClient})
		if err != nil || revoked == 0 {
			t.Fatalf("revoke the client: %d, %v; want its capabilities", revoked, err)
		}
		rec, err := store.GetRecord(auth.WithActingTenant(ctx, tenant), res.Capability.ID)
		if err != nil || rec.Revocation == nil {
			t.Errorf("the client's capability: revocation %v (%v), want revoked", rec.Revocation, err)
		}
	})
}
