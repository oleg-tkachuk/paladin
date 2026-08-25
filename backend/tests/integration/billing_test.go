//go:build integration

// End-to-end coverage for the billing surface: capability charges flow
// through capability/postgres.UsageStore.Charge — bumping the
// capability counter, the tenant aggregate, AND writing a charges
// ledger row — and the read side is the BillingService handler that
// aggregates the ledger over a time window.
//
// Both sides exercise a real Postgres via pgharness so the SQL
// (date_trunc bucketing, GROUP BY tie-breaker, RLS-bypassed reads)
// is wired against the actual migrations rather than a hand-rolled
// in-memory aggregator.
package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/capability"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/billingh"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	capabilitypg "github.com/oleg-tkachuk/paladin/internal/capability/postgres"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/tests/integration/pgharness"
)

// allowAuth is the permit-everything Cedar authorizer used by the
// billing tests. Mirrors the pattern in
// internal/api/admin/v1/billingh/handler_test.go — the tests target
// the SQL aggregation, not the policy gate, so we short-circuit the
// gate to allow.
type allowAuth struct{}

func (allowAuth) IsAuthorized(_ context.Context, _ *cedar.Principal, _ string, _ *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionAllow, nil
}

// ─── fixture ────────────────────────────────────────────────────────────────

type billingFixture struct {
	h       *pgharness.Harness
	store   *capabilitypg.UsageStore
	handler *billingh.Handler
}

func setupBilling(t *testing.T) *billingFixture {
	t.Helper()
	h := pgharness.Setup(t)
	q := sqlc.New(h.PoolMigrate)
	store := capabilitypg.NewUsageStore(q, h.PoolMigrate, nil)
	handler := billingh.NewHandler(h.PoolMigrate, store, allowAuth{})
	return &billingFixture{h: h, store: store, handler: handler}
}

// ctxAdmin returns a context carrying a permit-everything admin
// principal — the handler's Cedar gate reads the principal even
// though the allowAuth stub ignores its contents.
func ctxAdmin(t *testing.T, tenantID uuid.UUID) context.Context {
	t.Helper()
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		TenantID: tenantID,
		Subject:  "ops@example.com",
		Roles:    []string{"platform.admin"},
		Audience: "paladin-admin",
	})
}

// seedCapability creates a capability_records row so charges can
// FK-reference it.
func (f *billingFixture) seedCapability(t *testing.T, tenant uuid.UUID, subject string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := f.h.PoolMigrate.Exec(context.Background(),
		`INSERT INTO capability_records
		   (id, tenant_id, issuer, principal_kind, principal_subject,
		    audience, caveats, created_by, expires_at)
		 VALUES ($1, $2, 'test-issuer', 'agent', $3,
		         '{admin}', '{}'::jsonb, 'test-issuer',
		         now() + interval '1 hour')`,
		id, tenant, subject,
	); err != nil {
		t.Fatalf("seed capability: %v", err)
	}
	return id
}

// seedBudget upserts a tenant budget cap.
//
// Reads the current version first: Set carries an OCC guard, and a charge may
// already have created the accumulator row, in which case this is an update
// and 0 would be refused. GetTenantBudget on a missing row yields the zero
// value, whose version is 0 — the create case, which is what we want.
func (f *billingFixture) seedBudget(t *testing.T, tenant uuid.UUID, max float64, unit string) {
	t.Helper()
	cur, err := f.store.GetTenantBudget(context.Background(), tenant)
	if err != nil && !errors.Is(err, capability.ErrTenantBudgetNotFound) {
		t.Fatalf("read budget: %v", err)
	}
	if _, err := f.store.SetTenantBudget(context.Background(), capability.SetTenantBudgetArgs{
		TenantID:        tenant,
		MaxBudgetAmount: max,
		UnitCode:        unit,
		ResetSpend:      true,
		ExpectedVersion: cur.ResourceVersion,
	}); err != nil {
		t.Fatalf("seed budget: %v", err)
	}
}

// charge runs UsageStore.Charge (the production write path).
func (f *billingFixture) charge(t *testing.T, capID uuid.UUID, amount float64, unit, op, actor string, tenant uuid.UUID) {
	t.Helper()
	if _, err := f.store.Charge(context.Background(), capID, amount, 0, unit, tenant, op, actor, nil); err != nil {
		t.Fatalf("charge: %v", err)
	}
}

