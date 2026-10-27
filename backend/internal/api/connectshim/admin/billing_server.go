package admin

import (
	"context"
	"fmt"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/billingh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"

	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// BillingServer is the Connect adapter for billingh.Handler. Pure
// shape conversion — Cedar gating + SQL aggregation lives in the
// handler, this adapter only translates wire types.
type BillingServer struct {
	paladinadminv1connect.UnimplementedBillingServiceHandler
	H billingHandler
	// Tenants names the tenants ListTenantSpend lists: the ledger carries
	// their ids alone.
	Tenants tenanth.Names
}

// NewBillingServer wires the handler. h may be nil — callers that
// reach a nil-H server get CodeUnavailable (matches the
// TenantBudgetServer disabled-subsystem shape).
func NewBillingServer(h *billingh.Handler, tenants tenanth.Names) *BillingServer {
	// H is an interface, so a nil *billingh.Handler assigned straight into it
	// would produce a NON-nil interface value and the `s.H == nil`
	// disabled-subsystem checks below would silently stop firing — the
	// RPCs would panic on a nil receiver instead of answering
	// CodeUnavailable. Leave the field zero instead.
	if h == nil {
		return &BillingServer{Tenants: tenants}
	}
	return &BillingServer{H: h, Tenants: tenants}
}

func (s *BillingServer) GetTenantSummary(
	ctx context.Context,
	req *pb.GetTenantSummaryRequest,
) (*pb.GetTenantSummaryResponse, error) {
	if s.H == nil {
		return nil, connect.Errorf(connect.CodeUnavailable, "billing: handler not wired")
	}
	tenantID, err := uuid.Parse(req.GetTenantId())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("tenant_id: %w", err))
	}
	sum, err := s.H.GetTenantSummary(ctx, tenantID,
		timeFromPB(req.GetPeriodStart()),
		timeFromPB(req.GetPeriodEnd()),
	)
	if err != nil {
		return nil, err
	}
	out := &pb.GetTenantSummaryResponse{
		Total:           apiutil.MoneyOf(sum.UnitCode, sum.TotalAmount),
		ChargeCount:     sum.ChargeCount,
		TopCapabilities: topEntriesToProto(sum.UnitCode, sum.TopCapabilities),
		TopActors:       topEntriesToProto(sum.UnitCode, sum.TopActors),
		TopOps:          topEntriesToProto(sum.UnitCode, sum.TopOps),
	}
	setSummaryCeiling(out, sum.Ceiling, sum.CeilingUnit)
	return out, nil
}

func (s *BillingServer) GetTenantTimeSeries(
	ctx context.Context,
	req *pb.GetTenantTimeSeriesRequest,
) (*pb.GetTenantTimeSeriesResponse, error) {
	if s.H == nil {
		return nil, connect.Errorf(connect.CodeUnavailable, "billing: handler not wired")
	}
	tenantID, err := uuid.Parse(req.GetTenantId())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("tenant_id: %w", err))
	}
	ts, err := s.H.GetTenantTimeSeries(ctx, tenantID,
		timeFromPB(req.GetPeriodStart()),
		timeFromPB(req.GetPeriodEnd()),
		req.GetGranularity(),
	)
	if err != nil {
		return nil, err
	}
	return &pb.GetTenantTimeSeriesResponse{Buckets: bucketsToProto(ts)}, nil
}

// GetPlatformTimeSeries is GetTenantTimeSeries over every tenant's charges.
func (s *BillingServer) GetPlatformTimeSeries(
	ctx context.Context,
	req *pb.GetPlatformTimeSeriesRequest,
) (*pb.GetPlatformTimeSeriesResponse, error) {
	if s.H == nil {
		return nil, connect.Errorf(connect.CodeUnavailable, "billing: handler not wired")
	}
	ts, err := s.H.GetPlatformTimeSeries(ctx,
		timeFromPB(req.GetPeriodStart()),
		timeFromPB(req.GetPeriodEnd()),
		req.GetGranularity(),
	)
	if err != nil {
		return nil, err
	}
	return &pb.GetPlatformTimeSeriesResponse{Buckets: bucketsToProto(ts)}, nil
}

