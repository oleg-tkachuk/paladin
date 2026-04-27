package connectshim

import (
	"context"
	"encoding/json"
	"errors"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/v1/paladinv1connect"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/tenant"
)

// TenantServer bridges generated Connect to tenant.Handler.
type TenantServer struct {
	paladinv1connect.UnimplementedTenantServiceHandler
	H *tenant.Handler
}

func NewTenantServer(h *tenant.Handler) *TenantServer { return &TenantServer{H: h} }

func (s *TenantServer) CreateTenant(ctx context.Context, req *connect.Request[pb.CreateTenantRequest]) (*connect.Response[pb.Tenant], error) {
	m := req.Msg
	var id uuid.UUID
	if m.GetTenantId() != "" {
		parsed, err := uuid.Parse(m.GetTenantId())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		id = parsed
	}
	labelBytes, _ := json.Marshal(m.GetLabels())
	t, err := s.H.CreateTenant(ctx, tenant.CreateTenantArgs{
		TenantID:             id,
		DisplayName:          m.GetDisplayName(),
		Labels:               labelBytes,
		InheritedCedarPolicy: m.GetInheritedCedarPolicy(),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(tenantToProto(t)), nil
}

func (s *TenantServer) GetTenant(ctx context.Context, req *connect.Request[pb.GetTenantRequest]) (*connect.Response[pb.Tenant], error) {
	id, err := parseTenantName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	t, err := s.H.GetTenant(ctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(tenantToProto(t)), nil
}

func (s *TenantServer) UpdateTenant(ctx context.Context, req *connect.Request[pb.UpdateTenantRequest]) (*connect.Response[pb.Tenant], error) {
	m := req.Msg
	id, err := parseTenantName(m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := parseResourceVersion(m.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	args := tenant.UpdateTenantArgs{TenantID: id, ExpectedVersion: rv}
	for _, path := range m.GetUpdateMask().GetPaths() {
		switch path {
		case "display_name":
			v := m.GetDisplayName()
			args.DisplayName = &v
		case "labels":
			b, _ := json.Marshal(m.GetLabels())
			args.Labels = b
		case "inherited_cedar_policy":
			v := m.GetInheritedCedarPolicy()
			args.InheritedCedarPolicy = &v
		}
	}
	t, err := s.H.UpdateTenant(ctx, args)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(tenantToProto(t)), nil
}

func (s *TenantServer) DeleteTenant(ctx context.Context, req *connect.Request[pb.DeleteTenantRequest]) (*connect.Response[pb.DeleteTenantResponse], error) {
	id, err := parseTenantName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	// resource_version is not part of DeleteTenantRequest — handler accepts 0 as "no guard".
	if err := s.H.DeleteTenant(ctx, id, 0); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.DeleteTenantResponse{}), nil
}

func (s *TenantServer) ListTenants(ctx context.Context, req *connect.Request[pb.ListTenantsRequest]) (*connect.Response[pb.ListTenantsResponse], error) {
	m := req.Msg
	ts, next, err := s.H.ListTenants(ctx, m.GetPageSize(), m.GetPageToken())
	if err != nil {
		return nil, err
	}
	out := &pb.ListTenantsResponse{NextPageToken: next}
	for i := range ts {
		out.Tenants = append(out.Tenants, tenantToProto(&ts[i]))
	}
	return connect.NewResponse(out), nil
}

var _ paladinv1connect.TenantServiceHandler = (*TenantServer)(nil)

// sanity — silences the unused-import linter when generated code path changes.
var _ = errors.New
