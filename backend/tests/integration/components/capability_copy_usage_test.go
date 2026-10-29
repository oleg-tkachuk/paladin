//go:build integration

package components

import (
	"context"
	"crypto/ed25519"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/biscuit"
	"github.com/oleg-tkachuk/limes/jwks"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/limes/pgstore"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
	capstore "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
)

// copyMinterIssuer is the issuer the copy tests' capabilities carry and their
// verifier trusts.
const copyMinterIssuer = "paladin-core/copy-test"

// copyMinterAudience is the audience the copy tests issue and verify for.
const copyMinterAudience = "data"

// copyMinterTTL is how long a capability the copy tests issue lives.
const copyMinterTTL = time.Hour

// copyMinter issues capabilities onto the Postgres store and verifies them,
// the way the server does: a Biscuit copy's limits reach the meter only on a
// subject a verifier made from the copy itself.
type copyMinter struct {
	issuer   *limes.Issuer
	verifier *limes.Verifier
}

func newCopyMinter(t *testing.T, records *pgstore.Store) *copyMinter {
	t.Helper()
	key, err := limes.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := limes.NewSigner(limes.SignerConfig{KeyID: key.KeyID, Key: key.Private})
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := limes.NewIssuer(limes.IssuerConfig{Biscuit: biscuit.Form(), Signer: signer, Store: records, IssuerName: copyMinterIssuer, DefaultTTL: copyMinterTTL})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := jwks.NewStatic(jwks.StaticConfig{Keys: map[string]ed25519.PublicKey{key.KeyID: key.Public()}})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := limes.NewVerifier(limes.VerifierConfig{
		Keys: keys, Revocations: records, TrustedIssuers: []string{copyMinterIssuer},
		Biscuit: &limes.BiscuitConfig{Form: biscuit.Form(), Revocations: records, MeterCopies: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &copyMinter{issuer: issuer, verifier: verifier}
}

// issue puts a root capability of tenant on record with the lineage
// fixture's root ceilings, and verifies it.
func (m *copyMinter) issue(t *testing.T, ctx context.Context, tenant uuid.UUID) limes.VerifiedCapability {
	t.Helper()
	res, err := m.issuer.Issue(ctx, limes.IssueRequest{
		ClientID: captest.ClientID,
		Subject:  limes.Principal{Type: limes.PrincipalTypeAgent, TenantID: tenant, Subject: "agent:copies"},
		IssuedBy: seedIssuer,
		Audience: []string{copyMinterAudience},
		Caveats: limes.Caveats{
			// No share: a Biscuit is sealed, and its copies counted, without it.
			Ops:      []limes.Op{limes.OpGet},
			Requests: lineageRequests,
			Budget:   lineageRootBudget,
			Copies:   limes.CopyQuota{Unlimited: true},
		},
		TTL: copyMinterTTL,
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	return m.verify(t, ctx, res.Token)
}

// copyOf seals v as a Biscuit, narrows the copy by a, and verifies it. Two
// copies of one capability attenuate apart, so each has its own revocation
// id and its own counters.
func (m *copyMinter) copyOf(t *testing.T, ctx context.Context, v limes.VerifiedCapability, a limes.Attenuation) limes.MeterSubject {
	t.Helper()
	b, err := m.issuer.Biscuit(ctx, limes.BiscuitRequest{Capability: v})
	if err != nil {
		t.Fatalf("biscuit: %v", err)
	}
	token, err := biscuit.Attenuate(b.Token, a)
	if err != nil {
		t.Fatalf("attenuate: %v", err)
	}
	s := m.verify(t, ctx, token).MeterSubject()
	if len(s.Copies()) != 1 {
		t.Fatalf("the copy carries %d limits, want 1", len(s.Copies()))
	}
	return s
}

func (m *copyMinter) verify(t *testing.T, ctx context.Context, token string) limes.VerifiedCapability {
	t.Helper()
	v, err := m.verifier.Verify(ctx, limes.VerifyRequest{Token: token, Audience: copyMinterAudience})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	return v
}

// copyID is the revocation id the copy's counters are keyed by.
func copyID(s limes.MeterSubject) []byte { return s.Copies()[0].RevocationID }

// copyRequestCount reads one copy's request counter as it is stored, past RLS.
func copyRequestCount(t *testing.T, ctx context.Context, f lineageFixture, id []byte) int64 {
	t.Helper()
	var n int64
	if err := f.pool.QueryRow(ctx,
		`SELECT request_count FROM limes.copy_usage WHERE revocation_id = $1`, id,
	).Scan(&n); err != nil {
		t.Fatalf("copy %x: %v", id, err)
	}
	return n
}

// copyRequests and copyBudget are the limits an attenuation gives a copy.
func copyRequests(n int64) *limes.RequestCeiling { return &limes.RequestCeiling{Max: n} }
func copyBudget(amount string) *limes.BudgetCeiling {
	return &limes.BudgetCeiling{Max: limes.MustParseAmount(amount)}
}

// Request limits per copy, on the runtime's NOBYPASSRLS connection: a copy
// stops at its own limit, a sibling counts apart, and the capability's limit
// still bounds them together. A copy's limit may not exceed the capability's,
// so the sibling takes the capability's own, which only the two together
// reach.
func TestCopyRequestLimitsUnderRowLevelSecurity(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	m := newCopyMinter(t, f.records)
	root := m.issue(t, ctx, f.tenant)
	f.usage = rlsUsage(t, ctx, f)
	ledgerCtx := auth.WithActingTenant(ctx, f.tenant)
	bump := func(s limes.MeterSubject) error {
		_, err := f.usage.Bump(ledgerCtx, meter.BumpRequest{Subject: s})
		return err
	}
	one := m.copyOf(t, ctx, root, limes.Attenuation{Requests: copyRequests(1)})
	if err := bump(one); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := bump(one); !errors.Is(err, limes.ErrRequestLimitExceeded) {
		t.Fatalf("past the copy's limit: err = %v", err)
	}
	if n := copyRequestCount(t, ctx, f, copyID(one)); n != 1 {
		t.Fatalf("a refused request left the copy at %d", n)
	}
	if u, err := f.usage.GetUsage(ledgerCtx, root.Capability().ID); err != nil || u.RequestCount != 1 {
		t.Fatalf("a refused request moved the capability: %+v, %v", u, err)
	}
	sibling := m.copyOf(t, ctx, root, limes.Attenuation{Requests: copyRequests(lineageRequests.Max)})
	for range 2 {
		if err := bump(sibling); err != nil {
			t.Fatalf("sibling: %v", err)
		}
	}
	if err := bump(sibling); !errors.Is(err, limes.ErrRequestLimitExceeded) {
		t.Fatalf("the capability's limit did not bound its copies together: %v", err)
	}
}

// inScope runs fn in a transaction on pool, scoped as the stores scope
// theirs (capstore.Scope).
func inScope(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if err := capstore.Scope(ctx, tx); err != nil {
			return err
		}
		return fn(tx)
	})
}

// The copy table's own policy, through the scope Paladin puts on a
// transaction: another tenant can neither write a counter for this tenant's
// capability nor read one.
func TestCopyUsageIsTenantScoped(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	pool := rlsPool(t, ctx, f.pool)
	other, _ := mkTenant(t, ctx, f.pool, "shared")
	otherCtx := auth.WithActingTenant(ctx, other)
	insert := func(ctx context.Context, revocationID string) error {
		return inScope(ctx, pool, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx,
				`INSERT INTO limes.copy_usage (revocation_id, capability_id, tenant_id) VALUES ($1, $2, $3)`,
				[]byte(revocationID), f.root, f.tenant)
			return err
		})
	}

	if err := insert(otherCtx, "foreign"); err == nil {
		t.Fatal("another tenant wrote a copy counter for this tenant's capability")
	}
	if err := insert(auth.WithActingTenant(ctx, f.tenant), "own"); err != nil {
		t.Fatalf("the owning tenant: %v", err)
	}
	var seen int
	if err := inScope(otherCtx, pool, func(tx pgx.Tx) error {
		return tx.QueryRow(otherCtx, `SELECT count(*) FROM limes.copy_usage`).Scan(&seen)
	}); err != nil {
		t.Fatal(err)
	}
	if seen != 0 {
		t.Fatalf("another tenant reads %d copy counters", seen)
	}
}

// CopyUsage reads back the counters under the owning tenant, and another
// tenant reads nothing.
func TestCopyUsageIsReadBackPerTenant(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	m := newCopyMinter(t, f.records)
	root := m.issue(t, ctx, f.tenant)
	f.usage = rlsUsage(t, ctx, f)
	ledgerCtx := auth.WithActingTenant(ctx, f.tenant)
	c := m.copyOf(t, ctx, root, limes.Attenuation{Requests: copyRequests(lineageRequests.Max), Budget: copyBudget("2")})
	if _, err := f.usage.Bump(ledgerCtx, meter.BumpRequest{Subject: c}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.usage.Charge(ledgerCtx, meter.ChargeRequest{Subject: c, Amount: limes.MustParseAmount("0.5")}, nil); err != nil {
		t.Fatal(err)
	}
	req := meter.CopyUsageRequest{CapabilityID: root.Capability().ID, RevocationIDs: [][]byte{copyID(c), []byte("never")}}
	res, err := f.usage.CopyUsage(ledgerCtx, req)
	if err != nil {
		t.Fatal(err)
	}
	got := res.Copies
	if len(got) != 1 || string(got[0].RevocationID) != string(copyID(c)) || got[0].CapabilityID != root.Capability().ID ||
		got[0].RequestCount != 1 || got[0].SpentAmount != limes.MustParseAmount("0.5") || got[0].ReservedAmount != 0 {
		t.Fatalf("CopyUsage = %+v", got)
	}
	other, _ := mkTenant(t, ctx, f.pool, "shared")
	if res, err := f.usage.CopyUsage(auth.WithActingTenant(ctx, other), req); err != nil || len(res.Copies) != 0 {
		t.Fatalf("another tenant read %+v, %v", res.Copies, err)
	}
}
