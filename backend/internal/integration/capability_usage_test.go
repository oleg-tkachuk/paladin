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
package integration

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/capability"
	capstore "github.com/oleg-tkachuk/paladin/internal/capability/postgres"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// usageFixture seeds a tenant and one capability, and returns a UsageStore
// wired the way production wires it (real pool ⇒ ledger + fan-out active).
type usageFixture struct {
	pool   *pgxpool.Pool
	usage  *capstore.UsageStore
	tenant uuid.UUID
	capID  uuid.UUID
}

func newUsageFixture(t *testing.T) (context.Context, usageFixture) {
	t.Helper()
	ctx := context.Background()
	pool := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, pool, "shared")

	c := mkCap(tenant, "user:spender", time.Now().Add(time.Hour))
	if err := newCapStore(t, pool).Insert(ctx, c, seedIssuer); err != nil {
		t.Fatalf("seed capability: %v", err)
	}
	return ctx, usageFixture{
		pool:   pool,
		usage:  capstore.NewUsageStore(sqlc.New(pool), pool, zap.NewNop()),
		tenant: tenant,
		capID:  c.ID,
	}
}

// spentOn reads the per-capability counter straight from the table, so the
// assertion does not depend on the same code path it is checking.
func (f usageFixture) spentOn(t *testing.T, ctx context.Context) float64 {
	t.Helper()
	var spent float64
	err := f.pool.QueryRow(ctx,
		`SELECT COALESCE(spent_usd, 0)::float8 FROM capability_usage WHERE capability_id = $1`, f.capID,
	).Scan(&spent)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0
	}
	if err != nil {
		t.Fatalf("read capability spend: %v", err)
	}
	return spent
}

func (f usageFixture) tenantSpent(t *testing.T, ctx context.Context) float64 {
	t.Helper()
	var spent float64
	err := f.pool.QueryRow(ctx,
		`SELECT COALESCE(spent_usd, 0)::float8 FROM tenant_budgets WHERE tenant_id = $1`, f.tenant,
	).Scan(&spent)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0
	}
	if err != nil {
		t.Fatalf("read tenant spend: %v", err)
	}
	return spent
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

func closeEnough(got, want float64) bool { return math.Abs(got-want) < 1e-9 }

// TestBumpRequestEnforcesCap pins the request counter: it counts up, it
// refuses the call that would cross the cap, and a refused call does not
// consume quota it then reports as spent.
func TestBumpRequestEnforcesCap(t *testing.T) {
	ctx, f := newUsageFixture(t)

	for want := int64(1); want <= 3; want++ {
		got, err := f.usage.BumpRequest(ctx, f.capID, 3)
		if err != nil {
			t.Fatalf("bump %d: %v", want, err)
		}
		if got != want {
			t.Fatalf("bump %d returned count %d", want, got)
		}
	}

	if _, err := f.usage.BumpRequest(ctx, f.capID, 3); !errors.Is(err, capability.ErrRequestLimitExceeded) {
		t.Fatalf("fourth bump: want ErrRequestLimitExceeded, got %v", err)
	}

	// The refused bump must not have incremented anything.
	got, err := f.usage.Get(ctx, f.capID)
	if err != nil {
		t.Fatalf("get usage: %v", err)
	}
	if got.RequestCount != 3 {
		t.Errorf("request_count is %d after a refused bump, want 3", got.RequestCount)
	}
}

// TestBumpRequestUnlimited pins that max=0 means unlimited rather than
// "reject everything" — the sense of the zero value is easy to invert and the
// consequence is every capability without an explicit cap being unusable.
func TestBumpRequestUnlimited(t *testing.T) {
	ctx, f := newUsageFixture(t)
	for i := 0; i < 5; i++ {
		if _, err := f.usage.BumpRequest(ctx, f.capID, 0); err != nil {
			t.Fatalf("unlimited bump %d: %v", i, err)
		}
	}
}

