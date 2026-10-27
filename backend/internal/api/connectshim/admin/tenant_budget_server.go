package admin

import (
	"context"
	"errors"
	"fmt"
	"math"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/convx"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"

	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// budgetSegment names a tenant's budget in a resource name:
// `tenants/{tenant}/budget`, as its quota is `tenants/{tenant}/quota`.
const budgetSegment = "/budget"

// TenantBudgetServer is the Connect adapter for meter.TenantBudgets —
// the tenant-ceiling half of the usage store. It depends on nothing that
// meters, so it cannot charge or refund. A thin pass-through that does shape
// conversion + error mapping.
//
// Authorization: this handler runs on the admin plane behind the
// standard JWT + Cedar interceptor stack. cfg.Cedar policies gate
// who can read / write per-tenant budgets — typical setup is
// platform.admin (cross-tenant) and tenant.admin (own-tenant only).
type TenantBudgetServer struct {
	paladinadminv1connect.UnimplementedTenantBudgetServiceHandler
	Usage meter.TenantBudgets
	// Tenants names the tenants a summary lists: the budget rows carry
	// their ids alone.
	Tenants tenanth.Names
}

// NewTenantBudgetServer wires the handler. usage may be nil — the
// capability subsystem is opt-in (cfg.Capability.Enabled). When nil,
// every RPC returns CodeUnavailable so the operator notices the
// misconfig immediately rather than getting silent NULL responses.
// tenants is required with usage: Summarize names each row's tenant.
func NewTenantBudgetServer(usage meter.TenantBudgets, tenants tenanth.Names) *TenantBudgetServer {
	return &TenantBudgetServer{Usage: usage, Tenants: tenants}
}

func (s *TenantBudgetServer) Get(
	ctx context.Context,
	req *pb.TenantBudgetServiceGetRequest,
) (*pb.TenantBudgetServiceGetResponse, error) {
	if s.Usage == nil {
		return nil, connect.NewError(connect.CodeUnavailable, "capability subsystem disabled; tenant budget unavailable")
	}
	tenantID, err := uuid.Parse(req.GetTenantId())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("tenant_id: %w", err))
	}
	// Same RLS constraint as Set, on the read side: the isolation policy's
	// USING clause hides rows belonging to another tenant, so a platform admin
	// reading someone else's budget got NotFound for a row that exists. Acting
	// as the tenant is what makes the read see it.
	tb, err := s.Usage.GetTenantBudget(auth.WithActingTenant(ctx, tenantID), tenantID)
	if err != nil {
		if errors.Is(err, meter.ErrTenantBudgetNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err.Error()).WithCause(err)
		}
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &pb.TenantBudgetServiceGetResponse{
		Budget: tenantBudgetToProto(tb),
	}, nil
}

func (s *TenantBudgetServer) Set(
	ctx context.Context,
	req *pb.TenantBudgetServiceSetRequest,
) (*pb.TenantBudgetServiceSetResponse, error) {
	if s.Usage == nil {
		return nil, connect.NewError(connect.CodeUnavailable, "capability subsystem disabled; tenant budget unavailable")
	}
	m := req
	tenantID, err := uuid.Parse(m.GetTenantId())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("tenant_id: %w", err))
	}
	// The request names the tenant by id, so the audit row would name nothing
	// and the change would be in no tenant's trail.
	apiutil.StashResource(ctx, apiutil.TenantNamePrefix+tenantID.String()+budgetSegment)
	expected, err := convx.ParseRV(m.GetResourceVersion())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("resource_version: %w", err))
	}
	if err := apiutil.RefuseRemovedFields(m); err != nil {
		return nil, err
	}
	// A budget that names no unit leaves the unit to the existing row, or to
	// the platform's currency on the first one (capabilitypg.UsageStore). A
	// zero limit allows no spend.
	ceiling, _, set, err := apiutil.BudgetCeilingOf(fieldBudgetCeiling, m.GetMaxBudget(), m.GetUnlimitedBudget())
	if err != nil {
		return nil, err
	}
	if !set {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fieldBudgetCeiling+": set max_budget, or unlimited_budget for no limit")
	}
	args := meter.SetTenantBudgetRequest{
		TenantID:        tenantID,
		Ceiling:         ceiling,
		ResetSpend:      m.GetResetSpend(),
		ExpectedVersion: expected,
	}
	if pe := m.GetPeriodEnd(); pe != nil {
		args.PeriodEnd = pe.AsTime()
	}
	// limes.tenant_budgets carries row-level security, whose WITH CHECK pins
	// writes to the tenant the request acts on. A platform admin setting another
	// tenant's cap therefore has to act AS that tenant for the write, or
	// Postgres refuses the insert with "new row violates row-level security
	// policy" — which is what happened: creating a budget from the console
	// failed for every tenant except the admin's own. SetQuota, the same shape
	// on the same policy, has always done this.
	tb, err := s.Usage.SetTenantBudget(auth.WithActingTenant(ctx, tenantID), args)
	if err != nil {
		// A version mismatch is the caller's to resolve — re-read and retry —
		// not a server fault, so it must not read as Internal.
		switch {
		case errors.Is(err, meter.ErrTenantBudgetVersionMismatch):
			return nil, connect.NewError(connect.CodeAborted, err.Error()).WithCause(err)
		// Re-denominating a tenant that still counts spend or holds in its
		// unit: no retry meets it until they are reset or released.
		case errors.Is(err, limes.ErrUnitCodeMismatch):
			return nil, connect.NewError(connect.CodeFailedPrecondition, err.Error()).WithCause(err)
		case errors.Is(err, limes.ErrInvalidRequest):
			return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
		}
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &pb.TenantBudgetServiceSetResponse{
		Budget: tenantBudgetToProto(tb),
	}, nil
}

