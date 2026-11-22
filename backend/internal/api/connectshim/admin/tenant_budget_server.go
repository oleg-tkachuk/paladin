package admin

import (
	"context"
	"errors"
	"fmt"
	"math"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/convx"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"

	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// budgetSegment names a tenant's budget in a resource name:
// `tenants/{tenant}/budget`, as its quota is `tenants/{tenant}/quota`.
const budgetSegment = "/budget"

// tenantBudgetReadExceptions answer Get: a tenant with no budget on record
// is NotFound to the admin API, where the table, for a meter that needs one,
// says FailedPrecondition.
var tenantBudgetReadExceptions = auth.Exceptions{
	meter.CodeTenantBudgetNotFound: {Code: connect.CodeNotFound},
}

// TenantBudgetServer is the Connect adapter for meter.TenantBudgets —
// the tenant-ceiling half of the usage store. It depends on nothing that
// meters, so it cannot charge or refund.
//
// Authorization: the admin plane's interceptors authenticate the caller and
// nothing more; this server authorizes each call itself, against Policy.
// Get and ListReservations read one tenant's budget: ReadTenantBudget on that
// tenant, for a caller in it or a platform admin. Set and Summarize are the
// platform's: the platform.admin role, then ManageTenantBudget on the tenant
// set, or ReadTenantBudget on the caller's own tenant for the summary across
// every tenant.
type TenantBudgetServer struct {
	paladinadminv1connect.UnimplementedTenantBudgetServiceHandler
	Usage TenantBudgetStore
	// Tenants names the tenants a summary lists: the budget rows carry
	// their ids alone.
	Tenants tenanth.Names
	// Policy decides every call.
	Policy cedar.Authorizer
}

// TenantBudgetStore is the part of the usage store the service serves: the
// tenant budgets, and the holds that make up what each one has reserved.
type TenantBudgetStore interface {
	meter.TenantBudgets
	meter.TenantReservationLister
}

// budgetUnavailable answers every RPC while the capability subsystem is off.
const budgetUnavailable = "capability subsystem disabled; tenant budget unavailable"

// errBudgetDisabled is the cause of budgetDisabled's answer, by which a log
// line tells the deployment's choice from the store failing.
var errBudgetDisabled = errors.New(budgetUnavailable)

// budgetDisabled is what an authorized caller is answered while the
// capability subsystem is off.
func budgetDisabled() error {
	return connect.NewError(connect.CodeUnavailable, budgetUnavailable).WithCause(errBudgetDisabled)
}

// NewTenantBudgetServer wires the handler. usage may be nil — the
// capability subsystem is opt-in (cfg.Capability.Enabled). When nil,
// every RPC a caller is authorized for returns CodeUnavailable so the
// operator notices the misconfig immediately rather than getting silent
// NULL responses. tenants is required with usage: Summarize names each
// row's tenant. policy is required.
func NewTenantBudgetServer(usage TenantBudgetStore, tenants tenanth.Names, policy cedar.Authorizer) *TenantBudgetServer {
	if policy == nil {
		panic("admin: tenant budget server needs a policy authorizer")
	}
	return &TenantBudgetServer{Usage: usage, Tenants: tenants, Policy: policy}
}

// The operations a log line names.
const (
	opBudgetGet              = "TenantBudgetService.Get"
	opBudgetSet              = "TenantBudgetService.Set"
	opBudgetSummarize        = "TenantBudgetService.Summarize"
	opBudgetListReservations = "TenantBudgetService.ListReservations"
)

// msgOtherTenantsBudget refuses a caller reading a tenant's budget from
// another tenant without the platform.admin role.
const msgOtherTenantsBudget = "another tenant's budget requires the platform.admin role"

// authorizeRead admits a read of tenantID's budget — the caller's own tenant,
// or any for a platform admin — then asks Policy for ReadTenantBudget on it.
// On allow it returns ctx acting as tenantID: the budget and its holds are
// under row-level security, whose USING clause hides another tenant's rows,
// so a platform admin reading someone else's budget would get NotFound for a
// row that exists.
func (s *TenantBudgetServer) authorizeRead(ctx context.Context, tenantID uuid.UUID) (context.Context, error) {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	if p.TenantID != tenantID && !p.HasRole(apiutil.RolePlatformAdmin) {
		return nil, connect.NewError(connect.CodePermissionDenied, msgOtherTenantsBudget)
	}
	if _, err := apiutil.Authorize(ctx, s.Policy, cedar.ActionReadTenantBudget, &cedar.Resource{TenantID: tenantID}); err != nil {
		return nil, err
	}
	return auth.WithActingTenant(ctx, tenantID), nil
}

// authorizeSet admits setting tenantID's budget: the platform.admin role, as
// the ceiling bounds what the tenant may spend, then ManageTenantBudget on
// the tenant. It returns ctx acting as tenantID: limes.tenant_budgets carries
// row-level security whose WITH CHECK pins writes to the tenant the request
// acts on, so a platform admin setting another tenant's cap has to act AS
// that tenant or Postgres refuses the insert.
func (s *TenantBudgetServer) authorizeSet(ctx context.Context, tenantID uuid.UUID) (context.Context, error) {
	if err := apiutil.RequireRole(ctx, apiutil.RolePlatformAdmin); err != nil {
		return nil, err
	}
	if _, err := apiutil.Authorize(ctx, s.Policy, cedar.ActionManageTenantBudget, &cedar.Resource{TenantID: tenantID}); err != nil {
		return nil, err
	}
	return auth.WithActingTenant(ctx, tenantID), nil
}

// authorizeSummary admits a summary of every tenant's budget: the
// platform.admin role, which alone reads past the caller's tenant, then
// ReadTenantBudget on the caller's own tenant. It returns ctx reading every
// tenant's rows with no write allowed.
func (s *TenantBudgetServer) authorizeSummary(ctx context.Context) (context.Context, error) {
	if err := apiutil.RequireRole(ctx, apiutil.RolePlatformAdmin); err != nil {
		return nil, err
	}
	if _, err := apiutil.AuthorizeOwnTenant(ctx, s.Policy, cedar.ActionReadTenantBudget); err != nil {
		return nil, err
	}
	return auth.WithCrossTenantRead(ctx), nil
}

// logOutcome logs a failed call to op on tenant ("" for none): a fault of
// the server or its store at Error, a refusal of the request at Warn, and a
// tenant with no budget yet — a state the console asks about on every view
// of the tenant — at Debug.
func logOutcome(ctx context.Context, op, tenant string, err error) {
	if err == nil {
		return
	}
	fields := []zap.Field{
		zap.String("op", op), zap.String("tenant_id", tenant),
		zap.String("code", connect.CodeOf(err).String()), zap.Error(err),
	}
	l := logger.FromContext(ctx)
	switch {
	case budgetFault(err):
		l.Error("tenant budget call failed", fields...)
	case connect.CodeOf(err) == connect.CodeNotFound:
		l.Debug("tenant budget not found", fields...)
	default:
		l.Warn("tenant budget call refused", fields...)
	}
}

// budgetFault reports whether err is the server's failure rather than a
// refusal of the request. The subsystem being off is the deployment's
// choice, not a failure.
func budgetFault(err error) bool {
	if errors.Is(err, errBudgetDisabled) {
		return false
	}
	switch connect.CodeOf(err) {
	case connect.CodeInternal, connect.CodeUnknown, connect.CodeUnavailable, connect.CodeDataLoss:
		return true
	}
	return false
}

func (s *TenantBudgetServer) Get(
	ctx context.Context,
	req *pb.TenantBudgetServiceGetRequest,
) (_ *pb.TenantBudgetServiceGetResponse, err error) {
	defer func() { logOutcome(ctx, opBudgetGet, req.GetTenantId(), err) }()
	tenantID, err := uuid.Parse(req.GetTenantId())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("tenant_id: %w", err))
	}
	acting, err := s.authorizeRead(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if s.Usage == nil {
		return nil, budgetDisabled()
	}
	tb, err := s.Usage.GetTenantBudget(acting, tenantID)
	if err != nil {
		return nil, auth.AnswerCapabilityError(err, tenantBudgetReadExceptions)
	}
	return &pb.TenantBudgetServiceGetResponse{
		Budget: tenantBudgetToProto(tb),
	}, nil
}

