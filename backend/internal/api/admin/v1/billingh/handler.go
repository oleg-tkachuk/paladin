// Package billingh implements the admin BillingService — read-only
// aggregation over the charges ledger, read through meter.LedgerReader
// (pgstore's Ledger) rather than the ledger's table, whose columns are
// pgstore's own.
package billingh

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// defaultPeriod is the lookback when the caller omits period_start /
// period_end. Picked as 30d to match the typical billing close.
const defaultPeriod = 30 * 24 * time.Hour

// topN caps each breakdown list. 10 is enough for the dashboard tile;
// callers that need more can paginate via a future RPC.
const topN = 10

// Handler is the BillingService backend: the charges ledger, the Cedar gate,
// and a UsageStore for the tenant budget cap join.
type Handler struct {
	ledger meter.LedgerReader
	usage  meter.UsageStore
	policy cedar.Authorizer
	// currency is the platform's (capability.currency): what a summary with
	// no charge and no budget to take a unit from is in. See WithCurrency.
	currency string
}

// WithCurrency sets the platform's currency, capability.currency. Without it
// an empty summary is in apiutil.NoUnit.
func (h *Handler) WithCurrency(unit string) *Handler {
	h.currency = unit
	return h
}

// NewHandler wires the dependencies. policy MUST be non-nil; ledger may
// be nil only when the capability subsystem is disabled (the listener
// builder still needs to register a stub so the BFF allowlist doesn't
// 404).
func NewHandler(ledger meter.LedgerReader, usage meter.UsageStore, policy cedar.Authorizer) *Handler {
	if policy == nil {
		panic("billingh: policy authorizer is required")
	}
	return &Handler{ledger: ledger, usage: usage, policy: policy, currency: apiutil.NoUnit}
}

// authorize gates the RPC against Cedar. Resource is the Tenant
// entity carrying the queried tenant_id, so policies can pin
// "tenant.admin reads own tenant only" via resource.tenant_id ==
// principal.tenant_id.
//
// On allow it returns ctx acting on tenantID: the ledger and the budget are
// read under row-level security, and a platform admin reading another
// tenant would otherwise see that tenant's rows filtered to nothing — a
// summary of zeros, with nothing to say why.
func (h *Handler) authorize(ctx context.Context, tenantID uuid.UUID) (context.Context, error) {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipal(p),
		cedar.ActionReadBilling,
		&cedar.Resource{TenantID: tenantID},
		cedar.RequestContext{Now: time.Now()},
	)
	if err != nil {
		return nil, apiutil.MapError(fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return nil, connect.NewError(connect.CodePermissionDenied, "denied by policy")
	}
	return auth.WithActingTenant(ctx, tenantID), nil
}

// tenantBudget reads tenantID's budget for a summary that only decorates
// with it. A tenant with no budget row has none to show; any other failure
// is logged rather than returned, since the ledger figures stand without
// it, but a summary that shows no cap for a tenant that has one must leave
// a trace of why.
func (h *Handler) tenantBudget(ctx context.Context, tenantID uuid.UUID) (meter.TenantBudget, bool) {
	if h.usage == nil {
		return meter.TenantBudget{}, false
	}
	tb, err := h.usage.GetTenantBudget(ctx, tenantID)
	if err != nil {
		if !errors.Is(err, meter.ErrTenantBudgetNotFound) {
			logger.FromContext(ctx).Warn("billing: tenant budget unreadable; summary shows no cap",
				zap.String("tenant_id", tenantID.String()), zap.Error(err))
		}
		return meter.TenantBudget{}, false
	}
	return tb, true
}

// resolvePeriod computes the start/end window. Both args are optional;
// missing → last 30d. start > end → InvalidArgument so the SQL
// doesn't degrade silently to an empty result.
func resolvePeriod(start, end time.Time) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	if end.IsZero() {
		end = now
	}
	if start.IsZero() {
		start = end.Add(-defaultPeriod)
	}
	if start.After(end) {
		return time.Time{}, time.Time{}, fmt.Errorf("period_start must be <= period_end")
	}
	return start, end, nil
}

// Summary is the in-memory shape returned by GetTenantSummary.
type Summary struct {
	TotalAmount limes.Nanos
	UnitCode    string
	// Ceiling is the tenant's budget cap, in CeilingUnit; nil when the
	// tenant has no budget row.
	Ceiling         *limes.BudgetCeiling
	CeilingUnit     string
	ChargeCount     int64
	TopCapabilities []TopEntry
	TopActors       []TopEntry
	TopOps          []TopEntry
}

// TopEntry mirrors the proto shape one-to-one.
type TopEntry struct {
	Label       string
	Amount      limes.Nanos
	ChargeCount int64
}