// Summarize returns one row per tenant joining the budget snapshot
// with slug + display_name. The platform.admin gate is enforced by
// the standard interceptor stack; this handler just translates
// request → store args → proto. unlimited_only and threshold_pct
// are mutually exclusive (server-enforced).
//
// exclude_inactive drops the rows of tenants in the trash from each page
// after it is read, so such a page can come back shorter than limit with
// more to follow; next_page_token says whether there are.
func (s *TenantBudgetServer) Summarize(
	ctx context.Context,
	req *pb.TenantBudgetServiceSummarizeRequest,
) (*pb.TenantBudgetServiceSummarizeResponse, error) {
	if s.Usage == nil {
		return nil, connect.NewError(connect.CodeUnavailable, "capability subsystem disabled; tenant budget unavailable")
	}
	m := req
	if m.GetUnlimitedOnly() && m.GetThresholdPct() > 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, "unlimited_only is mutually exclusive with a non-zero threshold_pct")
	}
	// Every tenant's row, which the platform.admin gate above allows, read
	// with no write allowed.
	page, err := s.Usage.ListTenantBudgets(auth.WithCrossTenantRead(ctx), budgetListRequest(m))
	if err != nil {
		if errors.Is(err, limes.ErrInvalidRequest) { // a page token the store cannot read
			return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
		}
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	ids := make([]uuid.UUID, 0, len(page.Items))
	for _, b := range page.Items {
		ids = append(ids, b.TenantID)
	}
	names, err := s.Tenants.TenantNames(ctx, ids)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	out := &pb.TenantBudgetServiceSummarizeResponse{
		Summaries:     make([]*pb.TenantBudgetSummary, 0, len(page.Items)),
		NextPageToken: page.Next,
	}
	for _, b := range page.Items {
		name, ok := names[b.TenantID]
		if m.GetExcludeInactive() && (!ok || name.Deleted) {
			continue
		}
		pct, _ := b.Utilization() // an unlimited tenant reports 0
		out.Summaries = append(out.Summaries, &pb.TenantBudgetSummary{
			TenantId:       b.TenantID.String(),
			Slug:           name.Slug,
			DisplayName:    name.DisplayName,
			Budget:         tenantBudgetToProto(b),
			UtilisationPct: pct,
		})
	}
	return out, nil
}

// budgetListRequest is the store's listing for a summary request: the
// unlimited tenants, the tenants at or past a threshold, or all of them —
// threshold_pct 0 lists every tenant, as it always has.
func budgetListRequest(m *pb.TenantBudgetServiceSummarizeRequest) meter.ListTenantBudgetsRequest {
	req := meter.ListTenantBudgetsRequest{Filter: meter.TenantBudgetFilterAll, Limit: int(m.GetLimit()), Cursor: m.GetPageToken()}
	switch {
	case m.GetUnlimitedOnly():
		req.Filter = meter.TenantBudgetFilterUnlimited
	case m.GetThresholdPct() > 0:
		req.Filter, req.ThresholdBasisPoints = meter.TenantBudgetFilterAtOrPast, basisPointsOf(m.GetThresholdPct())
	}
	return req
}

// basisPointsPerPercent is how many of limes's basis points one percent of
// threshold_pct is.
const basisPointsPerPercent = meter.BasisPointsPerWhole / 100

// basisPointsOf is a threshold_pct, a percentage, in the basis points limes
// compares in, to the nearest one: 29 is 2900, 1.1 is 110.
func basisPointsOf(pct float64) int {
	return int(math.Round(pct * basisPointsPerPercent))
}

var _ paladinadminv1connect.TenantBudgetServiceHandler = (*TenantBudgetServer)(nil)

// fieldBudgetCeiling is the Set request's ceiling, named in its refusals.
const fieldBudgetCeiling = "budget_ceiling"

// tenantBudgetToProto converts the internal snapshot to the wire shape.
// Times that are zero come back as nil so the wire payload is tighter
// (Connect-JSON doesn't need to ship the epoch timestamp).
func tenantBudgetToProto(tb meter.TenantBudget) *pb.TenantBudget {
	out := &pb.TenantBudget{
		TenantId:        tb.TenantID.String(),
		Spent:           apiutil.MoneyOf(tb.Ceiling.UnitCode, tb.SpentAmount),
		ResourceVersion: convx.ResourceVersion(tb.ResourceVersion),
	}
	limit, unlimited := apiutil.BudgetCeilingArms(tb.Ceiling, tb.Ceiling.UnitCode)
	if unlimited != nil {
		out.BudgetCeiling = &pb.TenantBudget_UnlimitedBudget{UnlimitedBudget: unlimited}
	} else {
		out.BudgetCeiling = &pb.TenantBudget_MaxBudget{MaxBudget: limit}
	}
	if !tb.PeriodStart.IsZero() {
		out.PeriodStart = timestamppb.New(tb.PeriodStart)
	}
	if !tb.PeriodEnd.IsZero() {
		out.PeriodEnd = timestamppb.New(tb.PeriodEnd)
	}
	if !tb.UpdatedAt.IsZero() {
		out.UpdatedAt = timestamppb.New(tb.UpdatedAt)
	}
	return out
}
