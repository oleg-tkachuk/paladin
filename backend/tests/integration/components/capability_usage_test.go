//go:build integration

// The capability usage store is the money path: it enforces per-capability
// request and spend caps, the tenant-wide budget, and writes the charges
// ledger. It had no test against a real database.
//
// The behaviour that most needs pinning is not the happy path but the
// rollback: Charge runs the capability counter, the tenant counter, the
// ledger insert and the outbox fan-out on one transaction, and a rejection
// anywhere must leave all four untouched. A partial charge is worse than a
// failed one — it bills a tenant for work that never happened.
package components

import (
	"bytes"
	"context"
	"errors"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/limes"
	capstore "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgmoney"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// usageFixture seeds a tenant and one capability, and returns a UsageStore
// wired the way production wires it (real pool ⇒ ledger + fan-out active).
// subject is the capability as the meter counts it, read from its record.
type usageFixture struct {
	pool    *pgxpool.Pool
	records *capstore.Store
	usage   *capstore.UsageStore
	tenant  uuid.UUID
	capID   uuid.UUID
	subject limes.MeterSubject
}

// unlimitedCeilings lifts both of a capability's ceilings, so only the
// tenant's, an ancestor's or a copy's can bind.
func unlimitedCeilings(c *limes.Caveats) {
	c.Requests = limes.RequestCeiling{Unlimited: true}
	c.Budget = limes.BudgetCeiling{Unlimited: true}
}

// insertMeteredCap puts a capability of tenant on record — unlimited, then
// changed by edit when it is not nil — and returns its id and the subject the
// meter counts it as.
func insertMeteredCap(t *testing.T, ctx context.Context, records *capstore.Store, tenant uuid.UUID, edit func(*limes.Caveats)) (uuid.UUID, limes.MeterSubject) {
	t.Helper()
	c := mkCap(tenant, "user:spender", time.Now().Add(time.Hour))
	unlimitedCeilings(&c.Caveats)
	if edit != nil {
		edit(&c.Caveats)
	}
	if err := records.Insert(ctx, limes.InsertRequest{Capability: c, IssuedBy: seedIssuer}); err != nil {
		t.Fatalf("seed capability: %v", err)
	}
	return c.ID, storedSubject(t, ctx, records, c.ID)
}

// storedSubject is the meter subject of a capability on record.
func storedSubject(t *testing.T, ctx context.Context, records *capstore.Store, id uuid.UUID) limes.MeterSubject {
	t.Helper()
	s, err := limes.StoredMeterSubject(ctx, records, id)
	if err != nil {
		t.Fatalf("meter subject of %s: %v", id, err)
	}
	return s
}

// newUsageFixture seeds a capability with no ceilings of its own.
func newUsageFixture(t *testing.T) (context.Context, usageFixture) {
	t.Helper()
	return newUsageFixtureWith(t, nil)
}

// newUsageFixtureWith seeds a capability whose caveats edit changes from
// unlimited.
func newUsageFixtureWith(t *testing.T, edit func(*limes.Caveats)) (context.Context, usageFixture) {
	t.Helper()
	ctx := context.Background()
	pool := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, pool, "shared")
	records := newCapStore(t, pool)
	capID, subject := insertMeteredCap(t, ctx, records, tenant, edit)
	return ctx, usageFixture{
		pool:    pool,
		records: records,
		usage:   capstore.NewUsageStore(sqlc.New(pool), pool, zap.NewNop()),
		tenant:  tenant,
		capID:   capID,
		subject: subject,
	}
}

// spentOn reads the per-capability counter straight from the table, so the
// assertion does not depend on the same code path it is checking.
func (f usageFixture) spentOn(t *testing.T, ctx context.Context) limes.Nanos {
	t.Helper()
	var spent pgtype.Numeric
	err := f.pool.QueryRow(ctx,
		`SELECT COALESCE(spent_usd, 0) FROM capability_usage WHERE capability_id = $1`, f.capID,
	).Scan(&spent)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0
	}
	if err != nil {
		t.Fatalf("read capability spend: %v", err)
	}
	return nanosFrom(t, spent)
}

func (f usageFixture) tenantSpent(t *testing.T, ctx context.Context) limes.Nanos {
	t.Helper()
	var spent pgtype.Numeric
	err := f.pool.QueryRow(ctx,
		`SELECT COALESCE(spent_usd, 0) FROM tenant_budgets WHERE tenant_id = $1`, f.tenant,
	).Scan(&spent)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0
	}
	if err != nil {
		t.Fatalf("read tenant spend: %v", err)
	}
	return nanosFrom(t, spent)
}

func (f usageFixture) ledgerRows(t *testing.T, ctx context.Context) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(ctx,
		`SELECT count(*) FROM charges WHERE capability_id = $1`, f.capID).Scan(&n); err != nil {
		t.Fatalf("count charges: %v", err)
	}
	return n
}

// nanosFrom reads a money column, scanned as numeric, exactly.
func nanosFrom(t *testing.T, v pgtype.Numeric) limes.Nanos {
	t.Helper()
	n, err := pgmoney.NanosFromNumeric(v)
	if err != nil {
		t.Fatalf("read money column: %v", err)
	}
	return n
}

