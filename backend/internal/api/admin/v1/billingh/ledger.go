package billingh

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
)

// The granularities GetTenantTimeSeries takes (billing_service.proto names
// them). Bigger windows (month / year) are deliberately excluded — charts at
// that resolution come from a different dashboard surface.
const (
	granularityHour = "hour"
	granularityDay  = "day"
	granularityWeek = "week"
)

// daysPerWeek is the days a week bucket sums.
const daysPerWeek = 7

// ledgerBuckets is the ledger's bucket each granularity is read in, cut at
// UTC boundaries; a week starts on Monday, as PostgreSQL's date_trunc('week')
// cuts it.
var ledgerBuckets = map[string]limes.ChargeBucket{
	granularityHour: limes.ChargeBucketHour,
	granularityDay:  limes.ChargeBucketDay,
	granularityWeek: limes.ChargeBucketWeek,
}

// granularitySteps is each granularity's bucket width.
var granularitySteps = map[string]time.Duration{
	granularityHour: time.Hour,
	granularityDay:  24 * time.Hour,
	granularityWeek: daysPerWeek * 24 * time.Hour,
}

// ledgerPage is the page the ledger is read in: the most one page holds.
const ledgerPage = limes.MaxListLimit

// sumCharges reads req's sums page by page, handing each to keep until keep
// returns false or the pages end.
func (h *Handler) sumCharges(ctx context.Context, req limes.SumChargesRequest, keep func(limes.ChargeSum) bool) error {
	req.Limit = ledgerPage
	for {
		page, err := h.ledger.SumCharges(ctx, req)
		if err != nil {
			return rpcerr.New(connect.CodeInternal, fmt.Errorf("billing: sum charges: %w", err))
		}
		for _, s := range page.Items {
			if !keep(s) {
				return nil
			}
		}
		if page.Next == "" {
			return nil
		}
		req.Cursor = page.Next
	}
}

// net is what a group's charges took less what was refunded from them.
func net(s limes.ChargeSum) limes.Nanos { return s.Charged - s.Refunded }

// dominant is the sum of the unit with the most charges among sums, which
// hold one unit each and no other grouping; ties go to the lower unit code,
// so the answer does not depend on the order the ledger returns them in.
// ok is false when sums is empty.
func dominant(sums []limes.ChargeSum) (best limes.ChargeSum, ok bool) {
	for _, s := range sums {
		if !ok || s.Charges > best.Charges || (s.Charges == best.Charges && s.UnitCode < best.UnitCode) {
			best, ok = s, true
		}
	}
	return best, ok
}

// series is the time series of the unit with the most charges among sums —
// the ledger's sums by bucket and unit — in time order.
func series(sums []limes.ChargeSum) (string, []TimeBucket) {
	byUnit := map[string]limes.ChargeSum{}
	for _, s := range sums {
		u := byUnit[s.UnitCode]
		u.UnitCode = s.UnitCode
		u.Charges += s.Charges
		byUnit[s.UnitCode] = u
	}
	unit, ok := dominant(slices.Collect(maps.Values(byUnit)))
	if !ok {
		return "", []TimeBucket{}
	}
	buckets := map[time.Time]*TimeBucket{}
	for _, s := range sums {
		if s.UnitCode != unit.UnitCode {
			continue
		}
		at := s.BucketStart
		b, ok := buckets[at]
		if !ok {
			b = &TimeBucket{Start: at}
			buckets[at] = b
		}
		b.Amount += net(s)
		b.ChargeCount += s.Charges
	}
	out := make([]TimeBucket, 0, len(buckets))
	for _, b := range buckets {
		out = append(out, *b)
	}
	slices.SortFunc(out, func(a, b TimeBucket) int { return cmp.Compare(a.Start.UnixNano(), b.Start.UnixNano()) })
	return unit.UnitCode, out
}
