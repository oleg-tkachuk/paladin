package admin

import (
	"context"
	"errors"
	"fmt"

	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/convx"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/capability"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"

	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// budgetSegment names a tenant's budget in a resource name:
// `tenants/{tenant}/budget`, as its quota is `tenants/{tenant}/quota`.
const budgetSegment = "/budget"

// TenantBudgetServer is the Connect adapter for capability.TenantBudgets —
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
	Usage capability.TenantBudgets
}

// NewTenantBudgetServer wires the handler. usage may be nil — the
// capability subsystem is opt-in (cfg.Capability.Enabled). When nil,
// every RPC returns CodeUnavailable so the operator notices the
// misconfig immediately rather than getting silent NULL responses.
func NewTenantBudgetServer(usage capability.TenantBudgets) *TenantBudgetServer {
	return &TenantBudgetServer{Usage: usage}
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
		if errors.Is(err, capability.ErrTenantBudgetNotFound) {
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
	// An absent max_budget lifts the cap and leaves the unit to the
	// existing row (or DEFAULT 'USD' on first insert): unit stays "".
	budget, unit, err := apiutil.NanosOf("max_budget", m.GetMaxBudget())
	if err != nil {
		return nil, err
	}
	args := capability.SetTenantBudgetRequest{
		TenantID:        tenantID,
		MaxBudgetAmount: budget,
		UnitCode:        unit,
		ResetSpend:      m.GetResetSpend(),
		ExpectedVersion: expected,
	}
	if pe := m.GetPeriodEnd(); pe != nil {
		t := pe.AsTime()
		args.PeriodEnd = &t
	}
	// tenant_budgets carries the standard RLS policy, whose WITH CHECK pins
	// writes to paladin_session_tenant_id(). A platform admin setting another
	// tenant's cap therefore has to act AS that tenant for the write, or
	// Postgres refuses the insert with "new row violates row-level security
	// policy" — which is what happened: creating a budget from the console
	// failed for every tenant except the admin's own. SetQuota, the same shape
	// on the same policy, has always done this.
	tb, err := s.Usage.SetTenantBudget(auth.WithActingTenant(ctx, tenantID), args)
	if err != nil {
		// A version mismatch is the caller's to resolve — re-read and retry —
		// not a server fault, so it must not read as Internal.
		if errors.Is(err, capability.ErrTenantBudgetVersionMismatch) {
			return nil, connect.NewError(connect.CodeAborted, err.Error()).WithCause(err)
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
	rows, next, err := s.Usage.ListTenantBudgets(ctx, capability.ListTenantBudgetsRequest{
		ThresholdPct:    m.GetThresholdPct(),
		UnlimitedOnly:   m.GetUnlimitedOnly(),
		ExcludeInactive: m.GetExcludeInactive(),
		Limit:           m.GetLimit(),
		Cursor:          m.GetPageToken(),
	})
	if err != nil {
		if errors.Is(err, capability.ErrInvalidRequest) { // a page token the store cannot read
			return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
		}
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	out := &pb.TenantBudgetServiceSummarizeResponse{
		Summaries:     make([]*pb.TenantBudgetSummary, 0, len(rows)),
		NextPageToken: next,
	}
	for _, r := range rows {
		out.Summaries = append(out.Summaries, &pb.TenantBudgetSummary{
			TenantId:       r.TenantID.String(),
			Slug:           r.Slug,
			DisplayName:    r.DisplayName,
			Budget:         tenantBudgetToProto(r.Budget),
			UtilisationPct: r.UtilisationPct,
		})
	}
	return out, nil
}

var _ paladinadminv1connect.TenantBudgetServiceHandler = (*TenantBudgetServer)(nil)

// tenantBudgetToProto converts the internal snapshot to the wire shape.
// Times that are zero come back as nil so the wire payload is tighter
// (Connect-JSON doesn't need to ship the epoch timestamp).
func tenantBudgetToProto(tb capability.TenantBudget) *pb.TenantBudget {
	out := &pb.TenantBudget{
		TenantId:        tb.TenantID.String(),
		MaxBudget:       apiutil.MoneyOf(tb.UnitCode, tb.MaxBudgetAmount),
		Spent:           apiutil.MoneyOf(tb.UnitCode, tb.SpentAmount),
		ResourceVersion: convx.ResourceVersion(tb.ResourceVersion),
	}
	if !tb.PeriodStart.IsZero() {
		out.PeriodStart = timestamppb.New(tb.PeriodStart)
	}
	if tb.PeriodEnd != nil && !tb.PeriodEnd.IsZero() {
		out.PeriodEnd = timestamppb.New(*tb.PeriodEnd)
	}
	if !tb.UpdatedAt.IsZero() {
		out.UpdatedAt = timestamppb.New(tb.UpdatedAt)
	}
	return out
}
