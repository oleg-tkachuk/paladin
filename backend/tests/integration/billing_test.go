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
	"uuid"

	"connectrpc.com/connect/v2"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/billingh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	connectshim "github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/admin"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	capabilitypg "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// allowAuth is the permit-everything Cedar authorizer used by the
// billing tests. Mirrors the pattern in
// internal/api/admin/v1/billingh/handler_test.go — the tests target
// the SQL aggregation, not the policy gate, so we short-circuit the
// gate to allow.
type allowAuth struct{}

func (allowAuth) IsAuthorized(_ context.Context, _ *cedar.Principal, _ cedar.Action, _ *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
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
	store := newUsageStore(t, h.PoolMigrate)
	handler := billingh.NewHandler(newLedger(t, h.PoolMigrate), store, allowAuth{})
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

// seedCapability puts a capability on record — the meter refuses one that
// is not — with no budget ceiling of its own, so only the tenant's bounds it.
func (f *billingFixture) seedCapability(t *testing.T, tenant uuid.UUID, subject string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	seedTestCapability(t, f.h, id, tenant, subject, limes.BudgetCeiling{Unlimited: true})
	return id
}

// seedBudget upserts a tenant budget cap.
//
// Reads the current version first: Set carries an OCC guard, and a charge may
// already have created the accumulator row, in which case this is an update
// and 0 would be refused. GetTenantBudget on a missing row yields the zero
// value, whose version is 0 — the create case, which is what we want.
func (f *billingFixture) seedBudget(t *testing.T, tenant uuid.UUID, max limes.Nanos, unit string) {
	t.Helper()
	cur, err := f.store.GetTenantBudget(context.Background(), tenant)
	if err != nil && !errors.Is(err, meter.ErrTenantBudgetNotFound) {
		t.Fatalf("read budget: %v", err)
	}
	if _, err := f.store.SetTenantBudget(context.Background(), meter.SetTenantBudgetRequest{
		TenantID:        tenant,
		Ceiling:         limes.BudgetCeiling{Max: max, UnitCode: unit},
		ResetSpend:      true,
		ExpectedVersion: cur.ResourceVersion,
	}); err != nil {
		t.Fatalf("seed budget: %v", err)
	}
}

// charge runs UsageStore.Charge (the production write path) against the
// capability as it is on record. The charge is in the capability's own unit;
// unit and tenant are what the caller expects it to be.
func (f *billingFixture) charge(t *testing.T, capID uuid.UUID, amount limes.Nanos, unit, op, actor string, tenant uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	records, err := capabilitypg.New(f.h.PoolMigrate)
	if err != nil {
		t.Fatalf("capability store: %v", err)
	}
	subject, err := limes.StoredMeterSubject(ctx, records, capID)
	if err != nil {
		t.Fatalf("meter subject: %v", err)
	}
	if subject.UnitCode() != unit || subject.TenantID() != tenant {
		t.Fatalf("capability %s counts in (%s, %s), want (%s, %s)", capID, subject.UnitCode(), subject.TenantID(), unit, tenant)
	}
	if _, err := f.store.Charge(ctx, meter.ChargeRequest{Subject: subject, Amount: amount, Op: limes.Op(op), Actor: actor}, nil); err != nil {
		t.Fatalf("charge: %v", err)
	}
}

// chargeAt charges at an explicit time through the meter, on a clock set to
// when, so tests can backdate charges into bucketed time windows without
// writing pgstore's charges table. unit is what the caller expects the
// capability to count in.
func (f *billingFixture) chargeAt(t *testing.T, capID uuid.UUID, tenant uuid.UUID, when time.Time, amount limes.Nanos, unit, op, actor string) {
	t.Helper()
	ctx := context.Background()
	clock := capabilitypg.WithClock(func() time.Time { return when })
	records, err := capabilitypg.New(f.h.PoolMigrate, clock)
	if err != nil {
		t.Fatalf("capability store: %v", err)
	}
	subject, err := limes.StoredMeterSubject(ctx, records, capID)
	if err != nil {
		t.Fatalf("meter subject: %v", err)
	}
	if subject.UnitCode() != unit || subject.TenantID() != tenant {
		t.Fatalf("capability %s counts in (%s, %s), want (%s, %s)", capID, subject.UnitCode(), subject.TenantID(), unit, tenant)
	}
	usage := newUsageStore(t, f.h.PoolMigrate, clock)
	if _, err := usage.Charge(ctx, meter.ChargeRequest{Subject: subject, Amount: amount, Op: limes.Op(op), Actor: actor}, nil); err != nil {
		t.Fatalf("charge at %v: %v", when, err)
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
// running totals AND inserts the matching ledger row.
func TestBilling_ChargeWritesLedgerRow(t *testing.T) {
	t.Parallel()
	f := setupBilling(t)

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "bil-write")
	f.seedBudget(t, tenant, limes.MustParseAmount("100"), "USD")
	capID := f.seedCapability(t, tenant, "agent-1")
	f.charge(t, capID, limes.MustParseAmount("2.50"), "USD", "presign.put", "agent-1", tenant)

	// the capability's running total
	usage, err := f.store.GetUsage(context.Background(), capID)
	if err != nil {
		t.Fatalf("get usage: %v", err)
	}
	if usage.SpentAmount != limes.MustParseAmount("2.50") {
		t.Errorf("cap spent = %v, want 2.50", usage.SpentAmount)
	}
	if usage.UnitCode != "USD" {
		t.Errorf("cap unit = %q, want USD", usage.UnitCode)
	}

	// the tenant's running total
	tb, err := f.store.GetTenantBudget(context.Background(), tenant)
	if err != nil {
		t.Fatalf("get tenant: %v", err)
	}
	if tb.SpentAmount != limes.MustParseAmount("2.50") {
		t.Errorf("tenant spent = %v, want 2.50", tb.SpentAmount)
	}

	sums := ledgerSums(t, f.h, tenant)
	if len(sums) != 1 || sums[0].Charges != 1 {
		t.Fatalf("ledger = %+v, want one charge", sums)
	}
	if got := sums[0].Charged; got != limes.MustParseAmount("2.50") {
		t.Errorf("ledger amount = %v, want 2.50", got)
	}
	if s := sums[0]; s.UnitCode != "USD" || s.Op != "presign.put" || s.Actor != "agent-1" {
		t.Errorf("ledger metadata = (%q, %q, %q), want (USD, presign.put, agent-1)", s.UnitCode, s.Op, s.Actor)
	}
}

// TestBilling_GetTenantSummary_AggregatesCorrectly: 22 charges across
// 3 caps, 2 ops; verify totals and the top-N breakdowns.
func TestBilling_GetTenantSummary_AggregatesCorrectly(t *testing.T) {
	t.Parallel()
	f := setupBilling(t)

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "bil-agg")
	f.seedBudget(t, tenant, limes.MustParseAmount("500"), "USD")
	capA := f.seedCapability(t, tenant, "agent-A")
	capB := f.seedCapability(t, tenant, "agent-B")
	capC := f.seedCapability(t, tenant, "agent-C")

	for i := 0; i < 10; i++ {
		f.charge(t, capA, limes.MustParseAmount("1.0"), "USD", "get", "agent-A", tenant)
	}
	for i := 0; i < 4; i++ {
		f.charge(t, capB, limes.MustParseAmount("0.5"), "USD", "put", "agent-B", tenant)
	}
	for i := 0; i < 8; i++ {
		f.charge(t, capC, limes.MustParseAmount("0.25"), "USD", "get", "agent-C", tenant)
	}

	end := time.Now().UTC().Add(time.Hour)
	start := end.Add(-30 * 24 * time.Hour)
	s := f.summary(t, tenant, start, end)

	if got, want := s.TotalAmount, limes.MustParseAmount("14.0"); got != want {
		t.Errorf("total = %v, want 14.0", got)
	}
	if s.ChargeCount != 22 {
		t.Errorf("count = %d, want 22", s.ChargeCount)
	}
	if s.UnitCode != "USD" {
		t.Errorf("unit = %q, want USD", s.UnitCode)
	}
	if want := (limes.BudgetCeiling{Max: limes.MustParseAmount("500"), UnitCode: seedUnitCode}); s.Ceiling == nil || *s.Ceiling != want {
		t.Errorf("ceiling = %v, want %s", s.Ceiling, want)
	}

	// top_capabilities: A=10, then B and C tied at 2.0
	if len(s.TopCapabilities) != 3 {
		t.Fatalf("top caps len = %d, want 3", len(s.TopCapabilities))
	}
	if s.TopCapabilities[0].Label != capA.String() {
		t.Errorf("top cap[0] = %q, want %q", s.TopCapabilities[0].Label, capA)
	}
	if got := s.TopCapabilities[0].Amount; got != limes.MustParseAmount("10.0") {
		t.Errorf("top cap[0] amount = %v, want 10.0", got)
	}
	// caps[1] and caps[2] are B/C in either order; just check both appear.
	tail := map[string]limes.Nanos{
		s.TopCapabilities[1].Label: s.TopCapabilities[1].Amount,
		s.TopCapabilities[2].Label: s.TopCapabilities[2].Amount,
	}
	if v, ok := tail[capB.String()]; !ok || v != limes.MustParseAmount("2.0") {
		t.Errorf("cap B missing or wrong amount in tail: %v", tail)
	}
	if v, ok := tail[capC.String()]; !ok || v != limes.MustParseAmount("2.0") {
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
	if got := s.TopOps[0].Amount; got != limes.MustParseAmount("12") {
		t.Errorf("top op[0] amount = %v, want 12", got)
	}
	if s.TopOps[1].Label != "put" || s.TopOps[1].ChargeCount != 4 {
		t.Errorf("top op[1] = %+v, want {put, 4}", s.TopOps[1])
	}
	if got := s.TopOps[1].Amount; got != limes.MustParseAmount("2") {
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
	f.chargeAt(t, cap, tenant, day1, limes.MustParseAmount("1.0"), "USD", "get", "agent")
	f.chargeAt(t, cap, tenant, day1, limes.MustParseAmount("2.0"), "USD", "get", "agent") // day1 = 3.0
	f.chargeAt(t, cap, tenant, day2, limes.MustParseAmount("5.0"), "USD", "put", "agent") // day2 = 5.0
	f.chargeAt(t, cap, tenant, day3, limes.MustParseAmount("7.0"), "USD", "get", "agent") // day3 = 7.0

	start := day1.Add(-time.Hour)
	end := day3.Add(2 * time.Hour)
	ts := f.timeSeries(t, tenant, start, end, "day")

	if len(ts.Buckets) != 3 {
		t.Fatalf("buckets = %d, want 3 (got %+v)", len(ts.Buckets), ts.Buckets)
	}
	wants := []struct {
		start  time.Time
		amount limes.Nanos
		count  int64
	}{
		{time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), limes.MustParseAmount("3.0"), 2},
		{time.Date(2026, 1, 6, 0, 0, 0, 0, time.UTC), limes.MustParseAmount("5.0"), 1},
		{time.Date(2026, 1, 7, 0, 0, 0, 0, time.UTC), limes.MustParseAmount("7.0"), 1},
	}
	for i, w := range wants {
		got := ts.Buckets[i]
		if !got.Start.Equal(w.start) {
			t.Errorf("bucket[%d].Start = %v, want %v", i, got.Start, w.start)
		}
		if got.Amount != w.amount {
			t.Errorf("bucket[%d].Amount = %v, want %v", i, got.Amount, w.amount)
		}
		if got.ChargeCount != w.count {
			t.Errorf("bucket[%d].ChargeCount = %d, want %d", i, got.ChargeCount, w.count)
		}
	}
}

// TestBilling_GetTenantTimeSeries_BucketsByWeek: a Sunday and the Monday
// after it fall in two ISO weeks, each starting Monday 00:00 UTC.
func TestBilling_GetTenantTimeSeries_BucketsByWeek(t *testing.T) {
	t.Parallel()
	f := setupBilling(t)

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "bil-week")
	cap := f.seedCapability(t, tenant, "agent")
	sunday := time.Date(2026, 1, 4, 12, 0, 0, 0, time.UTC)
	monday := time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC)
	f.chargeAt(t, cap, tenant, sunday, limes.MustParseAmount("2.0"), "USD", "get", "agent")
	f.chargeAt(t, cap, tenant, monday, limes.MustParseAmount("12.0"), "USD", "get", "agent")

	ts := f.timeSeries(t, tenant, sunday.Add(-time.Hour), monday.Add(time.Hour), "week")
	want := []billingh.TimeBucket{
		{Start: time.Date(2025, 12, 29, 0, 0, 0, 0, time.UTC), Amount: limes.MustParseAmount("2.0"), ChargeCount: 1},
		{Start: time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), Amount: limes.MustParseAmount("12.0"), ChargeCount: 1},
	}
	if len(ts.Buckets) != len(want) {
		t.Fatalf("buckets = %+v, want %+v", ts.Buckets, want)
	}
	for i, w := range want {
		if got := ts.Buckets[i]; !got.Start.Equal(w.Start) || got.Amount != w.Amount || got.ChargeCount != w.ChargeCount {
			t.Errorf("bucket[%d] = %+v, want %+v", i, got, w)
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
	f.charge(t, capA, limes.MustParseAmount("5.0"), "USD", "get", "agent-a", tenantA)
	f.charge(t, capB, limes.MustParseAmount("99.0"), "USD", "put", "agent-b", tenantB)

	end := time.Now().UTC().Add(time.Hour)
	start := end.Add(-24 * time.Hour)
	a := f.summary(t, tenantA, start, end)
	if a.TotalAmount != limes.MustParseAmount("5.0") {
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
	f.seedBudget(t, tenant, limes.MustParseAmount("50"), "USD")

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
	if want := (limes.BudgetCeiling{Max: limes.MustParseAmount("50"), UnitCode: seedUnitCode}); s.Ceiling == nil || *s.Ceiling != want {
		t.Errorf("ceiling = %v, want %s", s.Ceiling, want)
	}
	if len(s.TopCapabilities) != 0 || len(s.TopActors) != 0 || len(s.TopOps) != 0 {
		t.Errorf("top lists not empty: caps=%d actors=%d ops=%d",
			len(s.TopCapabilities), len(s.TopActors), len(s.TopOps))
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
	f.chargeAt(t, cap, tenant, time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC), limes.MustParseAmount("7.0"), "USD", "get", "agent")
	f.chargeAt(t, cap, tenant, time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC), limes.MustParseAmount("3.0"), "USD", "get", "agent")
	// outside (3)
	f.chargeAt(t, cap, tenant, time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC), limes.MustParseAmount("100.0"), "USD", "get", "agent")
	f.chargeAt(t, cap, tenant, time.Date(2026, 4, 1, 0, 0, 1, 0, time.UTC), limes.MustParseAmount("100.0"), "USD", "get", "agent")
	f.chargeAt(t, cap, tenant, time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC), limes.MustParseAmount("100.0"), "USD", "get", "agent")

	s := f.summary(t, tenant, periodStart, periodEnd)
	if s.ChargeCount != 2 {
		t.Errorf("count = %d, want 2", s.ChargeCount)
	}
	if got, want := s.TotalAmount, limes.MustParseAmount("10.0"); got != want {
		t.Errorf("total = %v, want 10.0", got)
	}
}