func closeEnough(got, want float64) bool { return math.Abs(got-want) < 1e-9 }

// TestBumpRequestEnforcesCap pins the request counter: it counts up, it
// refuses the call that would cross the cap, and a refused call does not
// consume quota it then reports as spent.
func TestBumpRequestEnforcesCap(t *testing.T) {
	t.Parallel()
	const maxRequests = 3
	ctx, f := newUsageFixtureWith(t, func(c *limes.Caveats) { c.Requests = limes.RequestCeiling{Max: maxRequests} })

	for want := int64(1); want <= maxRequests; want++ {
		got, err := f.usage.Bump(ctx, limes.BumpRequest{Subject: f.subject})
		if err != nil {
			t.Fatalf("bump %d: %v", want, err)
		}
		if got.RequestCount != want {
			t.Fatalf("bump %d returned count %d", want, got.RequestCount)
		}
	}

	if _, err := f.usage.Bump(ctx, limes.BumpRequest{Subject: f.subject}); !errors.Is(err, limes.ErrRequestLimitExceeded) {
		t.Fatalf("fourth bump: want ErrRequestLimitExceeded, got %v", err)
	}

	// The refused bump must not have incremented anything.
	got, err := f.usage.GetUsage(ctx, f.capID)
	if err != nil {
		t.Fatalf("get usage: %v", err)
	}
	if got.RequestCount != maxRequests {
		t.Errorf("request_count is %d after a refused bump, want %d", got.RequestCount, maxRequests)
	}
}

// TestBumpRequestUnlimited pins the two senses a ceiling can have: only an
// explicit Unlimited lifts it, and a ceiling of 0 allows nothing. The zero
// value used to mean unlimited, so a capability whose ceiling was lost on the
// way to the store was never bounded; it now refuses the first request.
func TestBumpRequestUnlimited(t *testing.T) {
	t.Parallel()
	const unlimitedBumps = 5
	ctx, f := newUsageFixture(t)
	for i := 0; i < unlimitedBumps; i++ {
		if _, err := f.usage.Bump(ctx, limes.BumpRequest{Subject: f.subject}); err != nil {
			t.Fatalf("unlimited bump %d: %v", i, err)
		}
	}

	_, frozen := insertMeteredCap(t, ctx, f.records, f.tenant, func(c *limes.Caveats) { c.Requests = limes.RequestCeiling{} })
	if _, err := f.usage.Bump(ctx, limes.BumpRequest{Subject: frozen}); !errors.Is(err, limes.ErrRequestLimitExceeded) {
		t.Fatalf("bump under a ceiling of 0: err = %v, want ErrRequestLimitExceeded", err)
	}
	if u, err := f.usage.GetUsage(ctx, frozen.CapabilityID()); err == nil && u.RequestCount != 0 {
		t.Errorf("a refused bump counted %d requests", u.RequestCount)
	}
}

// TestChargeWritesLedgerAtomically pins the happy path across all three
// writes: the capability counter, the tenant aggregate, and the ledger row
// that the billing surface reads.
func TestChargeWritesLedgerAtomically(t *testing.T) {
	t.Parallel()
	ctx, f := newUsageFixture(t)

	spent, err := f.usage.Charge(ctx, limes.ChargeRequest{Subject: f.subject, Amount: limes.MustParseAmount("2.50"), Op: "objects.put", Actor: "user:alice"}, nil)
	if err != nil {
		t.Fatalf("charge: %v", err)
	}
	if spent.SpentAmount != limes.MustParseAmount("2.50") {
		t.Errorf("returned spend %v, want 2.50", spent.SpentAmount)
	}
	if spent.ChargeID == uuid.Nil {
		t.Error("receipt carries no charge ID")
	}
	if got := f.spentOn(t, ctx); got != limes.MustParseAmount("2.50") {
		t.Errorf("capability counter %v, want 2.50", got)
	}
	if got := f.tenantSpent(t, ctx); got != limes.MustParseAmount("2.50") {
		t.Errorf("tenant counter %v, want 2.50", got)
	}
	if n := f.ledgerRows(t, ctx); n != 1 {
		t.Fatalf("ledger has %d rows, want 1", n)
	}

	// The ledger captures the tenant slug at charge time so a later rename
	// cannot rewrite settled history.
	var slug, op, actor, unit string
	if err := f.pool.QueryRow(ctx,
		`SELECT tenant_slug, op, actor_subject, unit_code FROM charges WHERE capability_id = $1`, f.capID,
	).Scan(&slug, &op, &actor, &unit); err != nil {
		t.Fatalf("read ledger row: %v", err)
	}
	if slug == "" {
		t.Error("ledger row has empty tenant_slug")
	}
	if op != "objects.put" || actor != "user:alice" || unit != "USD" {
		t.Errorf("ledger row: op=%q actor=%q unit=%q", op, actor, unit)
	}
}

// TestChargeRejectsNegative pins that refunds cannot be smuggled in as a
// negative charge, which would bypass the floor-at-zero rule the explicit
// refund paths enforce.
func TestChargeRejectsNegative(t *testing.T) {
	t.Parallel()
	ctx, f := newUsageFixture(t)
	if _, err := f.usage.Charge(ctx, limes.ChargeRequest{Subject: f.subject, Amount: -limes.NanosPerUnit, Op: "op", Actor: "actor"}, nil); err == nil {
		t.Fatal("negative charge accepted")
	}
	if n := f.ledgerRows(t, ctx); n != 0 {
		t.Errorf("rejected charge wrote %d ledger rows", n)
	}
}

