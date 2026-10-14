package billingh

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/limes"
)

// fakeLedger answers SumCharges from sums, pageSize at a time, and records
// each request.
type fakeLedger struct {
	sums     func(limes.SumChargesRequest) []limes.ChargeSum
	pageSize int
	err      error
	requests []limes.SumChargesRequest
}

func (f *fakeLedger) SumCharges(_ context.Context, req limes.SumChargesRequest) (limes.Page[limes.ChargeSum], error) {
	f.requests = append(f.requests, req)
	if f.err != nil {
		return limes.Page[limes.ChargeSum]{}, f.err
	}
	all := f.sums(req)
	from := 0
	if req.Cursor != "" {
		from, _ = strconv.Atoi(req.Cursor)
	}
	size := f.pageSize
	if size == 0 {
		size = req.Limit
	}
	to := min(from+size, len(all))
	page := limes.Page[limes.ChargeSum]{Items: all[from:to]}
	if to < len(all) {
		page.Next = strconv.Itoa(to)
	}
	return page, nil
}

const (
	usd = "USD"
	eur = "EUR"
)

// units is n whole units.
func units(n int64) limes.Nanos { return limes.Nanos(n) * limes.NanosPerUnit }

// The summary is in the unit most charges are in, net of refunds, and each
// top list holds that unit's largest groups, however many of another unit
// the ledger returns ahead of them.
func TestSummaryIsInTheDominantUnit(t *testing.T) {
	capA, capB := uuid.New(), uuid.New()
	ledger := &fakeLedger{pageSize: 1, sums: func(r limes.SumChargesRequest) []limes.ChargeSum {
		switch {
		case r.ByCapability:
			return []limes.ChargeSum{
				{UnitCode: eur, CapabilityID: capB, Charged: units(50), Charges: 2},
				{UnitCode: usd, CapabilityID: capA, Charged: units(7), Refunded: units(1), Charges: 4},
				{UnitCode: usd, CapabilityID: capB, Charged: units(2), Charges: 1},
			}
		case r.ByActor:
			return []limes.ChargeSum{{UnitCode: usd, Actor: "agent", Charged: units(9), Refunded: units(1), Charges: 5}}
		case r.ByOp:
			return []limes.ChargeSum{{UnitCode: usd, Op: limes.OpGet, Charged: units(9), Refunded: units(1), Charges: 5}}
		}
		return []limes.ChargeSum{
			{UnitCode: eur, Charged: units(50), Charges: 2},
			{UnitCode: usd, Charged: units(9), Refunded: units(1), Charges: 5},
		}
	}}
	tenant := uuid.New()
	s, err := NewHandler(ledger, nil, allowAuthorizer{}).GetTenantSummary(ctxWithAdmin(t), tenant, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if s.UnitCode != usd || s.TotalAmount != units(8) || s.ChargeCount != 5 {
		t.Errorf("summary = %s %v over %d charges, want USD 8 over 5", s.UnitCode, s.TotalAmount, s.ChargeCount)
	}
	want := []TopEntry{{Label: capA.String(), Amount: units(6), ChargeCount: 4}, {Label: capB.String(), Amount: units(2), ChargeCount: 1}}
	if !slices.Equal(s.TopCapabilities, want) {
		t.Errorf("top capabilities = %+v, want %+v", s.TopCapabilities, want)
	}
	if len(s.TopOps) != 1 || s.TopOps[0].Label != string(limes.OpGet) {
		t.Errorf("top ops = %+v, want get", s.TopOps)
	}
	for _, r := range ledger.requests {
		if r.TenantID != tenant {
			t.Errorf("the ledger was asked for tenant %s, want %s", r.TenantID, tenant)
		}
	}
}

// A top list stops reading the ledger once it holds topN groups.
func TestTopListStopsAtTopN(t *testing.T) {
	many := make([]limes.ChargeSum, 0, 3*topN)
	for i := range 3 * topN {
		many = append(many, limes.ChargeSum{UnitCode: usd, Actor: strconv.Itoa(i), Charged: units(1), Charges: 1})
	}
	ledger := &fakeLedger{pageSize: topN, sums: func(limes.SumChargesRequest) []limes.ChargeSum { return many }}
	h := NewHandler(ledger, nil, allowAuthorizer{})
	got, err := h.topBy(context.Background(), limes.SumChargesRequest{ByActor: true}, usd, func(s limes.ChargeSum) string { return s.Actor })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != topN || len(ledger.requests) != 1 {
		t.Errorf("%d entries over %d pages, want %d over 1", len(got), len(ledger.requests), topN)
	}
}

// A period with no charge reads no top list, and is in the platform's
// currency when the tenant has no budget either.
func TestSummaryOfAnEmptyPeriod(t *testing.T) {
	ledger := &fakeLedger{sums: func(limes.SumChargesRequest) []limes.ChargeSum { return nil }}
	s, err := NewHandler(ledger, nil, allowAuthorizer{}).WithCurrency(eur).GetTenantSummary(ctxWithAdmin(t), uuid.New(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if s.UnitCode != eur || s.ChargeCount != 0 || len(ledger.requests) != 1 {
		t.Errorf("summary = %+v after %d reads, want EUR and nothing after 1", s, len(ledger.requests))
	}
}

// A ledger that fails is the server's failure, whatever it failed with —
// a scope mismatch included.
func TestSummaryOfAFailedLedgerIsInternal(t *testing.T) {
	ledger := &fakeLedger{err: limes.ErrScopeMismatch}
	_, err := NewHandler(ledger, nil, allowAuthorizer{}).GetTenantSummary(ctxWithAdmin(t), uuid.New(), time.Time{}, time.Time{})
	if connect.CodeOf(err) != connect.CodeInternal || !errors.Is(err, limes.ErrScopeMismatch) {
		t.Errorf("err = %v, want Internal wrapping ErrScopeMismatch", err)
	}
}

// Weeks are the ledger's own week buckets; the series is the dominant
// unit's, in time order, net of refunds.
func TestTimeSeriesByWeek(t *testing.T) {
	week1 := time.Date(2025, 12, 29, 0, 0, 0, 0, time.UTC) // a Monday
	week2 := week1.AddDate(0, 0, daysPerWeek)
	ledger := &fakeLedger{pageSize: 1, sums: func(limes.SumChargesRequest) []limes.ChargeSum {
		return []limes.ChargeSum{
			{BucketStart: week1, UnitCode: usd, Charged: units(2), Charges: 1},
			{BucketStart: week1, UnitCode: eur, Charged: units(99), Charges: 1},
			{BucketStart: week2, UnitCode: usd, Charged: units(8), Refunded: units(1), Charges: 3},
		}
	}}
	ts, err := NewHandler(ledger, nil, allowAuthorizer{}).GetTenantTimeSeries(ctxWithAdmin(t), uuid.New(), week1, week2.AddDate(0, 0, daysPerWeek), granularityWeek)
	if err != nil {
		t.Fatal(err)
	}
	want := []TimeBucket{
		{Start: week1, Amount: units(2), ChargeCount: 1},
		{Start: week2, Amount: units(7), ChargeCount: 3},
	}
	if ts.UnitCode != usd || !slices.Equal(ts.Buckets, want) {
		t.Errorf("series = %s %+v, want USD %+v", ts.UnitCode, ts.Buckets, want)
	}
	if ledger.requests[0].Bucket != limes.ChargeBucketWeek {
		t.Errorf("weeks read in %s buckets, want weeks", ledger.requests[0].Bucket)
	}
}

// Every granularity the RPC takes has a ledger bucket and a width.
func TestEveryGranularityIsComplete(t *testing.T) {
	for g := range ledgerBuckets {
		if granularitySteps[g] == 0 {
			t.Errorf("granularity %q has no width", g)
		}
	}
	if len(granularitySteps) != len(ledgerBuckets) {
		t.Error("the granularity tables disagree")
	}
}
