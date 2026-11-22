package admin

import (
	"context"
	"fmt"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/billingh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
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

// What a caller is told by a server built with no handler: the deployment
// runs without the subsystem (the capability subsystem off, for billing).
const (
	msgBillingNotWired = "billing handler not wired"
	msgSystemNotWired  = "system handler not wired"
)

// UnwiredMessages is every message a server built with no handler answers
// with, outside MapError.
func UnwiredMessages() []string {
	return []string{msgBillingNotWired, msgSystemNotWired}
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
		return nil, connect.NewError(connect.CodeUnavailable, msgBillingNotWired)
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
		Refunded:        apiutil.MoneyOf(sum.UnitCode, sum.Refunded),
		ChargeCount:     sum.ChargeCount,
		TopCapabilities: topEntriesToProto(sum.UnitCode, sum.TopCapabilities),
		TopActors:       topEntriesToProto(sum.UnitCode, sum.TopActors),
		TopOps:          topEntriesToProto(sum.UnitCode, sum.TopOps),
	}
	setSummaryCeiling(out, sum)
	return out, nil
}

func (s *BillingServer) GetTenantTimeSeries(
	ctx context.Context,
	req *pb.GetTenantTimeSeriesRequest,
) (*pb.GetTenantTimeSeriesResponse, error) {
	if s.H == nil {
		return nil, connect.NewError(connect.CodeUnavailable, msgBillingNotWired)
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
		return nil, connect.NewError(connect.CodeUnavailable, msgBillingNotWired)
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

// ListTenantTimeSeries is GetTenantTimeSeries for each tenant named, in the
// order named.
func (s *BillingServer) ListTenantTimeSeries(
	ctx context.Context,
	req *pb.ListTenantTimeSeriesRequest,
) (*pb.ListTenantTimeSeriesResponse, error) {
	if s.H == nil {
		return nil, connect.NewError(connect.CodeUnavailable, msgBillingNotWired)
	}
	ids := make([]uuid.UUID, 0, len(req.GetTenantIds()))
	for _, raw := range req.GetTenantIds() {
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("tenant_ids: %w", err))
		}
		ids = append(ids, id)
	}
	got, err := s.H.ListTenantTimeSeries(ctx, ids,
		timeFromPB(req.GetPeriodStart()),
		timeFromPB(req.GetPeriodEnd()),
		req.GetGranularity(),
	)
	if err != nil {
		return nil, err
	}
	out := &pb.ListTenantTimeSeriesResponse{Series: make([]*pb.TenantTimeSeries, 0, len(got))}
	for i := range got {
		out.Series = append(out.Series, &pb.TenantTimeSeries{
			TenantId: got[i].TenantID.String(),
			Buckets:  bucketsToProto(&got[i].Series),
		})
	}
	return out, nil
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
		return nil, connect.NewError(connect.CodeUnavailable, msgBillingNotWired)
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
		logger.FromContext(ctx).Error("billing: tenant names unreadable",
			zap.Int("tenants", len(ids)), zap.Error(err))
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
			Refunded:    apiutil.MoneyOf(t.UnitCode, t.Refunded),
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

// setSummaryCeiling writes what sum knows of the tenant's budget onto out:
// its ceiling, in its unit; budget_unreadable for a budget that could not be
// read; nothing for a tenant with no budget row.
func setSummaryCeiling(out *pb.GetTenantSummaryResponse, sum *billingh.Summary) {
	switch {
	case sum.Budget == billingh.BudgetUnreadable:
		out.BudgetCeiling = &pb.GetTenantSummaryResponse_BudgetUnreadable{BudgetUnreadable: &pb.BudgetUnreadable{}}
		return
	case sum.Ceiling == nil:
		return
	}
	limit, unlimited := apiutil.BudgetCeilingArms(*sum.Ceiling, sum.CeilingUnit)
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
		return nil, connect.NewError(connect.CodeUnavailable, msgBillingNotWired)
	}
	currency, err := s.H.Currency(ctx)
	if err != nil {
		return nil, err
	}
	return &pb.GetBillingSettingsResponse{CurrencyCode: currency}, nil
}