// TestChargeCapExceededLeavesNothingBehind pins the per-capability cap and,
// more importantly, that crossing it writes nothing at all.
func TestChargeCapExceededLeavesNothingBehind(t *testing.T) {
	t.Parallel()
	ctx, f := newUsageFixtureWith(t, func(c *limes.Caveats) { c.Budget = limes.BudgetCeiling{Max: limes.MustParseAmount("10")} })

	if _, err := f.usage.Charge(ctx, limes.ChargeRequest{Subject: f.subject, Amount: limes.MustParseAmount("8"), Op: "op", Actor: "actor"}, nil); err != nil {
		t.Fatalf("first charge: %v", err)
	}
	if _, err := f.usage.Charge(ctx, limes.ChargeRequest{Subject: f.subject, Amount: limes.MustParseAmount("5"), Op: "op", Actor: "actor"}, nil); !errors.Is(err, limes.ErrBudgetExceeded) {
		t.Fatalf("over-cap charge: want ErrBudgetExceeded, got %v", err)
	}

	if got := f.spentOn(t, ctx); got != limes.MustParseAmount("8") {
		t.Errorf("capability counter moved to %v on a refused charge, want 8", got)
	}
	if got := f.tenantSpent(t, ctx); got != limes.MustParseAmount("8") {
		t.Errorf("tenant counter moved to %v on a refused charge, want 8", got)
	}
	if n := f.ledgerRows(t, ctx); n != 1 {
		t.Errorf("ledger has %d rows, want 1 — the refused charge should not appear", n)
	}
}

// TestChargeTenantBudgetExceededRollsBackCapability is the rollback that the
// single-transaction design exists to guarantee: the capability counter is
// bumped first, then the tenant cap rejects, and the capability bump must not
// survive. Before the two shared a transaction this needed a compensating
// refund, which is exactly the kind of thing that drifts.
func TestChargeTenantBudgetExceededRollsBackCapability(t *testing.T) {
	t.Parallel()
	ctx, f := newUsageFixture(t)

	if _, err := f.usage.SetTenantBudget(ctx, limes.SetTenantBudgetRequest{
		TenantID: f.tenant, Ceiling: limes.BudgetCeiling{Max: limes.MustParseAmount("10")}, UnitCode: "USD",
	}); err != nil {
		t.Fatalf("set tenant budget: %v", err)
	}

	if _, err := f.usage.Charge(ctx, limes.ChargeRequest{Subject: f.subject, Amount: limes.MustParseAmount("25"), Op: "op", Actor: "actor"}, nil); !errors.Is(err, limes.ErrTenantBudgetExceeded) {
		t.Fatalf("want ErrTenantBudgetExceeded, got %v", err)
	}

	if got := f.spentOn(t, ctx); got != 0 {
		t.Errorf("capability counter is %v after a tenant-budget rejection, want 0 — the transaction did not roll back", got)
	}
	if got := f.tenantSpent(t, ctx); got != 0 {
		t.Errorf("tenant counter is %v after its own rejection, want 0", got)
	}
	if n := f.ledgerRows(t, ctx); n != 0 {
		t.Errorf("ledger has %d rows after a rejected charge", n)
	}
}

// TestChargeFanOutFailureRollsBackEverything pins ADR-0003's guarantee from
// the other side: if the outbox enqueue fails, the spend it describes must
// not commit either.
func TestChargeFanOutFailureRollsBackEverything(t *testing.T) {
	t.Parallel()
	ctx, f := newUsageFixture(t)

	boom := errors.New("outbox unavailable")
	_, err := f.usage.Charge(ctx, limes.ChargeRequest{Subject: f.subject, Amount: limes.MustParseAmount("3"), Op: "op", Actor: "actor"}, func(context.Context, pgx.Tx) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("want the fan-out error, got %v", err)
	}

	if got := f.spentOn(t, ctx); got != 0 {
		t.Errorf("capability counter is %v after a fan-out failure, want 0", got)
	}
	if got := f.tenantSpent(t, ctx); got != 0 {
		t.Errorf("tenant counter is %v after a fan-out failure, want 0", got)
	}
	if n := f.ledgerRows(t, ctx); n != 0 {
		t.Errorf("ledger has %d rows after a fan-out failure", n)
	}
}

// TestChargeFanOutRunsOnTheChargeTransaction pins that onCharged is handed the
// same tx as the counters, not a fresh connection — otherwise the outbox rows
// would commit independently and ADR-0003's atomicity would be nominal only.
func TestChargeFanOutRunsOnTheChargeTransaction(t *testing.T) {
	t.Parallel()
	ctx, f := newUsageFixture(t)

	var sawSpend pgtype.Numeric
	_, err := f.usage.Charge(ctx, limes.ChargeRequest{Subject: f.subject, Amount: limes.MustParseAmount("4"), Op: "op", Actor: "actor"}, func(c context.Context, tx pgx.Tx) error {
		// Uncommitted at this point: only the charge's own transaction
		// can see the row.
		return tx.QueryRow(c,
			`SELECT spent_usd FROM capability_usage WHERE capability_id = $1`, f.capID,
		).Scan(&sawSpend)
	})
	if err != nil {
		t.Fatalf("charge: %v", err)
	}
	if got := nanosFrom(t, sawSpend); got != limes.MustParseAmount("4") {
		t.Errorf("fan-out saw spend %v, want 4 — it is not running on the charge transaction", got)
	}
}