// TestChargeWritesLedgerAtomically pins the happy path across all three
// writes: the capability counter, the tenant aggregate, and the ledger row
// that the billing surface reads.
func TestChargeWritesLedgerAtomically(t *testing.T) {
	ctx, f := newUsageFixture(t)

	spent, err := f.usage.Charge(ctx, f.capID, 2.50, 0, "USD", f.tenant, "objects.put", "user:alice", nil)
	if err != nil {
		t.Fatalf("charge: %v", err)
	}
	if !closeEnough(spent, 2.50) {
		t.Errorf("returned spend %v, want 2.50", spent)
	}
	if got := f.spentOn(t, ctx); !closeEnough(got, 2.50) {
		t.Errorf("capability counter %v, want 2.50", got)
	}
	if got := f.tenantSpent(t, ctx); !closeEnough(got, 2.50) {
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
	ctx, f := newUsageFixture(t)
	if _, err := f.usage.Charge(ctx, f.capID, -1, 0, "USD", f.tenant, "op", "actor", nil); err == nil {
		t.Fatal("negative charge accepted")
	}
	if n := f.ledgerRows(t, ctx); n != 0 {
		t.Errorf("rejected charge wrote %d ledger rows", n)
	}
}

// TestChargeCapExceededLeavesNothingBehind pins the per-capability cap and,
// more importantly, that crossing it writes nothing at all.
func TestChargeCapExceededLeavesNothingBehind(t *testing.T) {
	ctx, f := newUsageFixture(t)

	if _, err := f.usage.Charge(ctx, f.capID, 8, 10, "USD", f.tenant, "op", "actor", nil); err != nil {
		t.Fatalf("first charge: %v", err)
	}
	if _, err := f.usage.Charge(ctx, f.capID, 5, 10, "USD", f.tenant, "op", "actor", nil); !errors.Is(err, capability.ErrBudgetExceeded) {
		t.Fatalf("over-cap charge: want ErrBudgetExceeded, got %v", err)
	}

	if got := f.spentOn(t, ctx); !closeEnough(got, 8) {
		t.Errorf("capability counter moved to %v on a refused charge, want 8", got)
	}
	if got := f.tenantSpent(t, ctx); !closeEnough(got, 8) {
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
	ctx, f := newUsageFixture(t)

	if _, err := f.usage.SetTenantBudget(ctx, capability.SetTenantBudgetArgs{
		TenantID: f.tenant, MaxBudgetAmount: 10, UnitCode: "USD",
	}); err != nil {
		t.Fatalf("set tenant budget: %v", err)
	}

	if _, err := f.usage.Charge(ctx, f.capID, 25, 0, "USD", f.tenant, "op", "actor", nil); !errors.Is(err, capability.ErrTenantBudgetExceeded) {
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
	ctx, f := newUsageFixture(t)

	boom := errors.New("outbox unavailable")
	_, err := f.usage.Charge(ctx, f.capID, 3, 0, "USD", f.tenant, "op", "actor",
		func(context.Context, pgx.Tx) error { return boom })
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
	ctx, f := newUsageFixture(t)

	var sawSpend float64
	_, err := f.usage.Charge(ctx, f.capID, 4, 0, "USD", f.tenant, "op", "actor",
		func(c context.Context, tx pgx.Tx) error {
			// Uncommitted at this point: only the charge's own transaction
			// can see the row.
			return tx.QueryRow(c,
				`SELECT spent_usd::float8 FROM capability_usage WHERE capability_id = $1`, f.capID,
			).Scan(&sawSpend)
		})
	if err != nil {
		t.Fatalf("charge: %v", err)
	}
	if !closeEnough(sawSpend, 4) {
		t.Errorf("fan-out saw spend %v, want 4 — it is not running on the charge transaction", sawSpend)
	}
}

// TestChargeWithoutTenantSkipsLedger pins the documented shape of the
// no-tenant path: the capability counter still moves, but nothing bills.
func TestChargeWithoutTenantSkipsLedger(t *testing.T) {
	ctx, f := newUsageFixture(t)

	if _, err := f.usage.Charge(ctx, f.capID, 1.25, 0, "USD", uuid.Nil, "op", "actor", nil); err != nil {
		t.Fatalf("charge: %v", err)
	}
	if got := f.spentOn(t, ctx); !closeEnough(got, 1.25) {
		t.Errorf("capability counter %v, want 1.25", got)
	}
	if n := f.ledgerRows(t, ctx); n != 0 {
		t.Errorf("ledger has %d rows for a tenant-less charge, want 0", n)
	}
}

// TestChargeRejectsUnknownUnitCode pins that the currency is validated before
// anything is written — a ledger denominated in a unit nothing can convert is
// unusable for invoicing.
func TestChargeRejectsUnknownUnitCode(t *testing.T) {
	ctx, f := newUsageFixture(t)
	if _, err := f.usage.Charge(ctx, f.capID, 1, 0, "XYZ", f.tenant, "op", "actor", nil); err == nil {
		t.Fatal("unknown unit code accepted")
	}
}

// TestRefundsFloorAtZero pins both refund paths. Overshooting must clamp
// rather than go negative: a negative counter is future budget granted for
// free, and it compounds silently.
func TestRefundsFloorAtZero(t *testing.T) {
	ctx, f := newUsageFixture(t)

	if _, err := f.usage.Charge(ctx, f.capID, 5, 0, "USD", f.tenant, "op", "actor", nil); err != nil {
		t.Fatalf("charge: %v", err)
	}

	if err := f.usage.RefundCapability(ctx, f.capID, 2); err != nil {
		t.Fatalf("refund capability: %v", err)
	}
	if got := f.spentOn(t, ctx); !closeEnough(got, 3) {
		t.Errorf("capability counter %v after 5−2, want 3", got)
	}
	if err := f.usage.RefundCapability(ctx, f.capID, 99); err != nil {
		t.Fatalf("over-refund capability: %v", err)
	}
	if got := f.spentOn(t, ctx); got != 0 {
		t.Errorf("capability counter %v after over-refund, want 0", got)
	}

	if err := f.usage.RefundTenant(ctx, f.tenant, 99); err != nil {
		t.Fatalf("over-refund tenant: %v", err)
	}
	if got := f.tenantSpent(t, ctx); got != 0 {
		t.Errorf("tenant counter %v after over-refund, want 0", got)
	}

	// Non-positive refunds are no-ops, not errors — the callers retry them.
	if err := f.usage.RefundCapability(ctx, f.capID, 0); err != nil {
		t.Errorf("zero refund: %v", err)
	}
	if err := f.usage.RefundTenant(ctx, f.tenant, -1); err != nil {
		t.Errorf("negative refund: %v", err)
	}
	// Refunding a capability with no usage row must not error either.
	if err := f.usage.RefundCapability(ctx, uuid.New(), 1); err != nil {
		t.Errorf("refund of unknown capability: %v", err)
	}
}

// TestTenantBudgetLifecycle pins Set/Get, including the two options operators
// actually use: a mid-cycle cap change that preserves spend, and a monthly
// close that resets it.
func TestTenantBudgetLifecycle(t *testing.T) {
	ctx, f := newUsageFixture(t)

	if _, err := f.usage.GetTenantBudget(ctx, f.tenant); !errors.Is(err, capability.ErrTenantBudgetNotFound) {
		t.Fatalf("unset budget: want ErrTenantBudgetNotFound, got %v", err)
	}

	end := time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Microsecond)
	if _, err := f.usage.SetTenantBudget(ctx, capability.SetTenantBudgetArgs{
		TenantID: f.tenant, MaxBudgetAmount: 100, UnitCode: "EUR", PeriodEnd: &end,
	}); err != nil {
		t.Fatalf("set: %v", err)
	}

	got, err := f.usage.GetTenantBudget(ctx, f.tenant)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !closeEnough(got.MaxBudgetAmount, 100) || got.UnitCode != "EUR" {
		t.Errorf("budget: %+v", got)
	}
	if got.PeriodEnd == nil || !got.PeriodEnd.Equal(end) {
		t.Errorf("period_end: got %v want %v", got.PeriodEnd, end)
	}

	if _, err := f.usage.Charge(ctx, f.capID, 40, 0, "EUR", f.tenant, "op", "actor", nil); err != nil {
		t.Fatalf("charge: %v", err)
	}

	t.Run("mid-cycle cap change preserves spend and currency", func(t *testing.T) {
		if _, err := f.usage.SetTenantBudget(ctx, capability.SetTenantBudgetArgs{
			TenantID: f.tenant, MaxBudgetAmount: 200,
		}); err != nil {
			t.Fatalf("set: %v", err)
		}
		got, err := f.usage.GetTenantBudget(ctx, f.tenant)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if !closeEnough(got.SpentAmount, 40) {
			t.Errorf("spend reset by a cap change: got %v want 40", got.SpentAmount)
		}
		if got.UnitCode != "EUR" {
			t.Errorf("empty unit_code re-denominated the budget to %q", got.UnitCode)
		}
	})

	t.Run("reset_spend closes the period", func(t *testing.T) {
		if _, err := f.usage.SetTenantBudget(ctx, capability.SetTenantBudgetArgs{
			TenantID: f.tenant, MaxBudgetAmount: 200, ResetSpend: true,
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
		if _, err := f.usage.SetTenantBudget(ctx, capability.SetTenantBudgetArgs{
			TenantID: f.tenant, MaxBudgetAmount: 1, UnitCode: "XYZ",
		}); err == nil {
			t.Error("invalid unit_code accepted")
		}
	})
}

// TestListTenantBudgets pins the dashboard query's filters. Getting the
// threshold predicate wrong shows operators an empty alert list over tenants
// that are actually near their cap.
func TestListTenantBudgets(t *testing.T) {
	ctx, f := newUsageFixture(t)

	nearCap := f.tenant // will sit at 90%
	unlimited, _ := mkTenant(t, ctx, f.pool, "shared")
	lowUse, _ := mkTenant(t, ctx, f.pool, "shared")

	if _, err := f.usage.SetTenantBudget(ctx, capability.SetTenantBudgetArgs{TenantID: nearCap, MaxBudgetAmount: 100}); err != nil {
		t.Fatalf("set near-cap: %v", err)
	}
	if _, err := f.usage.SetTenantBudget(ctx, capability.SetTenantBudgetArgs{TenantID: unlimited, MaxBudgetAmount: 0}); err != nil {
		t.Fatalf("set unlimited: %v", err)
	}
	if _, err := f.usage.SetTenantBudget(ctx, capability.SetTenantBudgetArgs{TenantID: lowUse, MaxBudgetAmount: 100}); err != nil {
		t.Fatalf("set low-use: %v", err)
	}
	if _, err := f.usage.Charge(ctx, f.capID, 90, 0, "USD", nearCap, "op", "actor", nil); err != nil {
		t.Fatalf("charge near-cap: %v", err)
	}

	has := func(rows []capability.TenantBudgetSummary, id uuid.UUID) bool {
		for _, r := range rows {
			if r.TenantID == id {
				return true
			}
		}
		return false
	}

	t.Run("threshold selects tenants at or above it", func(t *testing.T) {
		rows, err := f.usage.ListTenantBudgets(ctx, capability.ListTenantBudgetsArgs{ThresholdPct: 80})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if !has(rows, nearCap) {
			t.Error("tenant at 90% missing from the ≥80% list")
		}
		if has(rows, lowUse) {
			t.Error("tenant at 0% appeared in the ≥80% list")
		}
		for _, r := range rows {
			if r.TenantID == nearCap && !closeEnough(r.UtilisationPct, 90) {
				t.Errorf("utilisation %v, want 90", r.UtilisationPct)
			}
			if r.TenantID == nearCap && r.Slug == "" {
				t.Error("summary is missing the tenant slug the dashboard renders")
			}
		}
	})

	t.Run("unlimited_only selects uncapped tenants", func(t *testing.T) {
		rows, err := f.usage.ListTenantBudgets(ctx, capability.ListTenantBudgetsArgs{UnlimitedOnly: true})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if !has(rows, unlimited) {
			t.Error("uncapped tenant missing from unlimited_only")
		}
		if has(rows, nearCap) {
			t.Error("capped tenant appeared in unlimited_only")
		}
	})

	t.Run("exclude_inactive drops soft-deleted tenants", func(t *testing.T) {
		mustExec(t, ctx, f.pool, `UPDATE tenants SET deleted_at = now() WHERE id = $1`, lowUse)
		rows, err := f.usage.ListTenantBudgets(ctx, capability.ListTenantBudgetsArgs{ExcludeInactive: true})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if has(rows, lowUse) {
			t.Error("soft-deleted tenant survived exclude_inactive")
		}
	})
}

// TestUsageGetDeleteAndPurgeOrphans pins the maintenance surface. PurgeOrphans
// exists because capability_usage outlives its capability in one direction
// only; if it deleted live rows instead, running counters would silently reset
// and callers would get their caps back.
func TestUsageGetDeleteAndPurgeOrphans(t *testing.T) {
	ctx, f := newUsageFixture(t)

	if _, err := f.usage.Get(ctx, uuid.New()); !errors.Is(err, capability.ErrUsageNotFound) {
		t.Fatalf("unknown capability: want ErrUsageNotFound, got %v", err)
	}

	if _, err := f.usage.Charge(ctx, f.capID, 3, 0, "USD", f.tenant, "op", "actor", nil); err != nil {
		t.Fatalf("charge: %v", err)
	}
	if _, err := f.usage.BumpRequest(ctx, f.capID, 0); err != nil {
		t.Fatalf("bump: %v", err)
	}

	got, err := f.usage.Get(ctx, f.capID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.RequestCount != 1 || !closeEnough(got.SpentAmount, 3) || got.UnitCode != "USD" {
		t.Errorf("usage: %+v", got)
	}

	// A live capability's usage row must survive the orphan purge.
	if _, err := f.usage.PurgeOrphans(ctx); err != nil {
		t.Fatalf("purge orphans: %v", err)
	}
	if _, err := f.usage.Get(ctx, f.capID); err != nil {
		t.Fatalf("live usage row purged as an orphan: %v", err)
	}

	// Delete is a no-op on a missing row, and removes an existing one.
	if err := f.usage.Delete(ctx, uuid.New()); err != nil {
		t.Errorf("delete of unknown capability: %v", err)
	}
	if err := f.usage.Delete(ctx, f.capID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := f.usage.Get(ctx, f.capID); !errors.Is(err, capability.ErrUsageNotFound) {
		t.Errorf("usage row survived Delete: %v", err)
	}
}

// TestTenantBudgetCapCannotBeNullOrNaN pins the two values that used to sit in
// max_budget_usd and satisfy neither `= 0` nor `> 0`. Every budget predicate is
// written as one of those two comparisons, so a NULL cap put the tenant in
// neither branch: ChargeTenantBudget matched no row and returned "budget
// exceeded" for every charge, while the summary query listed the tenant as
// neither capped nor unlimited. Unable to spend, and invisible to the operator
// who would have to notice.
//
// NaN was the same hole from the other side — Postgres orders NaN above every
// numeric, so `spent + amount <= NaN` always held and the cap enforced
// nothing.
//
// 005 makes the column NOT NULL DEFAULT 0 and rejects NaN, so both are now
// unrepresentable rather than merely unlikely.
func TestTenantBudgetCapCannotBeNullOrNaN(t *testing.T) {
	ctx, f := newUsageFixture(t)

	if _, err := f.usage.SetTenantBudget(ctx, capability.SetTenantBudgetArgs{
		TenantID: f.tenant, MaxBudgetAmount: 100, UnitCode: "USD",
	}); err != nil {
		t.Fatalf("set budget: %v", err)
	}

	t.Run("the column refuses NULL", func(t *testing.T) {
		_, err := f.pool.Exec(ctx,
			`UPDATE tenant_budgets SET max_budget_usd = NULL WHERE tenant_id = $1`, f.tenant)
		if err == nil {
			t.Fatal("a NULL cap was accepted — a tenant in this state can neither spend nor be listed")
		}
	})

	t.Run("the column refuses NaN", func(t *testing.T) {
		_, err := f.pool.Exec(ctx,
			`UPDATE tenant_budgets SET max_budget_usd = 'NaN'::numeric WHERE tenant_id = $1`, f.tenant)
		if err == nil {
			t.Fatal("a NaN cap was accepted — it reads as a limit and enforces nothing")
		}
	})

	t.Run("the encoder refuses a non-finite amount", func(t *testing.T) {
		for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
			if _, err := f.usage.SetTenantBudget(ctx, capability.SetTenantBudgetArgs{
				TenantID: f.tenant, MaxBudgetAmount: bad,
			}); err == nil {
				t.Errorf("SetTenantBudget accepted %v as a cap", bad)
			}
		}
		if _, err := f.usage.Charge(ctx, f.capID, math.NaN(), 0, "USD", f.tenant, "op", "actor", nil); err == nil {
			t.Error("Charge accepted a NaN amount")
		}
	})

	t.Run("a spend still works after all of that", func(t *testing.T) {
		if _, err := f.usage.Charge(ctx, f.capID, 5, 0, "USD", f.tenant, "op", "actor", nil); err != nil {
			t.Fatalf("charge: %v", err)
		}
		got, err := f.usage.GetTenantBudget(ctx, f.tenant)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if !closeEnough(got.SpentAmount, 5) {
			t.Errorf("spent %v, want 5", got.SpentAmount)
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
	ctx, f := newUsageFixture(t)

	for i, amount := range []float64{1, 2.5, 0.001} {
		if _, err := f.usage.Charge(ctx, f.capID, amount, 0, "USD", f.tenant, "op", "actor", nil); err != nil {
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
	defer func() {
		if recover() == nil {
			t.Error("NewUsageStore accepted a nil pool")
		}
	}()
	capstore.NewUsageStore(nil, nil, zap.NewNop())
}