// TestBilling_MoneyIsExact drives the Connect adapter over real charges
// whose amounts are not float64 values (0.1, 0.2): the ledger sums them in
// numeric, and the Money fields must carry that sum exactly — 0.1 + 0.2 is
// 300000000 nanos, not 0.30000000000000004 units rounded somewhere later.
func TestBilling_MoneyIsExact(t *testing.T) {
	t.Parallel()
	f := setupBilling(t)
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "bil-money")
	f.seedBudget(t, tenant, limes.MustParseAmount("19.99"), "USD")
	capID := f.seedCapability(t, tenant, "agent-M")
	for range 3 {
		f.charge(t, capID, limes.MustParseAmount("0.1"), "USD", "get", "agent-M", tenant)
	}
	f.charge(t, capID, limes.MustParseAmount("0.2"), "USD", "put", "agent-M", tenant)

	srv := connectshim.NewBillingServer(f.handler, nil)
	end := time.Now().UTC().Add(time.Hour)
	resp, err := srv.GetTenantSummary(ctxAdmin(t, tenant), &pb.GetTenantSummaryRequest{
		TenantId:    tenant.String(),
		PeriodStart: timestamppb.New(end.Add(-24 * time.Hour)),
		PeriodEnd:   timestamppb.New(end),
	})
	if err != nil {
		t.Fatalf("GetTenantSummary: %v", err)
	}
	if got, want := resp.GetTotal(), (&money.Money{CurrencyCode: "USD", Nanos: 500_000_000}); !proto.Equal(got, want) {
		t.Errorf("total = %v, want %v", got, want)
	}
	if got, want := resp.GetMaxBudget(), (&money.Money{CurrencyCode: "USD", Units: 19, Nanos: 990_000_000}); !proto.Equal(got, want) {
		t.Errorf("max_budget = %v, want %v", got, want)
	}
	if resp.GetUnlimitedBudget() != nil {
		t.Errorf("a capped tenant reads as unlimited: %v", resp.GetUnlimitedBudget())
	}
	var top limes.Nanos
	for _, e := range resp.GetTopOps() {
		n, _, err := apiutil.NanosOf("spent", e.GetSpent())
		if err != nil {
			t.Fatalf("top op %q: %v", e.GetLabel(), err)
		}
		top += n
	}
	if want := limes.MustParseAmount("0.5"); top != want {
		t.Errorf("top ops sum to %v, want %v", top, want)
	}
}