// TestChargeRequiresTenant pins that a charge cannot skip the tenant: the
// ledger row and the tenant ceiling both need one, and a tenant-less path
// used to move the capability counter while billing nothing. A subject read
// from a record or a token always names its tenant; the zero subject, which
// names none, is refused before anything is counted.
func TestChargeRequiresTenant(t *testing.T) {
	t.Parallel()
	ctx, f := newUsageFixture(t)

	if _, err := f.usage.Charge(ctx, limes.ChargeRequest{Subject: limes.MeterSubject{}, Amount: limes.MustParseAmount("1.25")}, nil); !errors.Is(err, limes.ErrInvalidRequest) {
		t.Fatalf("tenant-less charge: err = %v, want ErrInvalidRequest", err)
	}
	if got := f.spentOn(t, ctx); got != 0 {
		t.Errorf("capability counter %v after a refused charge, want 0", got)
	}
	if n := f.ledgerRows(t, ctx); n != 0 {
		t.Errorf("ledger has %d rows after a refused charge, want 0", n)
	}
}

// TestChargeRejectsUnknownUnitCode pins that the currency is validated before
// anything is written — a ledger denominated in a unit nothing can convert is
// unusable for invoicing. The unit is the capability's own, so the record
// carries the bad one here.
func TestChargeRejectsUnknownUnitCode(t *testing.T) {
	t.Parallel()
	const unknownUnit = "XYZ"
	ctx, f := newUsageFixture(t)
	capID, subject := insertMeteredCap(t, ctx, f.records, f.tenant, func(c *limes.Caveats) { c.UnitCode = unknownUnit })
	if _, err := f.usage.Charge(ctx, limes.ChargeRequest{Subject: subject, Amount: limes.MustParseAmount("1"), Op: "op", Actor: "actor"}, nil); err == nil {
		t.Fatal("unknown unit code accepted")
	}
	var n int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM charges WHERE capability_id = $1`, capID).Scan(&n); err != nil {
		t.Fatalf("count charges: %v", err)
	}
	if n != 0 {
		t.Errorf("a refused charge wrote %d ledger rows", n)
	}
}

// TestRefundIsBoundToItsCharge pins refunds against a real ledger: a refund
// returns spend from one charge to the capability and the tenant, is recorded
// against that charge, and can never return more than the charge took — a
// repeated "refund the rest" is a no-op rather than a second credit.
func TestRefundIsBoundToItsCharge(t *testing.T) {
	t.Parallel()
	ctx, f := newUsageFixture(t)

	receipt, err := f.usage.Charge(ctx, limes.ChargeRequest{Subject: f.subject, Amount: limes.MustParseAmount("5"), Op: "op", Actor: "actor"}, nil)
	if err != nil {
		t.Fatalf("charge: %v", err)
	}

	if got, err := f.usage.Refund(ctx, limes.RefundRequest{ChargeID: receipt.ChargeID, Amount: limes.MustParseAmount("2")}); err != nil || got.Amount != limes.MustParseAmount("2") {
		t.Fatalf("partial refund = %v, %v; want 2", got, err)
	}
	if got := f.spentOn(t, ctx); got != limes.MustParseAmount("3") {
		t.Errorf("capability counter %v after 5−2, want 3", got)
	}
	if got := f.tenantSpent(t, ctx); got != limes.MustParseAmount("3") {
		t.Errorf("tenant counter %v after 5−2, want 3", got)
	}

	if _, err := f.usage.Refund(ctx, limes.RefundRequest{ChargeID: receipt.ChargeID, Amount: limes.MustParseAmount("99")}); !errors.Is(err, limes.ErrRefundExceedsCharge) {
		t.Fatalf("over-refund err = %v, want ErrRefundExceedsCharge", err)
	}
	// A refund names an amount or the rest, never neither.
	if _, err := f.usage.Refund(ctx, limes.RefundRequest{ChargeID: receipt.ChargeID}); !errors.Is(err, limes.ErrInvalidRequest) {
		t.Fatalf("refund naming no amount: err = %v, want ErrInvalidRequest", err)
	}
	if got, err := f.usage.Refund(ctx, limes.RefundRequest{ChargeID: receipt.ChargeID, Remaining: true}); err != nil || got.Amount != limes.MustParseAmount("3") {
		t.Fatalf("refund of the rest = %v, %v; want 3", got, err)
	}
	if got, err := f.usage.Refund(ctx, limes.RefundRequest{ChargeID: receipt.ChargeID, Remaining: true}); err != nil || got.Amount != 0 {
		t.Fatalf("repeated full refund = %v, %v; want 0, nil", got, err)
	}
	if got := f.spentOn(t, ctx); got != 0 {
		t.Errorf("capability counter %v after full refund, want 0", got)
	}
	if got := f.tenantSpent(t, ctx); got != 0 {
		t.Errorf("tenant counter %v after full refund, want 0", got)
	}

	var refunds int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM charge_refunds WHERE charge_id = $1`, receipt.ChargeID).Scan(&refunds); err != nil {
		t.Fatalf("count refunds: %v", err)
	}
	if refunds != 2 {
		t.Errorf("charge_refunds has %d rows, want 2 (the no-op refund records nothing)", refunds)
	}

	if _, err := f.usage.Refund(ctx, limes.RefundRequest{ChargeID: uuid.New(), Remaining: true}); !errors.Is(err, limes.ErrChargeNotFound) {
		t.Errorf("unknown charge err = %v, want ErrChargeNotFound", err)
	}
	if _, err := f.usage.Refund(ctx, limes.RefundRequest{ChargeID: receipt.ChargeID, Amount: -limes.NanosPerUnit}); !errors.Is(err, limes.ErrInvalidAmount) {
		t.Errorf("negative refund err = %v, want ErrInvalidAmount", err)
	}
}

