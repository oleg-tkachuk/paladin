package admin

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/billingh"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1/paladinadminv1connect"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
)

// BillingServer is the Connect adapter for billingh.Handler. Pure
// shape conversion — Cedar gating + SQL aggregation lives in the
// handler, this adapter only translates wire types.
type BillingServer struct {
	paladinadminv1connect.UnimplementedBillingServiceHandler
	H billingHandler
}

// NewBillingServer wires the handler. h may be nil — callers that
// reach a nil-H server get CodeUnavailable (matches the
// TenantBudgetServer disabled-subsystem shape).
func NewBillingServer(h *billingh.Handler) *BillingServer {
	// H is an interface, so a nil *billingh.Handler assigned straight into it
	// would produce a NON-nil interface value and the `s.H == nil`
	// disabled-subsystem checks below would silently stop firing — the
	// RPCs would panic on a nil receiver instead of answering
	// CodeUnavailable. Leave the field zero instead.
	if h == nil {
		return &BillingServer{}
	}
	return &BillingServer{H: h}
}

func (s *BillingServer) GetTenantSummary(
	ctx context.Context,
	req *connect.Request[pb.GetTenantSummaryRequest],
) (*connect.Response[pb.GetTenantSummaryResponse], error) {
	if s.H == nil {
		return nil, connect.NewError(connect.CodeUnavailable,
			fmt.Errorf("billing: handler not wired"))
	}
	tenantID, err := uuid.Parse(req.Msg.GetTenantId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("tenant_id: %w", err))
	}
	sum, err := s.H.GetTenantSummary(ctx, tenantID,
		timeFromPB(req.Msg.GetPeriodStart()),
		timeFromPB(req.Msg.GetPeriodEnd()),
	)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.GetTenantSummaryResponse{
		TotalAmount:     sum.TotalAmount,
		UnitCode:        sum.UnitCode,
		MaxBudgetAmount: sum.MaxBudgetAmount,
		ChargeCount:     sum.ChargeCount,
		TopCapabilities: topEntriesToProto(sum.TopCapabilities),
		TopActors:       topEntriesToProto(sum.TopActors),
		TopOps:          topEntriesToProto(sum.TopOps),
	}), nil
}

func (s *BillingServer) GetTenantTimeSeries(
	ctx context.Context,
	req *connect.Request[pb.GetTenantTimeSeriesRequest],
) (*connect.Response[pb.GetTenantTimeSeriesResponse], error) {
	if s.H == nil {
		return nil, connect.NewError(connect.CodeUnavailable,
			fmt.Errorf("billing: handler not wired"))
	}
	tenantID, err := uuid.Parse(req.Msg.GetTenantId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("tenant_id: %w", err))
	}
	ts, err := s.H.GetTenantTimeSeries(ctx, tenantID,
		timeFromPB(req.Msg.GetPeriodStart()),
		timeFromPB(req.Msg.GetPeriodEnd()),
		req.Msg.GetGranularity(),
	)
	if err != nil {
		return nil, err
	}
	out := &pb.GetTenantTimeSeriesResponse{
		UnitCode: ts.UnitCode,
		Buckets:  make([]*pb.TimeBucket, 0, len(ts.Buckets)),
	}
	for _, b := range ts.Buckets {
		out.Buckets = append(out.Buckets, &pb.TimeBucket{
			Start:       timestamppb.New(b.Start),
			Amount:      b.Amount,
			ChargeCount: b.ChargeCount,
		})
	}
	return connect.NewResponse(out), nil
}

var _ paladinadminv1connect.BillingServiceHandler = (*BillingServer)(nil)

func topEntriesToProto(in []billingh.TopEntry) []*pb.TopEntry {
	out := make([]*pb.TopEntry, 0, len(in))
	for _, e := range in {
		out = append(out, &pb.TopEntry{
			Label:       e.Label,
			Amount:      e.Amount,
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
