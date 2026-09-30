// Package billingh implements the admin BillingService — read-only
// aggregation over the charges ledger (the schema baseline (001_initial_schema.sql)).
//
// The handler runs raw SQL against the *pgxpool.Pool because the
// queries are aggregation-only, lightly parameterised, and the time
// pressure to ship the billing surface didn't justify a sqlc
// regeneration round-trip. Future iterations may move the queries
// behind sqlc once the access patterns stabilise.
package billingh

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/capability"
)

// defaultPeriod is the lookback when the caller omits period_start /
// period_end. Picked as 30d to match the typical billing close.
const defaultPeriod = 30 * 24 * time.Hour

// topN caps each breakdown list. 10 is enough for the dashboard tile;
// callers that need more can paginate via a future RPC.
const topN = 10

// Handler is the BillingService backend. Owns the pool + Cedar gate
// + a UsageStore for the tenant_budgets cap join (cheap re-use vs.
// reaching for tenant_budgets directly).
type Handler struct {
	pool   *pgxpool.Pool
	usage  capability.UsageStore[pgx.Tx]
	policy cedar.Authorizer
}

// NewHandler wires the dependencies. policy MUST be non-nil; pool may
// be nil only when the capability subsystem is disabled (the listener
// builder still needs to register a stub so the BFF allowlist doesn't
// 404).
func NewHandler(pool *pgxpool.Pool, usage capability.UsageStore[pgx.Tx], policy cedar.Authorizer) *Handler {
	if policy == nil {
		panic("billingh: policy authorizer is required")
	}
	return &Handler{pool: pool, usage: usage, policy: policy}
}

// authorize gates the RPC against Cedar. Resource is the Tenant
// entity carrying the queried tenant_id, so policies can pin
// "tenant.admin reads own tenant only" via resource.tenant_id ==
// principal.tenant_id.
func (h *Handler) authorize(ctx context.Context, tenantID uuid.UUID) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipal(p),
		cedar.ActionReadBilling,
		&cedar.Resource{TenantID: tenantID},
		cedar.RequestContext{Now: time.Now()},
	)
	if err != nil {
		return apiutil.MapError(fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied, errors.New("denied by policy"))
	}
	return nil
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
	TotalAmount     float64
	UnitCode        string
	MaxBudgetAmount float64
	ChargeCount     int64
	TopCapabilities []TopEntry
	TopActors       []TopEntry
	TopOps          []TopEntry
}

// TopEntry mirrors the proto shape one-to-one.
type TopEntry struct {
	Label       string
	Amount      float64
	ChargeCount int64
}

// GetTenantSummary aggregates the charges ledger over the period.
// Cedar gate runs first (security before subsystem availability);
// pool == nil then short-circuits to Unavailable (capability
// subsystem disabled in this deployment).
func (h *Handler) GetTenantSummary(ctx context.Context, tenantID uuid.UUID, periodStart, periodEnd time.Time) (*Summary, error) {
	if err := h.authorize(ctx, tenantID); err != nil {
		return nil, err
	}
	if h.pool == nil {
		return nil, connect.NewError(connect.CodeUnavailable,
			errors.New("billing: capability subsystem disabled"))
	}
	start, end, err := resolvePeriod(periodStart, periodEnd)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	out := &Summary{}

	// Total + count + dominant unit_code. We pick the unit_code that
	// covers the most rows — mixed-unit tenants are degenerate today
	// (no FX) and the operator's choice is which unit to read the
	// total in. tenant_budgets.unit_code (when present) is preferred.
	row := h.pool.QueryRow(ctx,
		`SELECT
		   COALESCE(SUM(amount), 0)::float8,
		   COUNT(*),
		   COALESCE(
		     (SELECT unit_code FROM charges
		        WHERE tenant_id = $1 AND occurred_at >= $2 AND occurred_at < $3
		        GROUP BY unit_code ORDER BY COUNT(*) DESC LIMIT 1),
		     '')
		 FROM charges
		 WHERE tenant_id = $1 AND occurred_at >= $2 AND occurred_at < $3`,
		tenantID, start, end,
	)
	if err := row.Scan(&out.TotalAmount, &out.ChargeCount, &out.UnitCode); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("billing: summary: %w", err))
	}

	// Tenant budget cap join — best-effort. Missing row ⇒ no cap;
	// out.MaxBudgetAmount stays 0 which the frontend renders as
	// "no cap configured".
	if h.usage != nil {
		if tb, err := h.usage.GetTenantBudget(ctx, tenantID); err == nil {
			out.MaxBudgetAmount = tb.MaxBudgetAmount
			if out.UnitCode == "" {
				out.UnitCode = tb.UnitCode
			}
		}
	}
	if out.UnitCode == "" {
		out.UnitCode = capability.DefaultUnitCode
	}

	out.TopCapabilities, err = h.queryTopBy(ctx, tenantID, start, end, "capability_id::text")
	if err != nil {
		return nil, err
	}
	out.TopActors, err = h.queryTopBy(ctx, tenantID, start, end, "actor_subject")
	if err != nil {
		return nil, err
	}
	out.TopOps, err = h.queryTopBy(ctx, tenantID, start, end, "op")
	if err != nil {
		return nil, err
	}
	return out, nil
}

