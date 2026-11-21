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
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// day is a day's length, at UTC, where every billing window is cut.
const day = 24 * time.Hour

// defaultPeriodDays is the lookback, in days, when the caller omits
// period_start / period_end. Picked as 30d to match the typical billing
// close.
const defaultPeriodDays = 30

// defaultPeriod is defaultPeriodDays as a duration.
const defaultPeriod = defaultPeriodDays * day

// topN caps each breakdown list. 10 is enough for the dashboard tile;
// callers that need more can paginate via a future RPC.
const topN = 10

// msgSubsystemDisabled is what a deployment with no ledger answers.
const msgSubsystemDisabled = "billing: capability subsystem disabled"

// The operations a log line names.
const (
	opGetTenantSummary      = "GetTenantSummary"
	opGetTenantTimeSeries   = "GetTenantTimeSeries"
	opGetPlatformTimeSeries = "GetPlatformTimeSeries"
	opListTenantTimeSeries  = "ListTenantTimeSeries"
	opListTenantSpend       = "ListTenantSpend"
	opCurrency              = "Currency"
)

// Handler is the BillingService backend: the charges ledger, the Cedar gate,
// and a UsageStore for the tenant budget cap join.
type Handler struct {
	ledger meter.LedgerReader
	usage  meter.UsageStore
	policy cedar.Authorizer
	// currency is the platform's (capability.currency): what a summary with
	// no charge and no budget to take a unit from is in. See WithCurrency.
	currency string
	log      *zap.Logger
}

// WithCurrency sets the platform's currency, capability.currency. Without it
// an empty summary is in apiutil.NoUnit.
func (h *Handler) WithCurrency(unit string) *Handler {
	h.currency = unit
	return h
}

// SetLogger sets where each read, refusal and store failure is recorded.
// Unset, or set to nil, nothing is.
func (h *Handler) SetLogger(l *zap.Logger) {
	if l != nil {
		h.log = l
	}
}

// NewHandler wires the dependencies. policy MUST be non-nil; ledger may
// be nil only when the capability subsystem is disabled (the listener
// builder still needs to register a stub so the BFF allowlist doesn't
// 404).
func NewHandler(ledger meter.LedgerReader, usage meter.UsageStore, policy cedar.Authorizer) *Handler {
	if policy == nil {
		panic("billingh: policy authorizer is required")
	}
	return &Handler{ledger: ledger, usage: usage, policy: policy, currency: apiutil.NoUnit, log: zap.NewNop()}
}

// fields are what a log line of op says about the caller ctx carries, then
// extra.
func fields(ctx context.Context, op string, extra ...zap.Field) []zap.Field {
	actor := ""
	if p, err := auth.PrincipalFromContext(ctx); err == nil {
		actor = p.Subject
	}
	return append([]zap.Field{zap.String("op", op), zap.String("actor", actor)}, extra...)
}

// tenantField names the tenant a read is about.
func tenantField(tenantID uuid.UUID) zap.Field { return zap.String("tenant_id", tenantID.String()) }

// refused logs err, op's refusal of the request, at Warn, and returns it.
func (h *Handler) refused(ctx context.Context, op string, err error, extra ...zap.Field) error {
	h.log.Warn("billing read refused", append(fields(ctx, op, extra...), zap.Stringer("code", connect.CodeOf(err)), zap.Error(err))...)
	return err
}

// failed logs err, a store op could not read, at Error, and returns it.
func (h *Handler) failed(ctx context.Context, op string, err error, extra ...zap.Field) error {
	h.log.Error("billing store failed", append(fields(ctx, op, extra...), zap.Stringer("code", connect.CodeOf(err)), zap.Error(err))...)
	return err
}

// served logs op answered.
func (h *Handler) served(ctx context.Context, op string, extra ...zap.Field) {
	h.log.Debug("billing read served", fields(ctx, op, extra...)...)
}

// subsystemDisabled is the answer of a deployment with no ledger.
func subsystemDisabled() error {
	return connect.NewError(connect.CodeUnavailable, msgSubsystemDisabled)
}