// bucketsToProto is a series' buckets, each spent in the series' unit.
func bucketsToProto(ts *billingh.TimeSeries) []*pb.TimeBucket {
	out := make([]*pb.TimeBucket, 0, len(ts.Buckets))
	for _, b := range ts.Buckets {
		out = append(out, &pb.TimeBucket{
			Start:       timestamppb.New(b.Start),
			Spent:       apiutil.MoneyOf(ts.UnitCode, b.Amount),
			ChargeCount: b.ChargeCount,
		})
	}
	return out
}

// ListTenantSpend lists the tenants that spent over the period, the most
// first, each named as its tenant row names it — a tenant in the trash too,
// marked deleted, since what it spent still counts.
func (s *BillingServer) ListTenantSpend(
	ctx context.Context,
	req *pb.ListTenantSpendRequest,
) (*pb.ListTenantSpendResponse, error) {
	if s.H == nil {
		return nil, connect.Errorf(connect.CodeUnavailable, "billing: handler not wired")
	}
	spend, truncated, err := s.H.ListTenantSpend(ctx,
		timeFromPB(req.GetPeriodStart()), timeFromPB(req.GetPeriodEnd()), int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(spend))
	for _, t := range spend {
		ids = append(ids, t.TenantID)
	}
	names, err := s.Tenants.TenantNames(ctx, ids)
	if err != nil {
		return nil, rpcerr.New(connect.CodeInternal, fmt.Errorf("billing: tenant names: %w", err))
	}
	out := &pb.ListTenantSpendResponse{Tenants: make([]*pb.TenantSpend, 0, len(spend)), Truncated: truncated}
	for _, t := range spend {
		name := names[t.TenantID]
		out.Tenants = append(out.Tenants, &pb.TenantSpend{
			TenantId:    t.TenantID.String(),
			Slug:        name.Slug,
			DisplayName: name.DisplayName,
			Deleted:     name.Deleted,
			Spent:       apiutil.MoneyOf(t.UnitCode, t.Amount),
			ChargeCount: t.ChargeCount,
		})
	}
	return out, nil
}

var _ paladinadminv1connect.BillingServiceHandler = (*BillingServer)(nil)

func topEntriesToProto(unit string, in []billingh.TopEntry) []*pb.TopEntry {
	out := make([]*pb.TopEntry, 0, len(in))
	for _, e := range in {
		out = append(out, &pb.TopEntry{
			Label:       e.Label,
			Spent:       apiutil.MoneyOf(unit, e.Amount),
			ChargeCount: e.ChargeCount,
		})
	}
	return out
}

// timeFromPB returns zero time when the proto Timestamp is unset.
// The handler treats zero as "default lookback".
func timeFromPB(t *timestamppb.Timestamp) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.AsTime()
}

// setSummaryCeiling writes the tenant's budget ceiling, in unit, onto out;
// a nil ceiling — a tenant with no budget row — leaves it unset.
func setSummaryCeiling(out *pb.GetTenantSummaryResponse, ceiling *limes.BudgetCeiling, unit string) {
	if ceiling == nil {
		return
	}
	limit, unlimited := apiutil.BudgetCeilingArms(*ceiling, unit)
	if unlimited != nil {
		out.BudgetCeiling = &pb.GetTenantSummaryResponse_UnlimitedBudget{UnlimitedBudget: unlimited}
		return
	}
	out.BudgetCeiling = &pb.GetTenantSummaryResponse_MaxBudget{MaxBudget: limit}
}

func (s *BillingServer) GetBillingSettings(
	ctx context.Context,
	_ *pb.GetBillingSettingsRequest,
) (*pb.GetBillingSettingsResponse, error) {
	if s.H == nil {
		return nil, connect.Errorf(connect.CodeUnavailable, "billing: handler not wired")
	}
	currency, err := s.H.Currency(ctx)
	if err != nil {
		return nil, err
	}
	return &pb.GetBillingSettingsResponse{CurrencyCode: currency}, nil
}