// chargeAt inserts a charges row directly with an explicit
// occurred_at, bypassing UsageStore so tests can backdate charges
// into bucketed time windows. Does NOT bump the capability_usage /
// tenant_budgets counters — tests that mix this with summary asserts
// should be reading totals from the charges ledger only (which the
// summary handler does).
func (f *billingFixture) chargeAt(t *testing.T, capID uuid.UUID, tenant uuid.UUID, when time.Time, amount float64, unit, op, actor string) {
	t.Helper()
	if _, err := f.h.PoolMigrate.Exec(context.Background(),
		`INSERT INTO charges (id, tenant_id, tenant_slug, capability_id,
		                      occurred_at, amount, unit_code, op, actor_subject)
		 SELECT $1, $2, t.slug, $3, $4, $5, $6, $7, $8
		   FROM tenants t WHERE t.id = $2`,
		uuid.New(), tenant, capID, when, amount, unit, op, actor,
	); err != nil {
		t.Fatalf("seed charge at %v: %v", when, err)
	}
}

func (f *billingFixture) summary(t *testing.T, tenant uuid.UUID, start, end time.Time) *billingh.Summary {
	t.Helper()
	out, err := f.handler.GetTenantSummary(ctxAdmin(t, tenant), tenant, start, end)
	if err != nil {
		t.Fatalf("GetTenantSummary: %v", err)
	}
	return out
}

func (f *billingFixture) timeSeries(t *testing.T, tenant uuid.UUID, start, end time.Time, gran string) *billingh.TimeSeries {
	t.Helper()
	out, err := f.handler.GetTenantTimeSeries(ctxAdmin(t, tenant), tenant, start, end, gran)
	if err != nil {
		t.Fatalf("GetTenantTimeSeries: %v", err)
	}
	return out
}

// ─── tests ──────────────────────────────────────────────────────────────────

// TestBilling_ChargeWritesLedgerRow: a single Charge() bumps both
// running totals AND inserts the matching charges row.
func TestBilling_ChargeWritesLedgerRow(t *testing.T) {
	t.Parallel()
	f := setupBilling(t)

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "bil-write")
	f.seedBudget(t, tenant, 100, "USD")
	capID := f.seedCapability(t, tenant, "agent-1")
	f.charge(t, capID, 2.50, "USD", "presign.put", "agent-1", tenant)

	// capability_usage running total
	usage, err := f.store.Get(context.Background(), capID)
	if err != nil {
		t.Fatalf("get usage: %v", err)
	}
	if usage.SpentAmount < 2.49 || usage.SpentAmount > 2.51 {
		t.Errorf("cap spent = %v, want 2.50", usage.SpentAmount)
	}
	if usage.UnitCode != "USD" {
		t.Errorf("cap unit = %q, want USD", usage.UnitCode)
	}

	// tenant_budgets running total
	tb, err := f.store.GetTenantBudget(context.Background(), tenant)
	if err != nil {
		t.Fatalf("get tenant: %v", err)
	}
	if tb.SpentAmount < 2.49 || tb.SpentAmount > 2.51 {
		t.Errorf("tenant spent = %v, want 2.50", tb.SpentAmount)
	}

	// charges ledger row
	var (
		count    int64
		amount   float64
		unitCode string
		op       string
		actor    string
	)
	if err := f.h.PoolMigrate.QueryRow(context.Background(),
		`SELECT COUNT(*), COALESCE(SUM(amount), 0)::float8,
		        COALESCE(MAX(unit_code), ''),
		        COALESCE(MAX(op), ''),
		        COALESCE(MAX(actor_subject), '')
		   FROM charges WHERE tenant_id = $1`, tenant,
	).Scan(&count, &amount, &unitCode, &op, &actor); err != nil {
		t.Fatalf("ledger scan: %v", err)
	}
	if count != 1 {
		t.Errorf("ledger rows = %d, want 1", count)
	}
	if amount < 2.49 || amount > 2.51 {
		t.Errorf("ledger amount = %v, want 2.50", amount)
	}
	if unitCode != "USD" || op != "presign.put" || actor != "agent-1" {
		t.Errorf("ledger metadata = (%q, %q, %q)", unitCode, op, actor)
	}
}