// invalidPeriod is resolvePeriod's err as the caller is told it.
func invalidPeriod(err error) error {
	return connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
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
	if _, err := apiutil.Authorize(ctx, h.policy, cedar.ActionReadBilling, &cedar.Resource{TenantID: tenantID}); err != nil {
		return nil, err
	}
	return auth.WithActingTenant(ctx, tenantID), nil
}

// tenantBudget reads tenantID's budget. ok is false for a tenant with no
// budget, or a deployment with no store to read it from; any other failure
// is returned, answered by its limes code, for the caller to decide whether
// what it answers stands without it.
func (h *Handler) tenantBudget(ctx context.Context, tenantID uuid.UUID) (tb meter.TenantBudget, ok bool, err error) {
	if h.usage == nil {
		return meter.TenantBudget{}, false, nil
	}
	tb, err = h.usage.GetTenantBudget(ctx, tenantID)
	switch {
	case errors.Is(err, meter.ErrTenantBudgetNotFound):
		return meter.TenantBudget{}, false, nil
	case err != nil:
		return meter.TenantBudget{}, false, auth.AnswerCapabilityError(fmt.Errorf("billing: tenant budget: %w", err), nil)
	}
	return tb, true, nil
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
	// Refunded is what was returned from the period's charges; TotalAmount
	// is net of it.
	Refunded limes.Nanos
	UnitCode string
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
//
// A budget that cannot be read fails the summary: without its ceiling the
// summary reads as a tenant with no cap configured.
func (h *Handler) GetTenantSummary(ctx context.Context, tenantID uuid.UUID, periodStart, periodEnd time.Time) (*Summary, error) {
	tenant := tenantField(tenantID)
	acting, err := h.authorize(ctx, tenantID)
	if err != nil {
		return nil, h.refused(ctx, opGetTenantSummary, err, tenant)
	}
	if h.ledger == nil {
		return nil, h.refused(ctx, opGetTenantSummary, subsystemDisabled(), tenant)
	}
	start, end, err := resolvePeriod(periodStart, periodEnd)
	if err != nil {
		return nil, h.refused(ctx, opGetTenantSummary, invalidPeriod(err), tenant)
	}
	out, err := h.summarize(acting, meter.SumChargesRequest{TenantID: tenantID, From: start, To: end})
	if err != nil {
		return nil, h.failed(ctx, opGetTenantSummary, err, tenant)
	}
	tb, ok, err := h.tenantBudget(acting, tenantID)
	if err != nil {
		return nil, h.failed(ctx, opGetTenantSummary, err, tenant)
	}
	if ok {
		ceiling := tb.Ceiling
		out.Ceiling, out.CeilingUnit = &ceiling, tb.Ceiling.UnitCode
		if out.UnitCode == "" {
			out.UnitCode = tb.Ceiling.UnitCode
		}
	}
	if out.UnitCode == "" {
		out.UnitCode = h.currency
	}
	h.served(ctx, opGetTenantSummary, tenant)
	return out, nil
}

// summarize is the ledger's half of a summary over period: the dominant
// unit's total and its top lists. An empty period has none.
func (h *Handler) summarize(ctx context.Context, period meter.SumChargesRequest) (*Summary, error) {
	out := &Summary{}
	if !period.From.Before(period.To) {
		return out, nil
	}
	var totals []meter.ChargeSum
	if err := h.sumCharges(ctx, period, func(s meter.ChargeSum) bool {
		totals = append(totals, s)
		return true
	}); err != nil {
		return nil, err
	}
	total, ok := dominant(totals)
	if !ok {
		return out, nil
	}
	out.UnitCode, out.TotalAmount, out.Refunded, out.ChargeCount = total.UnitCode, net(total), total.Refunded, total.Charges
	for _, top := range []struct {
		into  *[]TopEntry
		by    meter.ChargeDimension
		label func(meter.ChargeSum) string
	}{
		{&out.TopCapabilities, meter.ChargeDimensionCapability, func(s meter.ChargeSum) string { return s.CapabilityID.String() }},
		{&out.TopActors, meter.ChargeDimensionActor, func(s meter.ChargeSum) string { return s.Actor }},
		{&out.TopOps, meter.ChargeDimensionOp, func(s meter.ChargeSum) string { return string(s.Op) }},
	} {
		req := period
		req.GroupBy = []meter.ChargeDimension{top.by}
		var err error
		if *top.into, err = h.topBy(ctx, req, out.UnitCode, top.label); err != nil {
			return nil, err
		}
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
	tenant := tenantField(tenantID)
	acting, err := h.authorize(ctx, tenantID)
	if err != nil {
		return nil, h.refused(ctx, opGetTenantTimeSeries, err, tenant)
	}
	// Input validation (granularity + period + bucket bound) runs before the
	// subsystem-availability check so a malformed request is rejected the same
	// way whether or not the capability subsystem is wired.
	start, end, granularity, err := seriesWindow(periodStart, periodEnd, granularity)
	if err != nil {
		return nil, h.refused(ctx, opGetTenantTimeSeries, err, tenant)
	}
	if h.ledger == nil {
		return nil, h.refused(ctx, opGetTenantTimeSeries, subsystemDisabled(), tenant)
	}
	out, err := h.seriesOver(acting, meter.SumChargesRequest{TenantID: tenantID, From: start, To: end, Bucket: ledgerBuckets[granularity]})
	if err != nil {
		return nil, h.failed(ctx, opGetTenantTimeSeries, err, tenant)
	}
	if out.UnitCode == "" {
		out.UnitCode = h.budgetUnit(acting, tenantID)
	}
	if out.UnitCode == "" {
		out.UnitCode = h.currency
	}
	h.served(ctx, opGetTenantTimeSeries, tenant)
	return out, nil
}

// budgetUnit is the unit of tenantID's budget, "" for none. A series only
// takes its unit from it, so one that cannot be read is logged and the
// series stands in the platform's currency.
func (h *Handler) budgetUnit(ctx context.Context, tenantID uuid.UUID) string {
	tb, ok, err := h.tenantBudget(ctx, tenantID)
	if err != nil {
		h.log.Error("billing: tenant budget unreadable; the series falls back to the platform currency",
			fields(ctx, opGetTenantTimeSeries, tenantField(tenantID), zap.Error(err))...)
	}
	if !ok {
		return ""
	}
	return tb.Ceiling.UnitCode
}

// seriesOver is req's time series, in the unit most of its charges are in;
// an empty period has no buckets and no unit.
func (h *Handler) seriesOver(ctx context.Context, req meter.SumChargesRequest) (*TimeSeries, error) {
	out := &TimeSeries{Buckets: make([]TimeBucket, 0)}
	if !req.From.Before(req.To) {
		return out, nil
	}
	var sums []meter.ChargeSum
	if err := h.sumCharges(ctx, req, func(s meter.ChargeSum) bool {
		sums = append(sums, s)
		return true
	}); err != nil {
		return nil, err
	}
	out.UnitCode, out.Buckets = series(sums)
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
		return time.Time{}, time.Time{}, "", invalidPeriod(err)
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
		return nil, h.refused(ctx, opGetPlatformTimeSeries, err)
	}
	start, end, granularity, err := seriesWindow(periodStart, periodEnd, granularity)
	if err != nil {
		return nil, h.refused(ctx, opGetPlatformTimeSeries, err)
	}
	if h.ledger == nil {
		return nil, h.refused(ctx, opGetPlatformTimeSeries, subsystemDisabled())
	}
	// Every tenant's rows, read with no write allowed.
	out, err := h.seriesOver(auth.WithCrossTenantRead(ctx),
		meter.SumChargesRequest{AllTenants: true, From: start, To: end, Bucket: ledgerBuckets[granularity]})
	if err != nil {
		return nil, h.failed(ctx, opGetPlatformTimeSeries, err)
	}
	if out.UnitCode == "" {
		out.UnitCode = h.currency
	}
	h.served(ctx, opGetPlatformTimeSeries)
	return out, nil
}

// TenantSeries is one tenant's spend series.
type TenantSeries struct {
	TenantID uuid.UUID
	Series   TimeSeries
}

// ListTenantTimeSeries is GetTenantTimeSeries for each of tenantIDs, from one
// sum of the ledger across them, grouped by tenant: one series per tenant, in
// the order asked, each in the unit most of its charges are in. A tenant that
// spent nothing has no buckets, in the platform's currency. It reads every
// named tenant's rows, so it is gated as ListTenantSpend is.
func (h *Handler) ListTenantTimeSeries(ctx context.Context, tenantIDs []uuid.UUID, periodStart, periodEnd time.Time, granularity string) ([]TenantSeries, error) {
	tenants := zap.Int("tenants", len(tenantIDs))
	if err := h.authorizeAcrossTenants(ctx); err != nil {
		return nil, h.refused(ctx, opListTenantTimeSeries, err, tenants)
	}
	start, end, granularity, err := seriesWindow(periodStart, periodEnd, granularity)
	if err != nil {
		return nil, h.refused(ctx, opListTenantTimeSeries, err, tenants)
	}
	if h.ledger == nil {
		return nil, h.refused(ctx, opListTenantTimeSeries, subsystemDisabled(), tenants)
	}
	byTenant, err := h.sumsByTenant(ctx, tenantIDs, meter.SumChargesRequest{
		AllTenants: true, From: start, To: end, Bucket: ledgerBuckets[granularity],
		GroupBy: []meter.ChargeDimension{meter.ChargeDimensionTenant},
		Filter:  meter.ChargeFilter{Tenants: tenantIDs},
	})
	if err != nil {
		return nil, h.failed(ctx, opListTenantTimeSeries, err, tenants)
	}
	h.served(ctx, opListTenantTimeSeries, tenants)
	return h.perTenant(tenantIDs, byTenant), nil
}

// sumsByTenant is req's sums, each under the tenant it is of; none for an
// empty period or no tenant asked for.
func (h *Handler) sumsByTenant(ctx context.Context, tenantIDs []uuid.UUID, req meter.SumChargesRequest) (map[uuid.UUID][]meter.ChargeSum, error) {
	byTenant := make(map[uuid.UUID][]meter.ChargeSum, len(tenantIDs))
	if !req.From.Before(req.To) || len(tenantIDs) == 0 {
		return byTenant, nil
	}
	// The named tenants' rows, read with no write allowed.
	err := h.sumCharges(auth.WithCrossTenantRead(ctx), req, func(s meter.ChargeSum) bool {
		byTenant[s.TenantID] = append(byTenant[s.TenantID], s)
		return true
	})
	return byTenant, err
}

// perTenant is a series for each of tenantIDs, in order, from its sums; one
// with none has no buckets, in the platform's currency.
func (h *Handler) perTenant(tenantIDs []uuid.UUID, byTenant map[uuid.UUID][]meter.ChargeSum) []TenantSeries {
	out := make([]TenantSeries, 0, len(tenantIDs))
	for _, id := range tenantIDs {
		ts := TimeSeries{Buckets: make([]TimeBucket, 0)}
		if sums := byTenant[id]; len(sums) > 0 {
			ts.UnitCode, ts.Buckets = series(sums)
		}
		if ts.UnitCode == "" {
			ts.UnitCode = h.currency
		}
		out = append(out, TenantSeries{TenantID: id, Series: ts})
	}
	return out
}

// Currency is the platform's currency, capability.currency, for a caller that
// may read its own tenant's billing: a client shows it and defaults to it.
func (h *Handler) Currency(ctx context.Context) (string, error) {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return "", h.refused(ctx, opCurrency, connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err))
	}
	if _, err := h.authorize(ctx, p.TenantID); err != nil {
		return "", h.refused(ctx, opCurrency, err, tenantField(p.TenantID))
	}
	return h.currency, nil
}
