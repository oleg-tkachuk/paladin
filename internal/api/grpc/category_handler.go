package grpcapi

import (
	"context"

	"connectrpc.com/connect"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/utils"
)

// CategoryHandler implements grpcapiconnect.CategoryServiceHandler.
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
	tenantID := utils.TenantIDFromContext(ctx, msg.TenantId)

	out, err := h.svc.Create(ctx, tenantID, msg.Slug, msg.Name, msg.Description)
	if err != nil {
		logger.FromContext(ctx).Warn("failed to create category", zap.Error(err), zap.String("slug", msg.Slug))
		return nil, grpcError(err)
	}

	return connect.NewResponse(&CreateCategoryResponse{
		Category: categoryToProto(out),
	}), nil
}

func (h *CategoryHandler) GetCategory(ctx context.Context, req *connect.Request[GetCategoryRequest]) (*connect.Response[GetCategoryResponse], error) {
	msg := req.Msg
	tenantID := utils.TenantIDFromContext(ctx, msg.TenantId)

	out, err := h.svc.Get(ctx, tenantID, msg.Slug)
	if err != nil {
		logger.FromContext(ctx).Warn("failed to get category", zap.Error(err), zap.String("slug", msg.Slug))
		return nil, grpcError(err)
	}

	return connect.NewResponse(&GetCategoryResponse{
		Category: categoryToProto(out),
	}), nil
}

func (h *CategoryHandler) UpdateCategory(ctx context.Context, req *connect.Request[UpdateCategoryRequest]) (*connect.Response[UpdateCategoryResponse], error) {
	msg := req.Msg
	tenantID := utils.TenantIDFromContext(ctx, msg.TenantId)

	// Note:domain.CategoryService.Update currently expects name, which might be empty if not provided in proto.
	// We need to handle optionality.
	existing, err := h.svc.Get(ctx, tenantID, msg.Slug)
	if err != nil {
		return nil, grpcError(err)
	}

	name := existing.Name
	if msg.Name != nil {
		name = *msg.Name
	}

	description := existing.Description
	if msg.Description != nil {
		description = msg.Description
	}

	out, err := h.svc.Update(ctx, tenantID, msg.Slug, name, description)
	if err != nil {
		logger.FromContext(ctx).Warn("failed to update category", zap.Error(err), zap.String("slug", msg.Slug))
		return nil, grpcError(err)
	}

	return connect.NewResponse(&UpdateCategoryResponse{
		Category: categoryToProto(out),
	}), nil
}

func (h *CategoryHandler) DeleteCategory(ctx context.Context, req *connect.Request[DeleteCategoryRequest]) (*connect.Response[DeleteCategoryResponse], error) {
	msg := req.Msg
	tenantID := utils.TenantIDFromContext(ctx, msg.TenantId)

	err := h.svc.Delete(ctx, tenantID, msg.Slug)
	if err != nil {
		logger.FromContext(ctx).Warn("failed to delete category", zap.Error(err), zap.String("slug", msg.Slug))
		return nil, grpcError(err)
	}

	return connect.NewResponse(&DeleteCategoryResponse{}), nil
}

func (h *CategoryHandler) ListCategories(ctx context.Context, req *connect.Request[ListCategoriesRequest]) (*connect.Response[ListCategoriesResponse], error) {
	msg := req.Msg

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
	if msg.SortOrder == SortOrder_SORT_ORDER_DESC {
		filter.SortOrder = "desc"
	} else if msg.SortOrder == SortOrder_SORT_ORDER_ASC {
		filter.SortOrder = "asc"
	}

	categories, nextCursor, total, err := h.svc.List(ctx, utils.TenantIDFromContext(ctx, msg.TenantId), filter)
	if err != nil {
		logger.FromContext(ctx).Warn("failed to list categories", zap.Error(err))
		return nil, grpcError(err)
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
	tenantID := utils.TenantIDFromContext(ctx, msg.TenantId)

	out, err := h.svc.GetStats(ctx, tenantID, msg.Slug)
	if err != nil {
		logger.FromContext(ctx).Warn("failed to get category stats", zap.Error(err), zap.String("slug", msg.Slug))
		return nil, grpcError(err)
	}

	return connect.NewResponse(&GetCategoryStatsResponse{
		Stats: categoryStatsToProto(out),
	}), nil
}