// TestBilling_GetTenantSummary_AggregatesCorrectly: 22 charges across
// 3 caps, 2 ops; verify totals and the top-N breakdowns.
func TestBilling_GetTenantSummary_AggregatesCorrectly(t *testing.T) {
	t.Parallel()
	f := setupBilling(t)

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "bil-agg")
	f.seedBudget(t, tenant, 500, "USD")
	capA := f.seedCapability(t, tenant, "agent-A")
	capB := f.seedCapability(t, tenant, "agent-B")
	capC := f.seedCapability(t, tenant, "agent-C")

	for i := 0; i < 10; i++ {
		f.charge(t, capA, 1.0, "USD", "get", "agent-A", tenant)
	}
	for i := 0; i < 4; i++ {
		f.charge(t, capB, 0.5, "USD", "put", "agent-B", tenant)
	}
	for i := 0; i < 8; i++ {
		f.charge(t, capC, 0.25, "USD", "get", "agent-C", tenant)
	}

	end := time.Now().UTC().Add(time.Hour)
	start := end.Add(-30 * 24 * time.Hour)
	s := f.summary(t, tenant, start, end)

	if got, want := s.TotalAmount, 14.0; got < want-0.01 || got > want+0.01 {
		t.Errorf("total = %v, want 14.0", got)
	}
	if s.ChargeCount != 22 {
		t.Errorf("count = %d, want 22", s.ChargeCount)
	}
	if s.UnitCode != "USD" {
		t.Errorf("unit = %q, want USD", s.UnitCode)
	}
	if got, want := s.MaxBudgetAmount, 500.0; got < want-0.01 || got > want+0.01 {
		t.Errorf("max budget = %v, want 500", got)
	}

	// top_capabilities: A=10, then B and C tied at 2.0
	if len(s.TopCapabilities) != 3 {
		t.Fatalf("top caps len = %d, want 3", len(s.TopCapabilities))
	}
	if s.TopCapabilities[0].Label != capA.String() {
		t.Errorf("top cap[0] = %q, want %q", s.TopCapabilities[0].Label, capA)
	}
	if got := s.TopCapabilities[0].Amount; got < 9.99 || got > 10.01 {
		t.Errorf("top cap[0] amount = %v, want 10.0", got)
	}
	// caps[1] and caps[2] are B/C in either order; just check both appear.
	tail := map[string]float64{
		s.TopCapabilities[1].Label: s.TopCapabilities[1].Amount,
		s.TopCapabilities[2].Label: s.TopCapabilities[2].Amount,
	}
	if v, ok := tail[capB.String()]; !ok || v < 1.99 || v > 2.01 {
		t.Errorf("cap B missing or wrong amount in tail: %v", tail)
	}
	if v, ok := tail[capC.String()]; !ok || v < 1.99 || v > 2.01 {
		t.Errorf("cap C missing or wrong amount in tail: %v", tail)
	}

	// top_actors: agent-A (10), then B/C tied
	if len(s.TopActors) != 3 {
		t.Errorf("top actors = %d, want 3", len(s.TopActors))
	}
	if len(s.TopActors) > 0 && s.TopActors[0].Label != "agent-A" {
		t.Errorf("top actor[0] = %q, want agent-A", s.TopActors[0].Label)
	}

	// top_ops: get=12@18, put=2@4. ORDER BY amount DESC → get, put.
	if len(s.TopOps) != 2 {
		t.Fatalf("top ops len = %d, want 2", len(s.TopOps))
	}
	if s.TopOps[0].Label != "get" || s.TopOps[0].ChargeCount != 18 {
		t.Errorf("top op[0] = %+v, want {get, 18}", s.TopOps[0])
	}
	if got := s.TopOps[0].Amount; got < 11.99 || got > 12.01 {
		t.Errorf("top op[0] amount = %v, want 12", got)
	}
	if s.TopOps[1].Label != "put" || s.TopOps[1].ChargeCount != 4 {
		t.Errorf("top op[1] = %+v, want {put, 4}", s.TopOps[1])
	}
	if got := s.TopOps[1].Amount; got < 1.99 || got > 2.01 {
		t.Errorf("top op[1] amount = %v, want 2", got)
	}
}