func (s *TenantBudgetServer) Set(
	ctx context.Context,
	req *pb.TenantBudgetServiceSetRequest,
) (_ *pb.TenantBudgetServiceSetResponse, err error) {
	defer func() { logOutcome(ctx, opBudgetSet, req.GetTenantId(), err) }()
	tenantID, err := uuid.Parse(req.GetTenantId())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("tenant_id: %w", err))
	}
	// The request names the tenant by id, so the audit row would name nothing
	// and the change would be in no tenant's trail.
	apiutil.StashResource(ctx, apiutil.TenantNamePrefix+tenantID.String()+budgetSegment)
	acting, err := s.authorizeSet(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if s.Usage == nil {
		return nil, budgetDisabled()
	}
	args, err := setBudgetRequest(tenantID, req)
	if err != nil {
		return nil, err
	}
	tb, err := s.Usage.SetTenantBudget(acting, args)
	if err != nil {
		// By the table: a version mismatch is Aborted, the caller's to re-read
		// and retry; re-denominating a tenant that still counts spend or holds
		// in its unit is FailedPrecondition until they are reset or released.
		return nil, auth.AnswerCapabilityError(err, nil)
	}
	return &pb.TenantBudgetServiceSetResponse{
		Budget: tenantBudgetToProto(tb),
	}, nil
}