// TestTenantBudgetLifecycle pins Set/Get, including the two options operators
// actually use: a mid-cycle cap change that preserves spend, and a monthly
// close that resets it.
func TestTenantBudgetLifecycle(t *testing.T) {
	t.Parallel()
	const budgetUnit = "EUR"
	ctx, f := newUsageFixtureWith(t, func(c *limes.Caveats) { c.UnitCode = budgetUnit })

	if _, err := f.usage.GetTenantBudget(ctx, f.tenant); !errors.Is(err, limes.ErrTenantBudgetNotFound) {
		t.Fatalf("unset budget: want ErrTenantBudgetNotFound, got %v", err)
	}

	end := time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Microsecond)
	if _, err := f.usage.SetTenantBudget(ctx, limes.SetTenantBudgetRequest{
		TenantID: f.tenant, Ceiling: limes.BudgetCeiling{Max: limes.MustParseAmount("100")}, UnitCode: budgetUnit, PeriodEnd: end,
	}); err != nil {
		t.Fatalf("set: %v", err)
	}

	got, err := f.usage.GetTenantBudget(ctx, f.tenant)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Ceiling != (limes.BudgetCeiling{Max: limes.MustParseAmount("100")}) || got.UnitCode != budgetUnit {
		t.Errorf("budget: %+v", got)
	}
	if !got.PeriodEnd.Equal(end) {
		t.Errorf("period_end: got %v want %v", got.PeriodEnd, end)
	}

	if _, err := f.usage.Charge(ctx, limes.ChargeRequest{Subject: f.subject, Amount: limes.MustParseAmount("40"), Op: "op", Actor: "actor"}, nil); err != nil {
		t.Fatalf("charge: %v", err)
	}

	t.Run("mid-cycle cap change preserves spend and currency", func(t *testing.T) {
		cur, err := f.usage.GetTenantBudget(ctx, f.tenant)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if _, err := f.usage.SetTenantBudget(ctx, limes.SetTenantBudgetRequest{
			TenantID: f.tenant, Ceiling: limes.BudgetCeiling{Max: limes.MustParseAmount("200")}, ExpectedVersion: cur.ResourceVersion,
		}); err != nil {
			t.Fatalf("set: %v", err)
		}
		got, err := f.usage.GetTenantBudget(ctx, f.tenant)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.SpentAmount != limes.MustParseAmount("40") {
			t.Errorf("spend reset by a cap change: got %v want 40", got.SpentAmount)
		}
		if got.UnitCode != budgetUnit {
			t.Errorf("empty unit_code re-denominated the budget to %q", got.UnitCode)
		}
	})

	t.Run("reset_spend closes the period", func(t *testing.T) {
		cur, err := f.usage.GetTenantBudget(ctx, f.tenant)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if _, err := f.usage.SetTenantBudget(ctx, limes.SetTenantBudgetRequest{
			TenantID: f.tenant, Ceiling: limes.BudgetCeiling{Max: limes.MustParseAmount("200")}, ResetSpend: true, ExpectedVersion: cur.ResourceVersion,
		}); err != nil {
			t.Fatalf("set: %v", err)
		}
		got, err := f.usage.GetTenantBudget(ctx, f.tenant)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.SpentAmount != 0 {
			t.Errorf("spend %v after reset, want 0", got.SpentAmount)
		}
	})

	t.Run("invalid unit_code is refused", func(t *testing.T) {
		if _, err := f.usage.SetTenantBudget(ctx, limes.SetTenantBudgetRequest{
			TenantID: f.tenant, Ceiling: limes.BudgetCeiling{Max: limes.MustParseAmount("1")}, UnitCode: "XYZ",
		}); err == nil {
			t.Error("invalid unit_code accepted")
		}
	})
}

