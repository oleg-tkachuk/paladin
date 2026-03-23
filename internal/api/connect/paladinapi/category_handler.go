package paladinapi

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/utils"
)

// CategoryHandler implements paladinapiconnect.CategoryServiceHandler.
type CategoryHandler struct {
	log *zap.Logger
	svc domain.CategoryService
}

// NewCategoryHandler creates a new CategoryHandler.
func NewCategoryHandler(log *zap.Logger, svc domain.CategoryService) *CategoryHandler {
	return &CategoryHandler{log: log, svc: svc}
}

func (h *CategoryHandler) CreateCategory(ctx context.Context, req *connect.Request[CreateCategoryRequest]) (*connect.Response[CreateCategoryResponse], error) {
	msg := req.Msg
	authorizedTenantID := utils.TenantIDFromContext(ctx, "")
	requestedTenantID := msg.TenantId

	if requestedTenantID == "" {
		requestedTenantID = authorizedTenantID
	}

	// Authorization check: only SystemAdmin, DefaultTenant (local dev), or the tenant themselves.
	if authorizedTenantID != "" &&
		authorizedTenantID != utils.SystemAdminTenant &&
		authorizedTenantID != utils.DefaultTenant &&
		authorizedTenantID != requestedTenantID {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("cannot create or update categories for other tenants"))
	}

	out, err := h.svc.Create(ctx, requestedTenantID, msg.Slug, msg.Name, msg.Description)
	if err != nil {
		return nil, mapError(err)
	}

	return connect.NewResponse(&CreateCategoryResponse{
		Category: categoryToProto(out),
	}), nil
}

func (h *CategoryHandler) GetCategory(ctx context.Context, req *connect.Request[GetCategoryRequest]) (*connect.Response[GetCategoryResponse], error) {
	msg := req.Msg
	authorizedTenantID := utils.TenantIDFromContext(ctx, "")
	requestedTenantID := msg.TenantId

	if requestedTenantID == "" {
		requestedTenantID = authorizedTenantID
	}

	// Authorization check: only SystemAdmin, DefaultTenant, or the tenant themselves.
	if authorizedTenantID != "" &&
		authorizedTenantID != utils.SystemAdminTenant &&
		authorizedTenantID != utils.DefaultTenant &&
		authorizedTenantID != requestedTenantID {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("cannot access categories of other tenants"))
	}

	out, err := h.svc.Get(ctx, requestedTenantID, msg.Slug)
	if err != nil {
		return nil, mapError(err)
	}

	return connect.NewResponse(&GetCategoryResponse{
		Category: categoryToProto(out),
	}), nil
}

func (h *CategoryHandler) UpdateCategory(ctx context.Context, req *connect.Request[UpdateCategoryRequest]) (*connect.Response[UpdateCategoryResponse], error) {
	msg := req.Msg
	authorizedTenantID := utils.TenantIDFromContext(ctx, "")
	requestedTenantID := msg.TenantId

	if requestedTenantID == "" {
		requestedTenantID = authorizedTenantID
	}

	// Authorization check: only SystemAdmin, DefaultTenant, or the tenant themselves.
	if authorizedTenantID != "" &&
		authorizedTenantID != utils.SystemAdminTenant &&
		authorizedTenantID != utils.DefaultTenant &&
		authorizedTenantID != requestedTenantID {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("cannot update categories of other tenants"))
	}

	// Note:domain.CategoryService.Update currently expects name, which might be empty if not provided in proto.
	// We need to handle optionality.
	existing, err := h.svc.Get(ctx, requestedTenantID, msg.Slug)
	if err != nil {
		return nil, mapError(err)
	}

	name := existing.Name
	if msg.Name != nil {
		name = *msg.Name
	}

	description := existing.Description
	if msg.Description != nil {
		description = msg.Description
	}

	out, err := h.svc.Update(ctx, requestedTenantID, msg.Slug, name, description)
	if err != nil {
		return nil, mapError(err)
	}

	return connect.NewResponse(&UpdateCategoryResponse{
		Category: categoryToProto(out),
	}), nil
}

func (h *CategoryHandler) DeleteCategory(ctx context.Context, req *connect.Request[DeleteCategoryRequest]) (*connect.Response[DeleteCategoryResponse], error) {
	msg := req.Msg
	authorizedTenantID := utils.TenantIDFromContext(ctx, "")
	requestedTenantID := msg.TenantId

	if requestedTenantID == "" {
		requestedTenantID = authorizedTenantID
	}

	// Authorization check: only SystemAdmin, DefaultTenant, or the tenant themselves.
	if authorizedTenantID != "" &&
		authorizedTenantID != utils.SystemAdminTenant &&
		authorizedTenantID != utils.DefaultTenant &&
		authorizedTenantID != requestedTenantID {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("cannot delete categories of other tenants"))
	}

	err := h.svc.Delete(ctx, requestedTenantID, msg.Slug)
	if err != nil {
		return nil, mapError(err)
	}

	return connect.NewResponse(&DeleteCategoryResponse{}), nil
}

func (h *CategoryHandler) ListCategories(ctx context.Context, req *connect.Request[ListCategoriesRequest]) (*connect.Response[ListCategoriesResponse], error) {
	msg := req.Msg
	authorizedTenantID := utils.TenantIDFromContext(ctx, "")
	requestedTenantID := msg.TenantId

	if requestedTenantID == "" {
		requestedTenantID = authorizedTenantID
	}

	// Authorization check: only SystemAdmin, DefaultTenant, or the tenant themselves.
	if authorizedTenantID != "" &&
		authorizedTenantID != utils.SystemAdminTenant &&
		authorizedTenantID != utils.DefaultTenant &&
		authorizedTenantID != requestedTenantID {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("cannot list categories of other tenants"))
	}

	filter := domain.ListCategoriesFilter{
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

	categories, nextCursor, total, err := h.svc.List(ctx, requestedTenantID, filter)
	if err != nil {
		return nil, mapError(err)
	}

	items := make([]*Category, 0, len(categories))
	for i := range categories {
		items = append(items, categoryToProto(&categories[i]))
	}

	return connect.NewResponse(&ListCategoriesResponse{
		Categories:    items,
		NextPageToken: nextCursor,
		TotalCount:    total,
	}), nil
}

func (h *CategoryHandler) GetCategoryStats(ctx context.Context, req *connect.Request[GetCategoryStatsRequest]) (*connect.Response[GetCategoryStatsResponse], error) {
	msg := req.Msg
	authorizedTenantID := utils.TenantIDFromContext(ctx, "")
	requestedTenantID := msg.TenantId

	if requestedTenantID == "" {
		requestedTenantID = authorizedTenantID
	}

	// Authorization check: only SystemAdmin, DefaultTenant, or the tenant themselves.
	if authorizedTenantID != "" &&
		authorizedTenantID != utils.SystemAdminTenant &&
		authorizedTenantID != utils.DefaultTenant &&
		authorizedTenantID != requestedTenantID {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("cannot access statistics of other tenants"))
	}

	out, err := h.svc.GetStats(ctx, requestedTenantID, msg.Slug)
	if err != nil {
		return nil, mapError(err)
	}

	return connect.NewResponse(&GetCategoryStatsResponse{
		Stats: categoryStatsToProto(out),
	}), nil
}