// setBudgetRequest is the store's write for a Set request on tenantID, or the
// InvalidArgument its fields earn.
func setBudgetRequest(tenantID uuid.UUID, m *pb.TenantBudgetServiceSetRequest) (meter.SetTenantBudgetRequest, error) {
	expected, err := convx.ParseRV(m.GetResourceVersion())
	if err != nil {
		return meter.SetTenantBudgetRequest{}, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("resource_version: %w", err))
	}
	if err := apiutil.RefuseRemovedFields(m); err != nil {
		return meter.SetTenantBudgetRequest{}, err
	}
	// A budget that names no unit leaves the unit to the existing row, or to
	// the platform's currency on the first one (capabilitypg.UsageStore). A
	// zero limit allows no spend.
	ceiling, _, set, err := apiutil.BudgetCeilingOf(fieldBudgetCeiling, m.GetMaxBudget(), m.GetUnlimitedBudget())
	if err != nil {
		return meter.SetTenantBudgetRequest{}, err
	}
	if !set {
		return meter.SetTenantBudgetRequest{}, connect.NewError(connect.CodeInvalidArgument,
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
	return args, nil
}

// Summarize returns one row per tenant joining the budget snapshot
// with slug + display_name, for a platform admin alone (authorizeSummary):
// a tenant admin reads its own tenant's budget with Get. unlimited_only and
// threshold_pct are mutually exclusive (server-enforced).
//
// exclude_inactive drops the rows of tenants in the trash from each page
// after it is read, so such a page can come back shorter than limit with
// more to follow; next_page_token says whether there are.
func (s *TenantBudgetServer) Summarize(
	ctx context.Context,
	req *pb.TenantBudgetServiceSummarizeRequest,
) (_ *pb.TenantBudgetServiceSummarizeResponse, err error) {
	defer func() { logOutcome(ctx, opBudgetSummarize, "", err) }()
	every, err := s.authorizeSummary(ctx)
	if err != nil {
		return nil, err
	}
	if s.Usage == nil {
		return nil, budgetDisabled()
	}
	m := req
	if m.GetUnlimitedOnly() && m.GetThresholdPct() > 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, "unlimited_only is mutually exclusive with a non-zero threshold_pct")
	}
	page, err := s.Usage.ListTenantBudgets(every, budgetListRequest(m))
	if err != nil {
		// A page token the store cannot read is InvalidArgument, by the table.
		return nil, auth.AnswerCapabilityError(err, nil)
	}
	ids := make([]uuid.UUID, 0, len(page.Items))
	for _, b := range page.Items {
		ids = append(ids, b.TenantID)
	}
	names, err := s.Tenants.TenantNames(ctx, ids)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &pb.TenantBudgetServiceSummarizeResponse{
		Summaries:     budgetSummaries(page.Items, names, m.GetExcludeInactive()),
		NextPageToken: page.Next,
	}, nil
}

// budgetSummaries is one row per budget, named by names; excludeInactive
// drops the rows of tenants in the trash or not found.
func budgetSummaries(budgets []meter.TenantBudget, names map[uuid.UUID]tenanth.Name, excludeInactive bool) []*pb.TenantBudgetSummary {
	out := make([]*pb.TenantBudgetSummary, 0, len(budgets))
	for _, b := range budgets {
		name, ok := names[b.TenantID]
		if excludeInactive && (!ok || name.Deleted) {
			continue
		}
		pct, _ := b.Utilization() // an unlimited tenant reports 0
		out = append(out, &pb.TenantBudgetSummary{
			TenantId:       b.TenantID.String(),
			Slug:           name.Slug,
			DisplayName:    name.DisplayName,
			Budget:         tenantBudgetToProto(b),
			UtilisationPct: pct,
		})
	}
	return out
}

// ListReservations pages through the holds on a tenant's budget, acting as
// the tenant: the holds are under the same row-level security as the budget,
// and reading them is reading the budget (authorizeRead).
func (s *TenantBudgetServer) ListReservations(
	ctx context.Context,
	req *pb.TenantBudgetServiceListReservationsRequest,
) (_ *pb.TenantBudgetServiceListReservationsResponse, err error) {
	defer func() { logOutcome(ctx, opBudgetListReservations, req.GetTenantId(), err) }()
	tenantID, err := uuid.Parse(req.GetTenantId())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("tenant_id: %w", err))
	}
	acting, err := s.authorizeRead(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if s.Usage == nil {
		return nil, budgetDisabled()
	}
	page, err := s.Usage.ListTenantReservations(acting, meter.ListTenantReservationsRequest{
		TenantID: tenantID, Limit: int(req.GetPageSize()), Cursor: req.GetPageToken(),
	})
	if err != nil {
		// A page token the store cannot read is InvalidArgument, by the table.
		return nil, auth.AnswerCapabilityError(err, nil)
	}
	out := &pb.TenantBudgetServiceListReservationsResponse{
		Reservations:  make([]*pb.TenantReservation, 0, len(page.Items)),
		NextPageToken: page.Next,
	}
	for _, r := range page.Items {
		out.Reservations = append(out.Reservations, reservationToProto(r))
	}
	return out, nil
}

// reservationToProto is one hold as the API shows it.
func reservationToProto(r meter.Reservation) *pb.TenantReservation {
	return &pb.TenantReservation{
		Id:           r.ID.String(),
		CapabilityId: r.CapabilityID.String(),
		Amount:       apiutil.MoneyOf(r.UnitCode, r.Amount),
		Op:           string(r.Op),
		Actor:        r.Actor,
		ExpiresAt:    timestamppb.New(r.ExpiresAt),
	}
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
		Reserved:        apiutil.MoneyOf(tb.Ceiling.UnitCode, tb.ReservedAmount),
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