// queryTopBy runs the GROUP BY+ORDER BY+LIMIT query for one
// dimension. col is interpolated into the SQL (not parameterised) —
// the value comes from a closed three-element constant set, no user
// input. Filter blanks out empty labels so unknown actors / ops
// don't dominate the top-N.
func (h *Handler) queryTopBy(ctx context.Context, tenantID uuid.UUID, start, end time.Time, col string) ([]TopEntry, error) {
	q := fmt.Sprintf(
		`SELECT %s AS label, COALESCE(SUM(amount), 0)::float8 AS amount, COUNT(*) AS n
		 FROM charges
		 WHERE tenant_id = $1 AND occurred_at >= $2 AND occurred_at < $3
		 GROUP BY %s
		 ORDER BY amount DESC
		 LIMIT %d`, col, col, topN)
	rows, err := h.pool.Query(ctx, q, tenantID, start, end)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("billing: top by %s: %w", col, err))
	}
	defer rows.Close()
	out := make([]TopEntry, 0, topN)
	for rows.Next() {
		var e TopEntry
		if err := rows.Scan(&e.Label, &e.Amount, &e.ChargeCount); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("billing: top scan: %w", err))
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return out, nil
}

// TimeSeries is the in-memory shape returned by GetTenantTimeSeries.
type TimeSeries struct {
	Buckets  []TimeBucket
	UnitCode string
}

// TimeBucket mirrors the proto shape.
type TimeBucket struct {
	Start       time.Time
	Amount      float64
	ChargeCount int64
}

// allowedGranularities is the closed set the SQL date_trunc accepts
// for this RPC. Bigger windows (month / year) are deliberately
// excluded — charts at that resolution come from a different
// dashboard surface and would benefit from a downsampled rollup
// table rather than scanning the raw ledger.
var allowedGranularities = map[string]bool{
	"hour": true, "day": true, "week": true,
}

// maxTimeSeriesBuckets caps the number of (period ÷ granularity) buckets a
// single GetTenantTimeSeries call may materialise, bounding the buffered result
// set. 1000 is generous for any legitimate chart — hour granularity spans ~41
// days (> the 30d default period), day spans ~2.7 years, week ~19 years — while
// rejecting the pathological "hour granularity over years" request.
const maxTimeSeriesBuckets = 1000

// granularityStep maps a validated granularity to its bucket width, used to
// pre-flight the bucket count. Returns 0 for an unknown value (the caller has
// already validated against allowedGranularities, so 0 only skips the check).
func granularityStep(granularity string) time.Duration {
	switch granularity {
	case "hour":
		return time.Hour
	case "day":
		return 24 * time.Hour
	case "week":
		return 7 * 24 * time.Hour
	default:
		return 0
	}
}

// GetTenantTimeSeries buckets charges.amount + count by time.
// granularity is validated against the closed allowlist before
// reaching the SQL — date_trunc would silently accept "minute" /
// "year" and we don't want to expose those without a deliberate
// product call.
func (h *Handler) GetTenantTimeSeries(ctx context.Context, tenantID uuid.UUID, periodStart, periodEnd time.Time, granularity string) (*TimeSeries, error) {
	if err := h.authorize(ctx, tenantID); err != nil {
		return nil, err
	}
	// Input validation (granularity + period + bucket bound) runs before the
	// subsystem-availability check so a malformed request is rejected the same
	// way whether or not the capability subsystem is wired.
	if granularity == "" {
		granularity = "day"
	}
	if !allowedGranularities[granularity] {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("granularity must be one of hour|day|week, got %q", granularity))
	}
	start, end, err := resolvePeriod(periodStart, periodEnd)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	// Bound the result set: (period ÷ granularity) is the bucket count the
	// GROUP BY produces, all buffered in memory. An hour-granularity request
	// over years would materialise tens of thousands of rows. Reject rather
	// than silently LIMIT — a truncated series is a misleading answer, so the
	// caller must narrow the period or coarsen the granularity instead.
	if step := granularityStep(granularity); step > 0 {
		if buckets := int64(end.Sub(start) / step); buckets > maxTimeSeriesBuckets {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("period too wide for %q granularity: %d buckets exceeds the %d cap; narrow the period or use a coarser granularity",
					granularity, buckets, maxTimeSeriesBuckets))
		}
	}
	if h.pool == nil {
		return nil, connect.NewError(connect.CodeUnavailable,
			errors.New("billing: capability subsystem disabled"))
	}

	q := fmt.Sprintf(
		`SELECT date_trunc('%s', occurred_at) AS bucket,
		        COALESCE(SUM(amount), 0)::float8 AS amount,
		        COUNT(*) AS n
		 FROM charges
		 WHERE tenant_id = $1 AND occurred_at >= $2 AND occurred_at < $3
		 GROUP BY bucket
		 ORDER BY bucket`, granularity)
	rows, err := h.pool.Query(ctx, q, tenantID, start, end)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("billing: timeseries: %w", err))
	}
	defer rows.Close()

	out := &TimeSeries{Buckets: make([]TimeBucket, 0, 32)}
	for rows.Next() {
		var b TimeBucket
		if err := rows.Scan(&b.Start, &b.Amount, &b.ChargeCount); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("billing: timeseries scan: %w", err))
		}
		out.Buckets = append(out.Buckets, b)
	}
	if err := rows.Err(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Unit_code derivation mirrors the summary path — take the
	// dominant unit over the period, fall back to tenant_budget,
	// then DefaultUnitCode.
	if err := h.pool.QueryRow(ctx,
		`SELECT COALESCE(
		    (SELECT unit_code FROM charges
		       WHERE tenant_id = $1 AND occurred_at >= $2 AND occurred_at < $3
		       GROUP BY unit_code ORDER BY COUNT(*) DESC LIMIT 1),
		    '')`,
		tenantID, start, end,
	).Scan(&out.UnitCode); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("billing: timeseries unit: %w", err))
	}
	if out.UnitCode == "" && h.usage != nil {
		if tb, err := h.usage.GetTenantBudget(ctx, tenantID); err == nil {
			out.UnitCode = tb.UnitCode
		}
	}
	if out.UnitCode == "" {
		out.UnitCode = capability.DefaultUnitCode
	}
	return out, nil
}
