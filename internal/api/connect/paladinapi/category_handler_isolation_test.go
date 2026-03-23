package paladinapi

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	domainmocks "github.com/oleg-tkachuk/paladin/internal/domain/mocks"
	"github.com/oleg-tkachuk/paladin/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"
)

func TestCategoryHandler_Isolation(t *testing.T) {
	log := zap.NewNop()
	svc := &domainmocks.MockCategoryService{}
	handler := &CategoryHandler{log: log, svc: svc}

	t.Run("Auth Disabled - Admin can access any tenant", func(t *testing.T) {
		ctx := utils.WithTenantID(context.Background(), utils.DefaultTenant)
		req := connect.NewRequest(&ListCategoriesRequest{
			TenantId: "tenant-a",
		})

		svc.On("List", mock.Anything, "tenant-a", mock.Anything).
			Return([]domain.Category{}, "", int64(0), nil).Once()

		_, err := handler.ListCategories(ctx, req)
		assert.NoError(t, err)
		svc.AssertExpectations(t)
	})

	t.Run("Auth Enabled - Admin can access any tenant", func(t *testing.T) {
		ctx := utils.WithTenantID(context.Background(), utils.SystemAdminTenant)
		req := connect.NewRequest(&ListCategoriesRequest{
			TenantId: "tenant-a",
		})

		svc.On("List", mock.Anything, "tenant-a", mock.Anything).
			Return([]domain.Category{}, "", int64(0), nil).Once()

		_, err := handler.ListCategories(ctx, req)
		assert.NoError(t, err)
		svc.AssertExpectations(t)
	})

	t.Run("Auth Enabled - Tenant can access own data", func(t *testing.T) {
		ctx := utils.WithTenantID(context.Background(), "tenant-a")
		req := connect.NewRequest(&ListCategoriesRequest{
			TenantId: "tenant-a",
		})

		svc.On("List", mock.Anything, "tenant-a", mock.Anything).
			Return([]domain.Category{}, "", int64(0), nil).Once()

		_, err := handler.ListCategories(ctx, req)
		assert.NoError(t, err)
		svc.AssertExpectations(t)
	})

	t.Run("Auth Enabled - Tenant cannot access other tenant data", func(t *testing.T) {
		ctx := utils.WithTenantID(context.Background(), "tenant-a")
		req := connect.NewRequest(&ListCategoriesRequest{
			TenantId: "tenant-b",
		})

		_, err := handler.ListCategories(ctx, req)
		assert.Error(t, err)
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})

	t.Run("Auth Disabled - TenantId defaults to context if empty", func(t *testing.T) {
		ctx := utils.WithTenantID(context.Background(), utils.DefaultTenant)
		req := connect.NewRequest(&ListCategoriesRequest{
			TenantId: "",
		})

		svc.On("List", mock.Anything, utils.DefaultTenant, mock.Anything).
			Return([]domain.Category{}, "", int64(0), nil).Once()

		_, err := handler.ListCategories(ctx, req)
		assert.NoError(t, err)
		svc.AssertExpectations(t)
	})
}
