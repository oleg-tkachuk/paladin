//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/biscuit"

	"github.com/oleg-tkachuk/limes/pgstore"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// delegatedTTL is how long the capabilities issued here live.
const delegatedTTL = time.Minute

// The MCP exchange issues at the token endpoint, where nothing names a
// tenant (ADR-0041). Under the role the server runs as, RLS refuses the
// capability row there, and pgstore.ScopedIssuance writes it under the
// capability's own tenant.
func TestDelegatedIssuanceIsWrittenUnderItsTenant(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	store := newCapStore(t, rlsPool(t, ctx, admin))

	key, err := limes.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := limes.NewSigner(limes.SignerConfig{KeyID: key.KeyID, Key: key.Private})
	if err != nil {
		t.Fatal(err)
	}
	issue := func(s limes.IssuanceStore) error {
		issuer, err := limes.NewIssuer(limes.IssuerConfig{
			Biscuit: biscuit.Form(),
			Signer:  signer, Store: s, IssuerName: auth.DelegationIssuerName("paladin-core/test"), DefaultTTL: delegatedTTL,
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = issuer.Issue(ctx, limes.IssueRequest{
			Subject:  limes.Principal{Type: limes.PrincipalTypeAgent, TenantID: tenant, Subject: "agent"},
			IssuedBy: auth.DelegationIssuer(),
			Audience: []string{auth.TokenPlaneData},
			Caveats:  limes.Caveats{Ops: []limes.Op{limes.OpGet}, Requests: limes.RequestCeiling{Unlimited: true}},
			ClientID: "agent",
		})
		return err
	}

	// Refused by the database, not by limes: the request itself is sound.
	if err := issue(store); err == nil || errors.Is(err, limes.ErrInvalidRequest) {
		t.Errorf("issued with no tenant in scope: err = %v, want the store's refusal", err)
	}
	if err := issue(pgstore.ScopedIssuance(store)); err != nil {
		t.Errorf("a capability issued under its own tenant: %v", err)
	}
}
