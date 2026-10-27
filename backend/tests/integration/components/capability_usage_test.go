//go:build integration

// The meter is limes/pgstore's, contract-tested by limes and here by
// capability_meter_contract_test.go. What these tests pin is Paladin's use of
// it: the outbox fan-out (ADR-0003) runs on the charge's own transaction, so
// a fan-out that fails takes the charge with it, and a tenant budget set with
// no unit is written in the platform's currency.
package components

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/limes/pgstore"
	capstore "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
)

// newUsageStore is the meter over pool, configured by opts.
func newUsageStore(t *testing.T, pool *pgxpool.Pool, opts ...capstore.Option) *capstore.UsageStore {
	t.Helper()
	u, err := capstore.NewUsageStore(pool, opts...)
	if err != nil {
		t.Fatalf("new usage store: %v", err)
	}
	return u
}

// usageFixture seeds a tenant and one capability with no ceilings of its own,
// and the meter over the same database. subject is the capability as the
// meter counts it, read from its record.
type usageFixture struct {
	pool    *pgxpool.Pool
	records *pgstore.Store
	usage   *capstore.UsageStore
	tenant  uuid.UUID
	capID   uuid.UUID
	subject limes.MeterSubject
}

// unlimitedCeilings lifts both of a capability's ceilings, so only the
// tenant's, an ancestor's or a copy's can bind.
func unlimitedCeilings(c *limes.Caveats) {
	c.Requests = limes.RequestCeiling{Unlimited: true}
	c.Budget = limes.BudgetCeiling{Unlimited: true, UnitCode: testUnit}
}

// storedSubject is the meter subject of a capability on record.
func storedSubject(t *testing.T, ctx context.Context, records *pgstore.Store, id uuid.UUID) limes.MeterSubject {
	t.Helper()
	s, err := limes.StoredMeterSubject(ctx, records, id)
	if err != nil {
		t.Fatalf("meter subject of %s: %v", id, err)
	}
	return s
}

func newUsageFixture(t *testing.T) (context.Context, usageFixture) {
	t.Helper()
	ctx := context.Background()
	pool := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, pool, "shared")
	records := newCapStore(t, pool)
	c := mkCap(tenant, "user:spender", time.Now().Add(time.Hour))
	unlimitedCeilings(&c.Caveats)
	if err := records.Insert(ctx, limes.InsertRequest{Capability: c, IssuedBy: seedIssuer}); err != nil {
		t.Fatalf("seed capability: %v", err)
	}
	return ctx, usageFixture{
		pool:    pool,
		records: records,
		usage:   newUsageStore(t, pool),
		tenant:  tenant,
		capID:   c.ID,
		subject: storedSubject(t, ctx, records, c.ID),
	}
}

// spentOn reads the per-capability counter straight from the table, so the
// assertion does not depend on the same code path it is checking.
func (f usageFixture) spentOn(t *testing.T, ctx context.Context) limes.Nanos {
	t.Helper()
	var spent limes.Nanos
	err := f.pool.QueryRow(ctx,
		`SELECT spent FROM limes.usage WHERE capability_id = $1`, f.capID,
	).Scan(&spent)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0
	}
	if err != nil {
		t.Fatalf("read capability spend: %v", err)
	}
	return spent
}

func (f usageFixture) tenantSpent(t *testing.T, ctx context.Context) limes.Nanos {
	t.Helper()
	var spent limes.Nanos
	err := f.pool.QueryRow(ctx,
		`SELECT spent FROM limes.tenant_budgets WHERE tenant_id = $1`, f.tenant,
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
		`SELECT count(*) FROM limes.charges WHERE capability_id = $1`, f.capID).Scan(&n); err != nil {
		t.Fatalf("count charges: %v", err)
	}
	return n
}