// TestListTenantBudgets pins the dashboard query's filters. Getting the
// threshold predicate wrong shows operators an empty alert list over tenants
// that are actually near their cap. A ceiling of 0 allows nothing, so it is
// listed with the tenants at or past any threshold; only an explicit
// Unlimited ceiling is listed as uncapped. Which tenants are in the trash is
// no longer the store's to know: the admin server drops them
// (connectshim/admin.TenantBudgetServer).
func TestListTenantBudgets(t *testing.T) {
	t.Parallel()
	const thresholdPct = 80
	const nearCapPct = 90
	ctx, f := newUsageFixture(t)

	nearCap := f.tenant // will sit at 90%
	unlimited, _ := mkTenant(t, ctx, f.pool, "shared")
	lowUse, _ := mkTenant(t, ctx, f.pool, "shared")
	frozen, _ := mkTenant(t, ctx, f.pool, "shared")

	hundred := limes.BudgetCeiling{Max: limes.MustParseAmount("100")}
	for tenant, ceiling := range map[uuid.UUID]limes.BudgetCeiling{
		nearCap:   hundred,
		unlimited: {Unlimited: true},
		lowUse:    hundred,
		frozen:    {},
	} {
		if _, err := f.usage.SetTenantBudget(ctx, limes.SetTenantBudgetRequest{TenantID: tenant, Ceiling: ceiling}); err != nil {
			t.Fatalf("set %s: %v", ceiling, err)
		}
	}
	if _, err := f.usage.Charge(ctx, limes.ChargeRequest{Subject: f.subject, Amount: limes.MustParseAmount("90"), Op: "op", Actor: "actor"}, nil); err != nil {
		t.Fatalf("charge near-cap: %v", err)
	}

	has := func(rows []limes.TenantBudget, id uuid.UUID) bool {
		for _, r := range rows {
			if r.TenantID == id {
				return true
			}
		}
		return false
	}

	t.Run("threshold selects tenants at or above it", func(t *testing.T) {
		page, err := f.usage.ListTenantBudgets(ctx, limes.ListTenantBudgetsRequest{Filter: limes.TenantBudgetsAtOrPast, ThresholdPct: thresholdPct})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		rows := page.Items
		if !has(rows, nearCap) {
			t.Error("tenant at 90% missing from the ≥80% list")
		}
		if !has(rows, frozen) {
			t.Error("tenant with a ceiling of 0 missing from the ≥80% list — it can spend nothing")
		}
		if has(rows, lowUse) {
			t.Error("tenant at 0% appeared in the ≥80% list")
		}
		if has(rows, unlimited) {
			t.Error("uncapped tenant appeared in the ≥80% list")
		}
		for _, r := range rows {
			if r.TenantID != nearCap {
				continue
			}
			if pct, limited := r.Utilization(); !limited || !closeEnough(pct, nearCapPct) {
				t.Errorf("utilization %v (limited %t), want %d", pct, limited, nearCapPct)
			}
		}
	})

	t.Run("the unlimited filter selects uncapped tenants", func(t *testing.T) {
		page, err := f.usage.ListTenantBudgets(ctx, limes.ListTenantBudgetsRequest{Filter: limes.TenantBudgetsUnlimited})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if !has(page.Items, unlimited) {
			t.Error("uncapped tenant missing from the unlimited list")
		}
		if has(page.Items, nearCap) || has(page.Items, frozen) {
			t.Error("capped tenant appeared in the unlimited list")
		}
	})
}