// TestBilling_GetTenantTimeSeries_BucketsByDay: charges spread across
// 3 distinct UTC days bucket cleanly into 3 day buckets.
func TestBilling_GetTenantTimeSeries_BucketsByDay(t *testing.T) {
	t.Parallel()
	f := setupBilling(t)

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "bil-ts")
	cap := f.seedCapability(t, tenant, "agent")

	// Pick three different days well in the past so they bucket
	// independently of "now". Use UTC noon to dodge DST edge cases.
	day1 := time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 1, 6, 12, 0, 0, 0, time.UTC)
	day3 := time.Date(2026, 1, 7, 12, 0, 0, 0, time.UTC)
	f.chargeAt(t, cap, tenant, day1, 1.0, "USD", "get", "agent")
	f.chargeAt(t, cap, tenant, day1, 2.0, "USD", "get", "agent") // day1 = 3.0
	f.chargeAt(t, cap, tenant, day2, 5.0, "USD", "put", "agent") // day2 = 5.0
	f.chargeAt(t, cap, tenant, day3, 7.0, "USD", "get", "agent") // day3 = 7.0

	start := day1.Add(-time.Hour)
	end := day3.Add(2 * time.Hour)
	ts := f.timeSeries(t, tenant, start, end, "day")

	if len(ts.Buckets) != 3 {
		t.Fatalf("buckets = %d, want 3 (got %+v)", len(ts.Buckets), ts.Buckets)
	}
	wants := []struct {
		start  time.Time
		amount float64
		count  int64
	}{
		{time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), 3.0, 2},
		{time.Date(2026, 1, 6, 0, 0, 0, 0, time.UTC), 5.0, 1},
		{time.Date(2026, 1, 7, 0, 0, 0, 0, time.UTC), 7.0, 1},
	}
	for i, w := range wants {
		got := ts.Buckets[i]
		if !got.Start.Equal(w.start) {
			t.Errorf("bucket[%d].Start = %v, want %v", i, got.Start, w.start)
		}
		if got.Amount < w.amount-0.01 || got.Amount > w.amount+0.01 {
			t.Errorf("bucket[%d].Amount = %v, want %v", i, got.Amount, w.amount)
		}
		if got.ChargeCount != w.count {
			t.Errorf("bucket[%d].ChargeCount = %d, want %d", i, got.ChargeCount, w.count)
		}
	}
}

// TestBilling_GetTenantTimeSeries_GranularityValidation: granularities
// outside the closed allowlist (hour|day|week) come back as
// InvalidArgument before any SQL fires.
func TestBilling_GetTenantTimeSeries_GranularityValidation(t *testing.T) {
	t.Parallel()
	f := setupBilling(t)
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "bil-gran")
	_, err := f.handler.GetTenantTimeSeries(ctxAdmin(t, tenant), tenant,
		time.Now().Add(-time.Hour), time.Now(), "minute")
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", connect.CodeOf(err))
	}
}

// TestBilling_TenantIsolation: a summary query for tenant A must not
// pick up tenant B's charges.
func TestBilling_TenantIsolation(t *testing.T) {
	t.Parallel()
	f := setupBilling(t)

	tenantA := mustCreateTenant(t, f.h.PoolMigrate, "bil-iso-a")
	tenantB := mustCreateTenant(t, f.h.PoolMigrate, "bil-iso-b")
	capA := f.seedCapability(t, tenantA, "agent-a")
	capB := f.seedCapability(t, tenantB, "agent-b")
	f.charge(t, capA, 5.0, "USD", "get", "agent-a", tenantA)
	f.charge(t, capB, 99.0, "USD", "put", "agent-b", tenantB)

	end := time.Now().UTC().Add(time.Hour)
	start := end.Add(-24 * time.Hour)
	a := f.summary(t, tenantA, start, end)
	if a.TotalAmount < 4.99 || a.TotalAmount > 5.01 {
		t.Errorf("tenant A total = %v, want 5.0", a.TotalAmount)
	}
	if a.ChargeCount != 1 {
		t.Errorf("tenant A count = %d, want 1", a.ChargeCount)
	}
}