// A platform admin reads another tenant's billing through the pool the
// server uses, row-level security and all: the handler must act on the
// tenant the request names, or the policies hide every row of it and the
// summary reads as zero with nothing saying why.
func TestBilling_AdminReadsAnotherTenantUnderRLS(t *testing.T) {
	t.Parallel()
	f := setupBilling(t)
	appStore := newUsageStore(t, f.h.PoolApp)
	handler := billingh.NewHandler(newLedger(t, f.h.PoolApp), appStore, allowAuth{})

	home := mustCreateTenant(t, f.h.PoolMigrate, "bil-rls-home")
	other := mustCreateTenant(t, f.h.PoolMigrate, "bil-rls-other")
	capOther := f.seedCapability(t, other, "agent-other")
	f.seedBudget(t, other, limes.MustParseAmount("12.000000001"), "USD")
	f.charge(t, capOther, limes.MustParseAmount("0.3"), "USD", "get", "agent-other", other)

	end := time.Now().UTC().Add(time.Hour)
	start := end.Add(-24 * time.Hour)
	ctx := ctxAdmin(t, home)
	sum, err := handler.GetTenantSummary(ctx, other, start, end)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if sum.TotalAmount != limes.MustParseAmount("0.3") || sum.ChargeCount != 1 {
		t.Errorf("summary of another tenant = %s over %d charges, want 0.3 over 1", sum.TotalAmount, sum.ChargeCount)
	}
	if want := (limes.BudgetCeiling{Max: limes.MustParseAmount("12.000000001"), UnitCode: seedUnitCode}); sum.Ceiling == nil || *sum.Ceiling != want {
		t.Errorf("summary budget = %v, want %s", sum.Ceiling, want)
	}
	ts, err := handler.GetTenantTimeSeries(ctx, other, start, end, "day")
	if err != nil {
		t.Fatalf("time series: %v", err)
	}
	var total limes.Nanos
	for _, b := range ts.Buckets {
		total += b.Amount
	}
	if total != limes.MustParseAmount("0.3") {
		t.Errorf("time series of another tenant sums to %s, want 0.3", total)
	}
}

