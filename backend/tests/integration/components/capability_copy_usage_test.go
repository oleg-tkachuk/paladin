//go:build integration

package components

import (
	"context"
	"crypto/ed25519"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/jwks"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
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

func newCopyMinter(t *testing.T, records *capstore.Store) *copyMinter {
	t.Helper()
	key, err := limes.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := limes.NewSigner(limes.SignerConfig{KeyID: key.KeyID, Key: key.Private})
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := limes.NewIssuer(limes.IssuerConfig{Signer: signer, Store: records, IssuerName: copyMinterIssuer, DefaultTTL: copyMinterTTL})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := jwks.NewStatic(jwks.StaticConfig{Keys: map[string]ed25519.PublicKey{key.KeyID: key.Public}})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := limes.NewVerifier(limes.VerifierConfig{
		Keys: keys, Revocations: records, TrustedIssuers: []string{copyMinterIssuer},
		AcceptBiscuit: true, BiscuitRevocations: records, MeterCopies: true,
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
		Subject:  limes.Principal{Type: limes.PrincipalAgent, TenantID: tenant, Subject: "agent:copies"},
		IssuedBy: seedIssuer,
		Audience: []string{copyMinterAudience},
		Caveats: limes.Caveats{
			Ops:      []limes.Op{limes.OpGet},
			Requests: lineageRequests,
			Budget:   lineageRootBudget,
			UnitCode: "USD",
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
	token, err := limes.Attenuate(b.Token, a)
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

// copyRow reads one copy's counters as they are stored, past RLS.
func copyRow(t *testing.T, ctx context.Context, f lineageFixture, id []byte) (requests int64, spent, reserved limes.Nanos) {
	t.Helper()
	var s, r pgtype.Numeric
	err := f.pool.QueryRow(ctx,
		`SELECT request_count, spent, reserved FROM capability_copy_usage WHERE revocation_id = $1`,
		id).Scan(&requests, &s, &r)
	if err != nil {
		t.Fatalf("copy %x: %v", id, err)
	}
	return requests, nanosFrom(t, s), nanosFrom(t, r)
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
		_, err := f.usage.Bump(ledgerCtx, limes.BumpRequest{Subject: s})
		return err
	}
	one := m.copyOf(t, ctx, root, limes.Attenuation{Requests: copyRequests(1)})
	if err := bump(one); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := bump(one); !errors.Is(err, limes.ErrRequestLimitExceeded) {
		t.Fatalf("past the copy's limit: err = %v", err)
	}
	if n, _, _ := copyRow(t, ctx, f, copyID(one)); n != 1 {
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

// A copy's budget is held to on charge, returned on refund, and recorded on
// the ledger row so the refund finds it.
func TestCopyBudgetChargeAndRefund(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	m := newCopyMinter(t, f.records)
	root := m.issue(t, ctx, f.tenant)
	f.usage = rlsUsage(t, ctx, f)
	ledgerCtx := auth.WithActingTenant(ctx, f.tenant)
	c := m.copyOf(t, ctx, root, limes.Attenuation{Budget: copyBudget("2")})
	charge := func(amount string) (limes.ChargeReceipt, error) {
		return f.usage.Charge(ledgerCtx, limes.ChargeRequest{Subject: c, Amount: limes.MustParseAmount(amount)}, nil)
	}
	r, err := charge("1.5")
	if err != nil {
		t.Fatalf("charge: %v", err)
	}
	if _, err := charge("1"); !errors.Is(err, limes.ErrBudgetExceeded) {
		t.Fatalf("past the copy's budget: err = %v", err)
	}
	if f.spent(t, ledgerCtx, root.Capability().ID) != limes.MustParseAmount("1.5") {
		t.Fatalf("a refused charge moved the capability")
	}
	var recorded [][]byte
	if err := f.pool.QueryRow(ctx, `SELECT copy_ids FROM charges WHERE id = $1`, r.ChargeID).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 1 || string(recorded[0]) != string(copyID(c)) {
		t.Fatalf("ledger copy_ids = %x, want [%x]", recorded, copyID(c))
	}
	if _, err := f.usage.Refund(ledgerCtx, limes.RefundRequest{ChargeID: r.ChargeID, Remaining: true}); err != nil {
		t.Fatalf("refund: %v", err)
	}
	if _, spent, _ := copyRow(t, ctx, f, copyID(c)); spent != 0 {
		t.Fatalf("copy spend after refund = %v", spent)
	}
	if _, err := charge("2"); err != nil {
		t.Fatalf("after refund: %v", err)
	}
}

// A hold counts against the copy's budget; settle checks and charges it,
// and the expiry sweep returns a lapsed hold to it. Only the copy that made
// a hold settles it: the capability without the copy finds no reservation.
func TestCopyBudgetReservations(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	m := newCopyMinter(t, f.records)
	root := m.issue(t, ctx, f.tenant)
	f.usage = rlsUsage(t, ctx, f)
	ledgerCtx := auth.WithActingTenant(ctx, f.tenant)
	c := m.copyOf(t, ctx, root, limes.Attenuation{Budget: copyBudget("2")})
	reserve := func(amount string, ttl time.Duration) (limes.Reservation, error) {
		return f.usage.Reserve(ledgerCtx, limes.ReserveRequest{Subject: c, Amount: limes.MustParseAmount(amount), TTL: ttl})
	}
	r, err := reserve("1.5", time.Hour)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := reserve("1", time.Hour); !errors.Is(err, limes.ErrBudgetExceeded) {
		t.Fatalf("a hold past the copy's budget: err = %v", err)
	}
	if _, err := f.usage.Settle(ledgerCtx, limes.SettleRequest{Subject: root.MeterSubject(), ReservationID: r.ID, Amount: limes.MustParseAmount("1")}, nil); !errors.Is(err, limes.ErrReservationNotFound) {
		t.Fatalf("settle without the copy that held: err = %v, want ErrReservationNotFound", err)
	}
	if _, err := f.usage.Settle(ledgerCtx, limes.SettleRequest{Subject: c, ReservationID: r.ID, Amount: limes.MustParseAmount("2.5")}, nil); !errors.Is(err, limes.ErrBudgetExceeded) {
		t.Fatalf("settle past the copy's budget: err = %v", err)
	}
	if _, err := f.usage.Settle(ledgerCtx, limes.SettleRequest{Subject: c, ReservationID: r.ID, Amount: limes.MustParseAmount("1.75")}, nil); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if _, spent, reserved := copyRow(t, ctx, f, copyID(c)); spent != limes.MustParseAmount("1.75") || reserved != 0 {
		t.Fatalf("copy after settle: spent %v, reserved %v", spent, reserved)
	}

	if _, err := reserve("0.25", time.Millisecond); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if res, err := f.usage.ReleaseExpired(auth.WithCrossTenantRead(ctx), limes.ReleaseExpiredRequest{}); err != nil || res.Released != 1 {
		t.Fatalf("ReleaseExpired = %d, %v", res.Released, err)
	}
	if _, _, reserved := copyRow(t, ctx, f, copyID(c)); reserved != 0 {
		t.Fatalf("copy still holds %v after the sweep", reserved)
	}
}

// The copy table's own policy: another tenant can neither write a counter
// for this tenant's capability nor read one.
func TestCopyUsageIsTenantScoped(t *testing.T) {
	t.Parallel()
	ctx, f := newLineageFixture(t)
	pool := rlsPool(t, ctx, f.pool)
	other, _ := mkTenant(t, ctx, f.pool, "shared")
	otherCtx := auth.WithActingTenant(ctx, other)

	if _, err := pool.Exec(otherCtx,
		`INSERT INTO capability_copy_usage (revocation_id, capability_id) VALUES ($1, $2)`,
		[]byte("foreign"), f.root); err == nil {
		t.Fatal("another tenant wrote a copy counter for this tenant's capability")
	}

	ownCtx := auth.WithActingTenant(ctx, f.tenant)
	if _, err := pool.Exec(ownCtx,
		`INSERT INTO capability_copy_usage (revocation_id, capability_id) VALUES ($1, $2)`,
		[]byte("own"), f.root); err != nil {
		t.Fatalf("the owning tenant: %v", err)
	}
	var seen int
	if err := pool.QueryRow(otherCtx, `SELECT count(*) FROM capability_copy_usage`).Scan(&seen); err != nil {
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
	if _, err := f.usage.Bump(ledgerCtx, limes.BumpRequest{Subject: c}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.usage.Charge(ledgerCtx, limes.ChargeRequest{Subject: c, Amount: limes.MustParseAmount("0.5")}, nil); err != nil {
		t.Fatal(err)
	}
	req := limes.CopyUsageRequest{RevocationIDs: [][]byte{copyID(c), []byte("never")}}
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
