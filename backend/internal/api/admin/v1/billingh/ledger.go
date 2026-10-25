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
	"github.com/oleg-tkachuk/limes/meter"
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
var ledgerBuckets = map[string]meter.ChargeBucket{
	granularityHour: meter.ChargeBucketHour,
	granularityDay:  meter.ChargeBucketDay,
	granularityWeek: meter.ChargeBucketWeek,
}

// granularitySteps is each granularity's bucket width.
var granularitySteps = map[string]time.Duration{
	granularityHour: time.Hour,
	granularityDay:  24 * time.Hour,
	granularityWeek: daysPerWeek * 24 * time.Hour,
}

// ledgerPage is the page the ledger is read in: the most one page holds.
const ledgerPage = limes.MaxListLimit

// sumCharges reads req's sums page by page, through meter's walk, handing
// each to keep until keep returns false or the pages end.
func (h *Handler) sumCharges(ctx context.Context, req meter.SumChargesRequest, keep func(meter.ChargeSum) bool) error {
	req.Limit = ledgerPage
	for s, err := range meter.AllChargeSums(ctx, h.ledger, req) {
		if err != nil {
			return rpcerr.New(connect.CodeInternal, fmt.Errorf("billing: sum charges: %w", err))
		}
		if !keep(s) {
			return nil
		}
	}
	return nil
}

// net is what a group's charges took less what was refunded from them.
func net(s meter.ChargeSum) limes.Nanos { return s.Charged - s.Refunded }

// dominant is the sum of the unit with the most charges among sums, which
// hold one unit each and no other grouping; ties go to the lower unit code,
// so the answer does not depend on the order the ledger returns them in.
// ok is false when sums is empty.
func dominant(sums []meter.ChargeSum) (best meter.ChargeSum, ok bool) {
	for _, s := range sums {
		if !ok || s.Charges > best.Charges || (s.Charges == best.Charges && s.UnitCode < best.UnitCode) {
			best, ok = s, true
		}
	}
	return best, ok
}

// series is the time series of the unit with the most charges among sums —
// the ledger's sums by bucket and unit — in time order.
func series(sums []meter.ChargeSum) (string, []TimeBucket) {
	byUnit := map[string]meter.ChargeSum{}
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