// The summary carries the budget on its own arm: unset with no budget row,
// unlimited in the tenant's unit, never a 0 standing for either.
func TestBilling_SummaryCeilingArms(t *testing.T) {
	t.Parallel()
	f := setupBilling(t)
	srv := connectshim.NewBillingServer(f.handler, nil)
	summary := func(tenant uuid.UUID) *pb.GetTenantSummaryResponse {
		t.Helper()
		resp, err := srv.GetTenantSummary(ctxAdmin(t, tenant), &pb.GetTenantSummaryRequest{TenantId: tenant.String()})
		if err != nil {
			t.Fatalf("GetTenantSummary: %v", err)
		}
		return resp
	}

	none := mustCreateTenant(t, f.h.PoolMigrate, "bil-nobudget")
	if c := summary(none).GetBudgetCeiling(); c != nil {
		t.Errorf("no budget row: ceiling = %v, want unset", c)
	}

	open := mustCreateTenant(t, f.h.PoolMigrate, "bil-unlimited")
	if _, err := f.store.SetTenantBudget(context.Background(), meter.SetTenantBudgetRequest{
		TenantID: open, Ceiling: limes.BudgetCeiling{Unlimited: true, UnitCode: "EUR"},
	}); err != nil {
		t.Fatalf("seed budget: %v", err)
	}
	if got := summary(open).GetUnlimitedBudget().GetCurrencyCode(); got != "EUR" {
		t.Errorf("unlimited: currency = %q, want EUR", got)
	}
}

