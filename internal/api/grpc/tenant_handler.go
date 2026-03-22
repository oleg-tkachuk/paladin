package grpcapi

import (
	"context"

	"connectrpc.com/connect"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/utils"
	"google.golang.org/grpc"
)

// TenantHandler implements grpcapiconnect.TenantServiceHandler.
type TenantHandler struct {
	log *zap.Logger
	svc domain.TenantService
}

// NewTenantHandler creates a new TenantHandler.
func NewTenantHandler(log *zap.Logger, svc domain.TenantService) *TenantHandler {
	return &TenantHandler{log: log, svc: svc}
}

func (h *TenantHandler) CreateTenant(ctx context.Context, req *connect.Request[CreateTenantRequest]) (*connect.Response[CreateTenantResponse], error) {
	msg := req.Msg
	tenantID := utils.TenantIDFromContext(ctx, msg.TenantId)

	out, err := h.svc.Create(ctx, tenantID, msg.DisplayName, msg.Labels, msg.Tags)
	if err != nil {
		return nil, grpcError(err)
	}

	return connect.NewResponse(&CreateTenantResponse{
		Tenant: tenantToProto(out),
	}), nil
}

func (h *TenantHandler) GetTenant(ctx context.Context, req *connect.Request[GetTenantRequest]) (*connect.Response[GetTenantResponse], error) {
	msg := req.Msg
	tenantID := utils.TenantIDFromContext(ctx, msg.TenantId)

	out, err := h.svc.Get(ctx, tenantID)
	if err != nil {
		return nil, grpcError(err)
	}

	return connect.NewResponse(&GetTenantResponse{
		Tenant: tenantToProto(out),
	}), nil
}

func (h *TenantHandler) ListTenants(ctx context.Context, req *connect.Request[ListTenantsRequest]) (*connect.Response[ListTenantsResponse], error) {
	msg := req.Msg

	filter := domain.ListTenantsFilter{
		Limit:  int(msg.PageSize),
		Cursor: msg.PageToken,
	}
	if filter.Limit <= 0 {
		filter.Limit = 20
	}

	if msg.Filter != nil {
		if msg.Filter.Search != "" {
			filter.Search = &msg.Filter.Search
		}
		if len(msg.Filter.Labels) > 0 {
			filter.LabelSelector = msg.Filter.Labels
		}
		if len(msg.Filter.Tags) > 0 {
			filter.TagSelector = msg.Filter.Tags
		}
	}

	if msg.OrderBy != "" {
		filter.SortBy = msg.OrderBy
	}
	switch msg.SortOrder {
	case SortOrder_SORT_ORDER_DESC:
		filter.SortOrder = domain.SortOrderDesc
	case SortOrder_SORT_ORDER_ASC:
		filter.SortOrder = domain.SortOrderAsc
	}

	tenants, nextCursor, total, err := h.svc.List(ctx, filter)
	if err != nil {
		return nil, grpcError(err)
	}

	items := make([]*Tenant, 0, len(tenants))
	for i := range tenants {
		items = append(items, tenantToProto(&tenants[i]))
	}

	return connect.NewResponse(&ListTenantsResponse{
		Tenants:       items,
		NextPageToken: nextCursor,
		TotalCount:    total,
	}), nil
}

func (h *TenantHandler) DeleteTenant(ctx context.Context, req *connect.Request[DeleteTenantRequest]) (*connect.Response[DeleteTenantResponse], error) {
	msg := req.Msg
	tenantID := utils.TenantIDFromContext(ctx, msg.TenantId)

	err := h.svc.Delete(ctx, tenantID)
	if err != nil {
		return nil, grpcError(err)
	}

	return connect.NewResponse(&DeleteTenantResponse{}), nil
}

func (h *TenantHandler) UpdateTenantMetadata(ctx context.Context, req *connect.Request[UpdateTenantMetadataRequest]) (*connect.Response[UpdateTenantMetadataResponse], error) {
	msg := req.Msg

	// domain.TenantService.PatchMetadata takes map[string]interface{} for labelsPatch.
	// We need to convert map[string]string to map[string]interface{}.
	labelsPatch := make(map[string]interface{}, len(msg.Labels))
	for k, v := range msg.Labels {
		labelsPatch[k] = v
	}

	out, err := h.svc.PatchMetadata(ctx, utils.TenantIDFromContext(ctx, msg.TenantId), labelsPatch, msg.Tags, msg.DisplayName)
	if err != nil {
		return nil, grpcError(err)
	}

	return connect.NewResponse(&UpdateTenantMetadataResponse{
		Tenant: tenantToProto(out),
	}), nil
}

// ────────────────────────────────────────────────────────────────────────────
// gRPC Bridge
// ────────────────────────────────────────────────────────────────────────────

type tenantGRPCServer struct {
	UnimplementedTenantServiceServer
	h *TenantHandler
}

// RegisterGRPC registers the handler as a native gRPC server.
func (h *TenantHandler) RegisterGRPC(srv *grpc.Server) {
	RegisterTenantServiceServer(srv, &tenantGRPCServer{h: h})
}

func (s *tenantGRPCServer) CreateTenant(ctx context.Context, req *CreateTenantRequest) (*CreateTenantResponse, error) {
	res, err := s.h.CreateTenant(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (s *tenantGRPCServer) GetTenant(ctx context.Context, req *GetTenantRequest) (*GetTenantResponse, error) {
	res, err := s.h.GetTenant(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (s *tenantGRPCServer) ListTenants(ctx context.Context, req *ListTenantsRequest) (*ListTenantsResponse, error) {
	res, err := s.h.ListTenants(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (s *tenantGRPCServer) DeleteTenant(ctx context.Context, req *DeleteTenantRequest) (*DeleteTenantResponse, error) {
	res, err := s.h.DeleteTenant(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (s *tenantGRPCServer) UpdateTenantMetadata(ctx context.Context, req *UpdateTenantMetadataRequest) (*UpdateTenantMetadataResponse, error) {
	res, err := s.h.UpdateTenantMetadata(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}
