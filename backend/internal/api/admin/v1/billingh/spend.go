package billingh

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// The tenants ListTenantSpend returns: DefaultSpendLimit when the request
// names no limit, and never more than MaxSpendLimit.
const (
	DefaultSpendLimit = 100
	MaxSpendLimit     = 1000
)

// TenantSpend is what one tenant's charges came to over a period, net of
// refunds, in the unit most of them are in.
type TenantSpend struct {
	TenantID    uuid.UUID
	UnitCode    string
	Amount      limes.Nanos
	ChargeCount int64
}

// ListTenantSpend sums the charges ledger across every tenant over the
// period, in one request, and returns the tenants that spent anything —
// at most limit of them, ordered as rankSpend orders them — and whether more
// spent than limit let through. It reads every tenant's rows, so beside
// ReadBilling on the caller's own tenant it needs the platform.admin role.
func (h *Handler) ListTenantSpend(ctx context.Context, periodStart, periodEnd time.Time, limit int) ([]TenantSpend, bool, error) {
	if err := h.authorizeAcrossTenants(ctx); err != nil {
		return nil, false, err
	}
	if h.ledger == nil {
		return nil, false, connect.NewError(connect.CodeUnavailable, "billing: capability subsystem disabled")
	}
	switch {
	case limit == 0:
		limit = DefaultSpendLimit
	case limit < 0 || limit > MaxSpendLimit:
		return nil, false, connect.NewError(connect.CodeInvalidArgument,
			fmt.Sprintf("limit %d is outside 0..%d", limit, MaxSpendLimit))
	}
	start, end, err := resolvePeriod(periodStart, periodEnd)
	if err != nil {
		return nil, false, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	if !start.Before(end) {
		return []TenantSpend{}, false, nil
	}
	req := meter.SumChargesRequest{
		AllTenants: true, From: start, To: end,
		GroupBy: []meter.ChargeDimension{meter.ChargeDimensionTenant},
	}
	var sums []meter.ChargeSum
	// Every tenant's rows, read with no write allowed.
	if err := h.sumCharges(auth.WithCrossTenantRead(ctx), req, func(s meter.ChargeSum) bool {
		sums = append(sums, s)
		return true
	}); err != nil {
		return nil, false, err
	}
	ranked := rankSpend(sums, h.currency)
	if len(ranked) > limit {
		return ranked[:limit], true, nil
	}
	return ranked, false, nil
}

// authorizeAcrossTenants gates a read of every tenant's billing: ReadBilling
// on the caller's own tenant, as the actions that reach past it are resourced,
// then the platform.admin role, which alone may read another tenant's.
func (h *Handler) authorizeAcrossTenants(ctx context.Context) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipal(p),
		cedar.ActionReadBilling,
		&cedar.Resource{TenantID: p.TenantID},
		cedar.RequestContext{Now: time.Now()},
	)
	if err != nil {
		return apiutil.MapError(fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied, "denied by policy")
	}
	return apiutil.RequireRole(ctx, apiutil.RolePlatformAdmin)
}

// rankSpend is each tenant's spend among sums — the ledger's sums by tenant
// and unit — in the unit most of its charges are in, ordered by unit, the
// platform's currency first and then the others by code, since amounts in
// different units do not compare; then by spend, the most first; then by
// tenant id, so the order does not depend on the ledger's.
func rankSpend(sums []meter.ChargeSum, currency string) []TenantSpend {
	byTenant := map[uuid.UUID][]meter.ChargeSum{}
	for _, s := range sums {
		byTenant[s.TenantID] = append(byTenant[s.TenantID], s)
	}
	out := make([]TenantSpend, 0, len(byTenant))
	for tenant, units := range byTenant {
		best, ok := dominant(units)
		if !ok {
			continue
		}
		out = append(out, TenantSpend{TenantID: tenant, UnitCode: best.UnitCode, Amount: net(best), ChargeCount: best.Charges})
	}
	slices.SortFunc(out, func(a, b TenantSpend) int {
		if (a.UnitCode == currency) != (b.UnitCode == currency) {
			if a.UnitCode == currency {
				return -1
			}
			return 1
		}
		return cmp.Or(
			cmp.Compare(a.UnitCode, b.UnitCode),
			cmp.Compare(b.Amount, a.Amount),
			cmp.Compare(a.TenantID.String(), b.TenantID.String()),
		)
	})
	return out
}