// A platform admin lists every tenant's spend through the pool the server
// uses, row-level security and all, from one sum across tenants: the read
// must be scoped to every tenant, read-only, or the policies hide the other
// tenants' rows and the list holds the caller's alone. Other tests charge
// the same ledger, so only these tenants' rows and order are checked.
func TestBilling_AdminListsEveryTenantsSpendUnderRLS(t *testing.T) {
	t.Parallel()
	f := setupBilling(t)
	handler := billingh.NewHandler(newLedger(t, f.h.PoolApp), newUsageStore(t, f.h.PoolApp), allowAuth{}).WithCurrency(seedUnitCode)

	home := mustCreateTenant(t, f.h.PoolMigrate, "bil-spend-home")
	more := mustCreateTenant(t, f.h.PoolMigrate, "bil-spend-more")
	less := mustCreateTenant(t, f.h.PoolMigrate, "bil-spend-less")
	f.charge(t, f.seedCapability(t, more, "agent-more"), limes.MustParseAmount("2.5"), seedUnitCode, "get", "agent-more", more)
	f.charge(t, f.seedCapability(t, less, "agent-less"), limes.MustParseAmount("0.4"), seedUnitCode, "get", "agent-less", less)

	end := time.Now().UTC().Add(time.Hour)
	spend, _, err := handler.ListTenantSpend(ctxAdmin(t, home), end.Add(-24*time.Hour), end, billingh.MaxSpendLimit)
	if err != nil {
		t.Fatalf("list tenant spend: %v", err)
	}
	at := map[uuid.UUID]int{}
	for i, s := range spend {
		at[s.TenantID] = i
		switch s.TenantID {
		case more:
			if s.Amount != limes.MustParseAmount("2.5") || s.ChargeCount != 1 || s.UnitCode != seedUnitCode {
				t.Errorf("the tenant that spent more = %+v", s)
			}
		case less:
			if s.Amount != limes.MustParseAmount("0.4") || s.ChargeCount != 1 {
				t.Errorf("the tenant that spent less = %+v", s)
			}
		case home:
			t.Error("the caller's tenant, which spent nothing, is listed")
		}
	}
	iMore, okMore := at[more]
	iLess, okLess := at[less]
	if !okMore || !okLess || iMore > iLess {
		t.Errorf("positions: more %d (%v), less %d (%v); want both listed, the larger first", iMore, okMore, iLess, okLess)
	}
}
