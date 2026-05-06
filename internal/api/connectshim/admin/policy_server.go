package admin

import (
	"context"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1/paladinadminv1connect"
	policyh "github.com/oleg-tkachuk/paladin/internal/api/v1/policy"
	"github.com/oleg-tkachuk/paladin/internal/auth"
)

type PolicyServer struct {
	paladinadminv1connect.UnimplementedPolicyServiceHandler
	H *policyh.Handler
}

func NewPolicyServer(h *policyh.Handler) *PolicyServer { return &PolicyServer{H: h} }

func (s *PolicyServer) Validate(ctx context.Context, req *connect.Request[pb.ValidateRequest]) (*connect.Response[pb.ValidateResponse], error) {
	ok, msg, err := s.H.ValidatePolicy(ctx, req.Msg.GetCedarPolicy())
	if err != nil {
		return nil, err
	}
	out := &pb.ValidateResponse{Ok: ok}
	if !ok {
		out.Diagnostics = []*pb.PolicyDiagnostic{{Severity: "error", Message: msg}}
	}
	return connect.NewResponse(out), nil
}

func (s *PolicyServer) SimulateAuthz(ctx context.Context, req *connect.Request[pb.SimulateAuthzRequest]) (*connect.Response[pb.SimulateAuthzResponse], error) {
	m := req.Msg
	var tenantID uuid.UUID
	if m.GetPrincipalTenantId() != "" {
		id, err := uuid.Parse(m.GetPrincipalTenantId())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		tenantID = id
	}
	out, err := s.H.SimulateAuthz(ctx, policyh.SimulateAuthzInput{
		PrincipalSubject:  m.GetPrincipalSubject(),
		PrincipalTenantID: tenantID,
		PrincipalRoles:    m.GetPrincipalRoles(),
		Action:            m.GetAction(),
		ResourceName:      m.GetResourceName(),
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&pb.SimulateAuthzResponse{
		Allowed:     out.Allowed,
		Explanation: out.Explanation,
	}), nil
}

func (s *PolicyServer) GetEffectivePolicy(ctx context.Context, req *connect.Request[pb.GetEffectivePolicyRequest]) (*connect.Response[pb.GetEffectivePolicyResponse], error) {
	// Caller-tenant fallback when the resource name does not embed one
	// (e.g. backend-only resources): use the JWT principal's tenant.
	var fallback uuid.UUID
	if p, perr := auth.PrincipalFromContext(ctx); perr == nil {
		fallback = p.TenantID
	}
	out, err := s.H.GetEffectivePolicy(ctx, req.Msg.GetResourceName(), fallback)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	resp := &pb.GetEffectivePolicyResponse{
		MergedCedarPolicy: out.MergedCedarPolicy,
	}
	for _, layer := range out.Layers {
		resp.Layers = append(resp.Layers, &pb.PolicyLayer{
			Source:      layer.Source,
			CedarPolicy: layer.CedarPolicy,
		})
	}
	return connect.NewResponse(resp), nil
}

var _ paladinadminv1connect.PolicyServiceHandler = (*PolicyServer)(nil)
