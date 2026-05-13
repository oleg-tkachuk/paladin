package admin

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1/paladinadminv1connect"
	"github.com/oleg-tkachuk/paladin/internal/capability"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
)

// TenantBudgetServer is the Connect adapter for capability.UsageStore's
// SetTenantBudget / GetTenantBudget. The capability.UsageStore is the
// authoritative seam for the runtime counters; this handler is a thin
// pass-through that does shape conversion + error mapping.
//
// Authorization: this handler runs on the admin plane behind the
// standard JWT + Cedar interceptor stack. cfg.Cedar policies gate
// who can read / write per-tenant budgets — typical setup is
// platform.admin (cross-tenant) and tenant.admin (own-tenant only).
type TenantBudgetServer struct {
	paladinadminv1connect.UnimplementedTenantBudgetServiceHandler
	Usage capability.UsageStore
}

// NewTenantBudgetServer wires the handler. usage may be nil — the
// capability subsystem is opt-in (cfg.Capability.Enabled). When nil,
// every RPC returns CodeUnavailable so the operator notices the
// misconfig immediately rather than getting silent NULL responses.
func NewTenantBudgetServer(usage capability.UsageStore) *TenantBudgetServer {
	return &TenantBudgetServer{Usage: usage}
}

func (s *TenantBudgetServer) Get(
	ctx context.Context,
	req *connect.Request[pb.TenantBudgetServiceGetRequest],
) (*connect.Response[pb.TenantBudgetServiceGetResponse], error) {
	if s.Usage == nil {
		return nil, connect.NewError(connect.CodeUnavailable,
			errors.New("capability subsystem disabled; tenant budget unavailable"))
	}
	tenantID, err := uuid.Parse(req.Msg.GetTenantId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("tenant_id: %w", err))
	}
	tb, err := s.Usage.GetTenantBudget(ctx, tenantID)
	if err != nil {
		if errors.Is(err, capability.ErrTenantBudgetNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&pb.TenantBudgetServiceGetResponse{
		Budget: tenantBudgetToProto(tb),
	}), nil
}

func (s *TenantBudgetServer) Set(
	ctx context.Context,
	req *connect.Request[pb.TenantBudgetServiceSetRequest],
) (*connect.Response[pb.TenantBudgetServiceSetResponse], error) {
	if s.Usage == nil {
		return nil, connect.NewError(connect.CodeUnavailable,
			errors.New("capability subsystem disabled; tenant budget unavailable"))
	}
	m := req.Msg
	tenantID, err := uuid.Parse(m.GetTenantId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("tenant_id: %w", err))
	}
	// Validate the optional unit_code at the boundary; the store
	// also re-validates but surfacing InvalidArgument to the caller
	// here is more useful than the generic Internal we'd otherwise
	// return. Empty string defers to the existing row's unit_code
	// (or DEFAULT 'USD' on first insert).
	unit := m.GetUnitCode()
	if unit != "" && !capability.IsAllowedUnitCode(unit) {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("unit_code: %q not in %v", unit, capability.AllowedUnitCodes))
	}
	args := capability.SetTenantBudgetArgs{
		TenantID:        tenantID,
		MaxBudgetAmount: m.GetMaxBudgetAmount(),
		UnitCode:        unit,
		ResetSpend:      m.GetResetSpend(),
	}
	if pe := m.GetPeriodEnd(); pe != nil {
		t := pe.AsTime()
		args.PeriodEnd = &t
	}
	tb, err := s.Usage.SetTenantBudget(ctx, args)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&pb.TenantBudgetServiceSetResponse{
		Budget: tenantBudgetToProto(tb),
	}), nil
}

// Summarize returns one row per tenant joining the budget snapshot
// with slug + display_name. The platform.admin gate is enforced by
// the standard interceptor stack; this handler just translates
// request → store args → proto. unlimited_only and threshold_pct
// are mutually exclusive (server-enforced).
func (s *TenantBudgetServer) Summarize(
	ctx context.Context,
	req *connect.Request[pb.TenantBudgetServiceSummarizeRequest],
) (*connect.Response[pb.TenantBudgetServiceSummarizeResponse], error) {
	if s.Usage == nil {
		return nil, connect.NewError(connect.CodeUnavailable,
			errors.New("capability subsystem disabled; tenant budget unavailable"))
	}
	m := req.Msg
	if m.GetUnlimitedOnly() && m.GetThresholdPct() > 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("unlimited_only is mutually exclusive with a non-zero threshold_pct"))
	}
	rows, err := s.Usage.ListTenantBudgets(ctx, capability.ListTenantBudgetsArgs{
		ThresholdPct:    m.GetThresholdPct(),
		UnlimitedOnly:   m.GetUnlimitedOnly(),
		ExcludeInactive: m.GetExcludeInactive(),
		Limit:           m.GetLimit(),
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := &pb.TenantBudgetServiceSummarizeResponse{
		Summaries: make([]*pb.TenantBudgetSummary, 0, len(rows)),
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
	return connect.NewResponse(out), nil
}

var _ paladinadminv1connect.TenantBudgetServiceHandler = (*TenantBudgetServer)(nil)

// tenantBudgetToProto converts the internal snapshot to the wire shape.
// Times that are zero come back as nil so the wire payload is tighter
// (Connect-JSON doesn't need to ship the epoch timestamp).
func tenantBudgetToProto(tb capability.TenantBudget) *pb.TenantBudget {
	unit := tb.UnitCode
	if unit == "" {
		unit = capability.DefaultUnitCode
	}
	out := &pb.TenantBudget{
		TenantId:        tb.TenantID.String(),
		MaxBudgetAmount: tb.MaxBudgetAmount,
		SpentAmount:     tb.SpentAmount,
		UnitCode:        unit,
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