// GetTenantSummary sums the charges ledger over the period, in the unit most
// of its charges are in: mixed-unit tenants are degenerate today (no FX), and
// a sum across units would mean nothing. Amounts are net of refunds. The
// tenant budget's unit stands in when the period has no charge, then the
// platform's currency. Cedar gate runs first (security before subsystem
// availability); no ledger then short-circuits to Unavailable (capability
// subsystem disabled in this deployment).
func (h *Handler) GetTenantSummary(ctx context.Context, tenantID uuid.UUID, periodStart, periodEnd time.Time) (*Summary, error) {
	ctx, err := h.authorize(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if h.ledger == nil {
		return nil, connect.NewError(connect.CodeUnavailable, "billing: capability subsystem disabled")
	}
	start, end, err := resolvePeriod(periodStart, periodEnd)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}

	out := &Summary{}
	if start.Before(end) {
		period := meter.SumChargesRequest{TenantID: tenantID, From: start, To: end}
		var totals []meter.ChargeSum
		if err := h.sumCharges(ctx, period, func(s meter.ChargeSum) bool {
			totals = append(totals, s)
			return true
		}); err != nil {
			return nil, err
		}
		if total, ok := dominant(totals); ok {
			out.UnitCode, out.TotalAmount, out.ChargeCount = total.UnitCode, net(total), total.Charges
			for _, top := range []struct {
				into  *[]TopEntry
				group func(*meter.SumChargesRequest)
				label func(meter.ChargeSum) string
			}{
				{&out.TopCapabilities, func(r *meter.SumChargesRequest) { r.GroupBy = []meter.ChargeDimension{meter.ChargeDimensionCapability} },
					func(s meter.ChargeSum) string { return s.CapabilityID.String() }},
				{&out.TopActors, func(r *meter.SumChargesRequest) { r.GroupBy = []meter.ChargeDimension{meter.ChargeDimensionActor} },
					func(s meter.ChargeSum) string { return s.Actor }},
				{&out.TopOps, func(r *meter.SumChargesRequest) { r.GroupBy = []meter.ChargeDimension{meter.ChargeDimensionOp} },
					func(s meter.ChargeSum) string { return string(s.Op) }},
			} {
				req := period
				top.group(&req)
				if *top.into, err = h.topBy(ctx, req, out.UnitCode, top.label); err != nil {
					return nil, err
				}
			}
		}
	}

	// Tenant budget cap join — best-effort. Missing row ⇒ out.Ceiling stays
	// nil, which the frontend renders as "no cap configured".
	if tb, ok := h.tenantBudget(ctx, tenantID); ok {
		ceiling := tb.Ceiling
		out.Ceiling, out.CeilingUnit = &ceiling, tb.Ceiling.UnitCode
		if out.UnitCode == "" {
			out.UnitCode = tb.Ceiling.UnitCode
		}
	}
	if out.UnitCode == "" {
		out.UnitCode = h.currency
	}
	return out, nil
}

// topBy is the topN groups req asks for in unit, the largest first, each
// named by label. The ledger hands the groups of every unit back largest
// first, so the first topN in unit are its top.
func (h *Handler) topBy(ctx context.Context, req meter.SumChargesRequest, unit string, label func(meter.ChargeSum) string) ([]TopEntry, error) {
	out := make([]TopEntry, 0, topN)
	err := h.sumCharges(ctx, req, func(s meter.ChargeSum) bool {
		if s.UnitCode == unit {
			out = append(out, TopEntry{Label: label(s), Amount: net(s), ChargeCount: s.Charges})
		}
		return len(out) < topN
	})
	return out, err
}

// TimeSeries is the in-memory shape returned by GetTenantTimeSeries.
type TimeSeries struct {
	Buckets  []TimeBucket
	UnitCode string
}

// TimeBucket mirrors the proto shape.
type TimeBucket struct {
	Start       time.Time
	Amount      limes.Nanos
	ChargeCount int64
}

// granularityStep maps a validated granularity to its bucket width, used to
// pre-flight the bucket count. Returns 0 for an unknown value (the caller has
// already validated it, so 0 only skips the check).
func granularityStep(granularity string) time.Duration {
	return granularitySteps[granularity]
}

// maxTimeSeriesBuckets caps the number of (period ÷ granularity) buckets a
// single GetTenantTimeSeries call may materialise, bounding the buffered result
// set. 1000 is generous for any legitimate chart — hour granularity spans ~41
// days (> the 30d default period), day spans ~2.7 years, week ~19 years — while
// rejecting the pathological "hour granularity over years" request.
const maxTimeSeriesBuckets = 1000