// Pages of the tenant budget list cover every tenant once, in the order, and
// of two tenants past their ceilings the further one leads although both read
// 100%. A tenant with a ceiling of 0 leads them all, and one with no ceiling
// trails.
func TestListTenantBudgetsPages(t *testing.T) {
	t.Parallel()
	ctx, f := newUsageFixture(t)
	ceiling := limes.BudgetCeiling{Max: limes.MustParseAmount("10")}
	// Spend per tenant, in the order the list must return them; the last two
	// tie, so tenant id decides between them.
	spends := []limes.Nanos{20 * limes.NanosPerUnit, 12 * limes.NanosPerUnit, 5 * limes.NanosPerUnit, limes.NanosPerUnit, limes.NanosPerUnit}
	// The tenant's own capability charges it: the tenant is the record's.
	chargeTenant := func(tenant uuid.UUID, spent limes.Nanos) {
		t.Helper()
		subject := f.subject
		if tenant != f.tenant {
			_, subject = insertMeteredCap(t, ctx, f.records, tenant, nil)
		}
		if _, err := f.usage.Charge(ctx, limes.ChargeRequest{Subject: subject, Amount: spent, Overrun: limes.OverrunRecord}, nil); err != nil {
			t.Fatal(err)
		}
	}
	setCeiling := func(tenant uuid.UUID, c limes.BudgetCeiling) {
		t.Helper()
		if _, err := f.usage.SetTenantBudget(ctx, limes.SetTenantBudgetRequest{TenantID: tenant, Ceiling: c}); err != nil {
			t.Fatal(err)
		}
	}

	frozen, _ := mkTenant(t, ctx, f.pool, "shared")
	setCeiling(frozen, limes.BudgetCeiling{})
	want := []uuid.UUID{frozen}
	var bounded []uuid.UUID
	for i, spent := range spends {
		tenant := f.tenant
		if i > 0 {
			tenant, _ = mkTenant(t, ctx, f.pool, "shared")
		}
		setCeiling(tenant, ceiling)
		chargeTenant(tenant, spent)
		bounded = append(bounded, tenant)
	}
	tail := bounded[len(bounded)-2:]
	slices.SortFunc(tail, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	want = append(want, bounded...)
	uncapped, _ := mkTenant(t, ctx, f.pool, "shared")
	chargeTenant(uncapped, limes.NanosPerUnit)
	want = append(want, uncapped)

	const pageSize = 2
	var got []uuid.UUID
	req := limes.ListTenantBudgetsRequest{Limit: pageSize}
	for pages := 0; ; pages++ {
		page, err := f.usage.ListTenantBudgets(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range page.Items {
			got = append(got, r.TenantID)
		}
		if page.Next == "" {
			break
		}
		if pages > len(want) {
			t.Fatal("the cursor never reached the last page")
		}
		req.Cursor = page.Next
	}
	if !slices.Equal(got, want) {
		t.Fatalf("paged %v, want %v", got, want)
	}
	if _, err := f.usage.ListTenantBudgets(ctx, limes.ListTenantBudgetsRequest{Cursor: "garbage"}); !errors.Is(err, limes.ErrInvalidRequest) {
		t.Errorf("a bad cursor: err = %v, want ErrInvalidRequest", err)
	}
}

// TestUsageGetDeleteAndPurgeOrphans pins the maintenance surface. The meter's
// rows outlive the capability's record (067), and PurgeOrphans is what drops
// them once the record is gone; if it deleted live rows instead, running
// counters would silently reset and callers would get their caps back.
func TestUsageGetDeleteAndPurgeOrphans(t *testing.T) {
	t.Parallel()
	ctx, f := newUsageFixture(t)

	if _, err := f.usage.GetUsage(ctx, uuid.New()); !errors.Is(err, limes.ErrUsageNotFound) {
		t.Fatalf("unknown capability: want ErrUsageNotFound, got %v", err)
	}

	if _, err := f.usage.Charge(ctx, limes.ChargeRequest{Subject: f.subject, Amount: limes.MustParseAmount("3"), Op: "op", Actor: "actor"}, nil); err != nil {
		t.Fatalf("charge: %v", err)
	}
	if _, err := f.usage.Bump(ctx, limes.BumpRequest{Subject: f.subject}); err != nil {
		t.Fatalf("bump: %v", err)
	}

	got, err := f.usage.GetUsage(ctx, f.capID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.RequestCount != 1 || got.SpentAmount != limes.MustParseAmount("3") || got.UnitCode != "USD" {
		t.Errorf("usage: %+v", got)
	}

	// A live capability's usage row must survive the orphan purge.
	if _, err := f.usage.PurgeOrphans(ctx, limes.PurgeOrphansRequest{}); err != nil {
		t.Fatalf("purge orphans: %v", err)
	}
	if _, err := f.usage.GetUsage(ctx, f.capID); err != nil {
		t.Fatalf("live usage row purged as an orphan: %v", err)
	}

	// Delete is a no-op on a missing row, and removes an existing one.
	if err := f.usage.DeleteUsage(ctx, limes.DeleteUsageRequest{CapabilityID: uuid.New()}); err != nil {
		t.Errorf("delete of unknown capability: %v", err)
	}
	if err := f.usage.DeleteUsage(ctx, limes.DeleteUsageRequest{CapabilityID: f.capID}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := f.usage.GetUsage(ctx, f.capID); !errors.Is(err, limes.ErrUsageNotFound) {
		t.Errorf("usage row survived Delete: %v", err)
	}

	// A usage row whose record is gone stays until the purge drops it.
	orphan, orphanSubject := insertMeteredCap(t, ctx, f.records, f.tenant, nil)
	if _, err := f.usage.Bump(ctx, limes.BumpRequest{Subject: orphanSubject}); err != nil {
		t.Fatalf("bump the soon-orphaned capability: %v", err)
	}
	mustExec(t, ctx, f.pool, `DELETE FROM capability_records WHERE id = $1`, orphan)
	if _, err := f.usage.GetUsage(ctx, orphan); err != nil {
		t.Fatalf("usage row went with its record: %v", err)
	}
	res, err := f.usage.PurgeOrphans(ctx, limes.PurgeOrphansRequest{})
	if err != nil {
		t.Fatalf("purge orphans: %v", err)
	}
	if res.Purged < 1 {
		t.Errorf("purged %d rows, want the orphan's", res.Purged)
	}
	if _, err := f.usage.GetUsage(ctx, orphan); !errors.Is(err, limes.ErrUsageNotFound) {
		t.Errorf("orphan usage row survived the purge: %v", err)
	}
}

// TestTenantBudgetCeilingNullIsUnlimitedAndNaNIsRefused pins the values
// max_budget_usd can hold. NULL is the unlimited ceiling (066): a tenant whose
// ceiling was never set, or was lifted, spends freely and is listed as
// uncapped. 0 is a ceiling that allows nothing. Before 066 the column held 0
// for "unlimited", so the two could not be told apart.
//
// NaN stays refused: Postgres orders NaN above every numeric, so
// `spent + amount <= NaN` always held and the cap enforced nothing.
func TestTenantBudgetCeilingNullIsUnlimitedAndNaNIsRefused(t *testing.T) {
	t.Parallel()
	ctx, f := newUsageFixture(t)

	if _, err := f.usage.SetTenantBudget(ctx, limes.SetTenantBudgetRequest{
		TenantID: f.tenant, Ceiling: limes.BudgetCeiling{Max: limes.MustParseAmount("100")}, UnitCode: "USD",
	}); err != nil {
		t.Fatalf("set budget: %v", err)
	}

	t.Run("the column refuses NaN", func(t *testing.T) {
		_, err := f.pool.Exec(ctx,
			`UPDATE tenant_budgets SET max_budget_usd = 'NaN'::numeric WHERE tenant_id = $1`, f.tenant)
		if err == nil {
			t.Fatal("a NaN cap was accepted — it reads as a limit and enforces nothing")
		}
	})

	t.Run("the store refuses an amount out of range", func(t *testing.T) {
		for _, bad := range []limes.Nanos{-limes.NanosPerUnit, limes.MaxNanos + 1} {
			if _, err := f.usage.SetTenantBudget(ctx, limes.SetTenantBudgetRequest{
				TenantID: f.tenant, Ceiling: limes.BudgetCeiling{Max: bad},
			}); err == nil {
				t.Errorf("SetTenantBudget accepted %v as a cap", bad)
			}
		}
		if _, err := f.usage.Charge(ctx, limes.ChargeRequest{Subject: f.subject, Amount: limes.MaxNanos + 1, Op: "op", Actor: "actor"}, nil); err == nil {
			t.Error("Charge accepted an amount beyond MaxNanos")
		}
	})

	t.Run("a spend still works after all of that", func(t *testing.T) {
		if _, err := f.usage.Charge(ctx, limes.ChargeRequest{Subject: f.subject, Amount: limes.MustParseAmount("5"), Op: "op", Actor: "actor"}, nil); err != nil {
			t.Fatalf("charge: %v", err)
		}
		got, err := f.usage.GetTenantBudget(ctx, f.tenant)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.SpentAmount != limes.MustParseAmount("5") {
			t.Errorf("spent %v, want 5", got.SpentAmount)
		}
	})

	t.Run("NULL reads as unlimited and admits any spend", func(t *testing.T) {
		mustExec(t, ctx, f.pool, `UPDATE tenant_budgets SET max_budget_usd = NULL WHERE tenant_id = $1`, f.tenant)
		got, err := f.usage.GetTenantBudget(ctx, f.tenant)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if !got.Ceiling.Unlimited {
			t.Fatalf("ceiling %s, want unlimited", got.Ceiling)
		}
		// Past the 100 the ceiling held before it was lifted.
		if _, err := f.usage.Charge(ctx, limes.ChargeRequest{Subject: f.subject, Amount: limes.MustParseAmount("500"), Op: "op", Actor: "actor"}, nil); err != nil {
			t.Fatalf("charge under no ceiling: %v", err)
		}
	})

	t.Run("0 admits nothing", func(t *testing.T) {
		mustExec(t, ctx, f.pool, `UPDATE tenant_budgets SET max_budget_usd = 0 WHERE tenant_id = $1`, f.tenant)
		if _, err := f.usage.Charge(ctx, limes.ChargeRequest{Subject: f.subject, Amount: limes.MustParseAmount("0.01"), Op: "op", Actor: "actor"}, nil); !errors.Is(err, limes.ErrTenantBudgetExceeded) {
			t.Fatalf("charge under a ceiling of 0: err = %v, want ErrTenantBudgetExceeded", err)
		}
	})
}

// TestChargeAlwaysWritesTheLedger pins that one UsageStore has one contract.
//
// The type used to accept a nil pool, which selected a second path: the same
// Charge call ran the counters and skipped both the charges ledger and the
// outbox fan-out. Two behaviours behind one method, chosen at construction,
// with no way for a caller to tell which instance it held — a spend that
// committed without its ledger row looks identical to one that did not, until
// somebody tries to bill for it.
//
// Nothing ever selected that path, in production or in tests, so it was a
// promise no call site could rely on and no test covered. This asserts what
// remains: every successful charge leaves a ledger row behind, and a store
// cannot be built without the pool that makes that possible.
func TestChargeAlwaysWritesTheLedger(t *testing.T) {
	t.Parallel()
	ctx, f := newUsageFixture(t)

	for i, amount := range []limes.Nanos{limes.MustParseAmount("1"), limes.MustParseAmount("2.5"), limes.MustParseAmount("0.001")} {
		if _, err := f.usage.Charge(ctx, limes.ChargeRequest{Subject: f.subject, Amount: amount, Op: "op", Actor: "actor"}, nil); err != nil {
			t.Fatalf("charge %d: %v", i, err)
		}
	}
	if n := f.ledgerRows(t, ctx); n != 3 {
		t.Errorf("ledger has %d rows after 3 charges, want 3", n)
	}
}

// TestUsageStoreRefusesANilPool pins the requirement at the boundary where it
// can still be fixed. A nil pool used to be accepted and silently downgrade
// every charge that followed; failing at wiring time turns a quiet
// data-integrity change into a startup crash, which is the right trade for a
// dependency the type cannot work correctly without.
func TestUsageStoreRefusesANilPool(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Error("NewUsageStore accepted a nil pool")
		}
	}()
	capstore.NewUsageStore(nil, nil, zap.NewNop())
}