// TestBilling_EmptyPeriod_ReturnsZeros: a tenant with a budget but no
// charges still gets a well-formed (empty) summary.
func TestBilling_EmptyPeriod_ReturnsZeros(t *testing.T) {
	t.Parallel()
	f := setupBilling(t)
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "bil-empty")
	f.seedBudget(t, tenant, 50, "USD")

	end := time.Now().UTC().Add(time.Hour)
	start := end.Add(-24 * time.Hour)
	s := f.summary(t, tenant, start, end)

	if s.TotalAmount != 0 {
		t.Errorf("total = %v, want 0", s.TotalAmount)
	}
	if s.ChargeCount != 0 {
		t.Errorf("count = %d, want 0", s.ChargeCount)
	}
	if s.UnitCode != "USD" {
		t.Errorf("unit = %q, want USD (from budget fallback)", s.UnitCode)
	}
	if got, want := s.MaxBudgetAmount, 50.0; got < want-0.01 || got > want+0.01 {
		t.Errorf("max budget = %v, want 50", got)
	}
	if len(s.TopCapabilities) != 0 || len(s.TopActors) != 0 || len(s.TopOps) != 0 {
		t.Errorf("top lists not empty: caps=%d actors=%d ops=%d",
			len(s.TopCapabilities), len(s.TopActors), len(s.TopOps))
	}
}

// TestBilling_MixedCurrencyTenant_DominantUnitWins: when a tenant has
// mixed-unit charges, the summary's UnitCode is the row-count
// dominant unit (USD here, 5 vs 3).
//
// Mixed-currency tenants are a deliberately unsupported configuration
// — there's no FX layer to convert across. This test pins the
// current dominant-unit behaviour so a future regression that
// silently sums across units is caught.
func TestBilling_MixedCurrencyTenant_DominantUnitWins(t *testing.T) {
	t.Parallel()
	f := setupBilling(t)

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "bil-mixed")
	cap := f.seedCapability(t, tenant, "agent")
	for i := 0; i < 5; i++ {
		f.chargeAt(t, cap, tenant, time.Now().UTC().Add(-time.Hour), 1.0, "USD", "get", "agent")
	}
	for i := 0; i < 3; i++ {
		f.chargeAt(t, cap, tenant, time.Now().UTC().Add(-time.Hour), 1.0, "EUR", "get", "agent")
	}
	end := time.Now().UTC().Add(time.Hour)
	start := end.Add(-24 * time.Hour)
	s := f.summary(t, tenant, start, end)

	if s.UnitCode != "USD" {
		t.Errorf("unit = %q, want USD (dominant)", s.UnitCode)
	}
}

// TestBilling_PeriodFiltering_ExcludesOutsideRange: 5 charges seeded,
// 2 inside the period window and 3 outside. The summary must reflect
// only the 2 inside.
func TestBilling_PeriodFiltering_ExcludesOutsideRange(t *testing.T) {
	t.Parallel()
	f := setupBilling(t)

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "bil-period")
	cap := f.seedCapability(t, tenant, "agent")

	periodStart := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 3, 31, 23, 59, 59, 0, time.UTC)

	// inside (2)
	f.chargeAt(t, cap, tenant, time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC), 7.0, "USD", "get", "agent")
	f.chargeAt(t, cap, tenant, time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC), 3.0, "USD", "get", "agent")
	// outside (3)
	f.chargeAt(t, cap, tenant, time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC), 100.0, "USD", "get", "agent")
	f.chargeAt(t, cap, tenant, time.Date(2026, 4, 1, 0, 0, 1, 0, time.UTC), 100.0, "USD", "get", "agent")
	f.chargeAt(t, cap, tenant, time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC), 100.0, "USD", "get", "agent")

	s := f.summary(t, tenant, periodStart, periodEnd)
	if s.ChargeCount != 2 {
		t.Errorf("count = %d, want 2", s.ChargeCount)
	}
	if got, want := s.TotalAmount, 10.0; got < want-0.01 || got > want+0.01 {
		t.Errorf("total = %v, want 10.0", got)
	}
}