// GetTenantTimeSeries buckets the charges of the unit most of them are in,
// net of refunds, and their count by time, as GetTenantSummary sums them.
// granularity is validated against the closed allowlist before the ledger
// is read.
func (h *Handler) GetTenantTimeSeries(ctx context.Context, tenantID uuid.UUID, periodStart, periodEnd time.Time, granularity string) (*TimeSeries, error) {
	ctx, err := h.authorize(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	// Input validation (granularity + period + bucket bound) runs before the
	// subsystem-availability check so a malformed request is rejected the same
	// way whether or not the capability subsystem is wired.
	start, end, granularity, err := seriesWindow(periodStart, periodEnd, granularity)
	if err != nil {
		return nil, err
	}
	if h.ledger == nil {
		return nil, connect.NewError(connect.CodeUnavailable, "billing: capability subsystem disabled")
	}

	out := &TimeSeries{Buckets: make([]TimeBucket, 0)}
	if start.Before(end) {
		var sums []meter.ChargeSum
		req := meter.SumChargesRequest{TenantID: tenantID, From: start, To: end, Bucket: ledgerBuckets[granularity]}
		if err := h.sumCharges(ctx, req, func(s meter.ChargeSum) bool {
			sums = append(sums, s)
			return true
		}); err != nil {
			return nil, err
		}
		out.UnitCode, out.Buckets = series(sums)
	}
	if out.UnitCode == "" {
		if tb, ok := h.tenantBudget(ctx, tenantID); ok {
			out.UnitCode = tb.Ceiling.UnitCode
		}
	}
	if out.UnitCode == "" {
		out.UnitCode = h.currency
	}
	return out, nil
}

// seriesWindow is a time series request's period and granularity, checked:
// the granularity one of the ledger's buckets (day when unset), the period
// resolved as every billing read resolves it, and no more buckets than
// maxTimeSeriesBuckets. Every error is InvalidArgument.
func seriesWindow(periodStart, periodEnd time.Time, granularity string) (time.Time, time.Time, string, error) {
	if granularity == "" {
		granularity = granularityDay
	}
	if _, ok := ledgerBuckets[granularity]; !ok {
		return time.Time{}, time.Time{}, "", connect.Errorf(connect.CodeInvalidArgument, "granularity must be one of hour|day|week, got %q", granularity)
	}
	start, end, err := resolvePeriod(periodStart, periodEnd)
	if err != nil {
		return time.Time{}, time.Time{}, "", connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	// Bound the result set: (period ÷ granularity) is the bucket count the
	// GROUP BY produces, all buffered in memory. An hour-granularity request
	// over years would materialise tens of thousands of rows. Reject rather
	// than silently LIMIT — a truncated series is a misleading answer, so the
	// caller must narrow the period or coarsen the granularity instead.
	if step := granularityStep(granularity); step > 0 {
		if buckets := int64(end.Sub(start) / step); buckets > maxTimeSeriesBuckets {
			return time.Time{}, time.Time{}, "", connect.Errorf(connect.CodeInvalidArgument, "period too wide for %q granularity: %d buckets exceeds the %d cap; narrow the period or use a coarser granularity",
				granularity, buckets, maxTimeSeriesBuckets)
		}
	}
	return start, end, granularity, nil
}

// GetPlatformTimeSeries is GetTenantTimeSeries over every tenant's charges:
// what the platform was charged, a bucket at a time, in the unit most of the
// period's charges are in. It reads every tenant's rows, so it is gated as
// ListTenantSpend is.
func (h *Handler) GetPlatformTimeSeries(ctx context.Context, periodStart, periodEnd time.Time, granularity string) (*TimeSeries, error) {
	if err := h.authorizeAcrossTenants(ctx); err != nil {
		return nil, err
	}
	start, end, granularity, err := seriesWindow(periodStart, periodEnd, granularity)
	if err != nil {
		return nil, err
	}
	if h.ledger == nil {
		return nil, connect.NewError(connect.CodeUnavailable, "billing: capability subsystem disabled")
	}
	out := &TimeSeries{Buckets: make([]TimeBucket, 0)}
	if start.Before(end) {
		var sums []meter.ChargeSum
		req := meter.SumChargesRequest{AllTenants: true, From: start, To: end, Bucket: ledgerBuckets[granularity]}
		// Every tenant's rows, read with no write allowed.
		if err := h.sumCharges(auth.WithCrossTenantRead(ctx), req, func(s meter.ChargeSum) bool {
			sums = append(sums, s)
			return true
		}); err != nil {
			return nil, err
		}
		out.UnitCode, out.Buckets = series(sums)
	}
	if out.UnitCode == "" {
		out.UnitCode = h.currency
	}
	return out, nil
}

// Currency is the platform's currency, capability.currency, for a caller that
// may read its own tenant's billing: a client shows it and defaults to it.
func (h *Handler) Currency(ctx context.Context) (string, error) {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return "", connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	if _, err := h.authorize(ctx, p.TenantID); err != nil {
		return "", err
	}
	return h.currency, nil
}