// TestChargeFanOutFailureRollsBackEverything pins ADR-0003's guarantee from
// the other side: if the outbox enqueue fails, the spend it describes must
// not commit either.
func TestChargeFanOutFailureRollsBackEverything(t *testing.T) {
	t.Parallel()
	ctx, f := newUsageFixture(t)

	boom := errors.New("outbox unavailable")
	_, err := f.usage.Charge(ctx, meter.ChargeRequest{Subject: f.subject, Amount: limes.MustParseAmount("3"), Op: "op", Actor: "actor"}, func(context.Context, meter.Charged) error { return boom })
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
// The first charge creates the capability's counter row, uncommitted while
// onCharged runs: the charge's own transaction sees it, another connection
// does not.
func TestChargeFanOutRunsOnTheChargeTransaction(t *testing.T) {
	t.Parallel()
	ctx, f := newUsageFixture(t)

	const countCounters = `SELECT count(*) FROM limes.usage WHERE capability_id = $1`
	var onTx, elsewhere int
	_, err := f.usage.Charge(ctx, meter.ChargeRequest{Subject: f.subject, Amount: limes.MustParseAmount("4"), Op: "op", Actor: "actor"}, func(c context.Context, _ meter.Charged) error {
		tx, ok := pgstore.TxFrom(c)
		if !ok {
			return errors.New("onCharged ran with no transaction in its context")
		}
		if err := tx.QueryRow(c, countCounters, f.capID).Scan(&onTx); err != nil {
			return err
		}
		return f.pool.QueryRow(c, countCounters, f.capID).Scan(&elsewhere)
	})
	if err != nil {
		t.Fatalf("charge: %v", err)
	}
	if onTx != 1 || elsewhere != 0 {
		t.Errorf("the counter row is visible to the fan-out's tx %d time(s) and to another connection %d time(s), want 1 and 0 — the fan-out is not running on the charge transaction", onTx, elsewhere)
	}
}

// The tenant budget's units: the platform's currency (capability.currency) and
// another one an operator names.
const (
	platformCurrency = "EUR"
	namedBudgetUnit  = "GBP"
)

// TestTenantBudgetIsFirstWrittenInThePlatformCurrency pins WithDefaultUnit: a
// first budget that names no unit is written in the platform's currency, a
// named unit is kept, and a later write that names none keeps whichever the
// budget has — a cap change never re-denominates it. With no platform currency
// a budget in no unit is refused: limes assumes none.
func TestTenantBudgetIsFirstWrittenInThePlatformCurrency(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	ceiling := limes.BudgetCeiling{Max: limes.MustParseAmount("100")}
	raised := limes.BudgetCeiling{Max: limes.MustParseAmount("200")}

	for name, tc := range map[string]struct {
		opts      []capstore.Option
		firstUnit string
		want      string
	}{
		"no unit, a platform currency": {[]capstore.Option{capstore.WithDefaultUnit(platformCurrency)}, "", platformCurrency},
		"a named unit":                 {[]capstore.Option{capstore.WithDefaultUnit(platformCurrency)}, namedBudgetUnit, namedBudgetUnit},
	} {
		t.Run(name, func(t *testing.T) {
			usage := newUsageStore(t, pool, tc.opts...)
			tenant, _ := mkTenant(t, ctx, pool, "shared")

			first, err := usage.SetTenantBudget(ctx, meter.SetTenantBudgetRequest{
				TenantID: tenant, Ceiling: limes.BudgetCeiling{Max: ceiling.Max, UnitCode: tc.firstUnit},
			})
			if err != nil {
				t.Fatalf("first set: %v", err)
			}
			if first.Ceiling.UnitCode != tc.want {
				t.Errorf("first budget in %q, want %q", first.Ceiling.UnitCode, tc.want)
			}

			if _, err := usage.SetTenantBudget(ctx, meter.SetTenantBudgetRequest{
				TenantID: tenant, Ceiling: raised, ExpectedVersion: first.ResourceVersion,
			}); err != nil {
				t.Fatalf("later set: %v", err)
			}
			got, err := usage.GetTenantBudget(ctx, tenant)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if got.Ceiling.Max != raised.Max || got.Ceiling.UnitCode != tc.want {
				t.Errorf("after a cap change with no unit: %s, want %s in %q", got.Ceiling, raised, tc.want)
			}
		})
	}
	t.Run("no unit, no currency", func(t *testing.T) {
		usage := newUsageStore(t, pool)
		tenant, _ := mkTenant(t, ctx, pool, "shared")
		if _, err := usage.SetTenantBudget(ctx, meter.SetTenantBudgetRequest{TenantID: tenant, Ceiling: ceiling}); !errors.Is(err, limes.ErrInvalidRequest) {
			t.Errorf("a budget in no unit = %v, want ErrInvalidRequest", err)
		}
	})
}

// Counters that hold only requests are in the capability's unit: the admin
// plane shows usage in the unit the meter records, with no fallback of its
// own.
func TestRequestsOnlyUsageIsInTheCapabilitysUnit(t *testing.T) {
	t.Parallel()
	ctx, f := newUsageFixture(t)
	if _, err := f.usage.Bump(ctx, meter.BumpRequest{Subject: f.subject}); err != nil {
		t.Fatalf("bump: %v", err)
	}
	u, err := f.usage.GetUsage(ctx, f.capID)
	if err != nil {
		t.Fatalf("get usage: %v", err)
	}
	if u.RequestCount != 1 || u.UnitCode != f.subject.UnitCode() {
		t.Errorf("usage = %d requests in %q, want 1 in %q", u.RequestCount, u.UnitCode, f.subject.UnitCode())
	}
}
