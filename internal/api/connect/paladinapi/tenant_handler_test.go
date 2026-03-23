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
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestTenantHandler_CreateTenant(t *testing.T) {
	log := zap.NewNop()
	svc := &domainmocks.MockTenantService{}
	handler := NewTenantHandler(log, svc)

	t.Run("AuthDisabled_DifferentTenantsAllowed", func(t *testing.T) {
		// Simulate auth disabled: DefaultTenant in context
		ctx := context.WithValue(context.Background(), utils.TenantIDKey, utils.DefaultTenant)

		// Create Tenant A
		reqA := connect.NewRequest(&CreateTenantRequest{
			TenantId:    "tenant-a",
			DisplayName: stringPtr("Tenant A"),
		})
		svc.On("Create", mock.Anything, "tenant-a", stringPtr("Tenant A"), mock.Anything, mock.Anything).
			Return(&domain.Tenant{TenantID: "tenant-a"}, nil).Once()

		resA, err := handler.CreateTenant(ctx, reqA)
		require.NoError(t, err)
		assert.Equal(t, "tenant-a", resA.Msg.Tenant.TenantId)

		// Create Tenant B
		reqB := connect.NewRequest(&CreateTenantRequest{
			TenantId:    "tenant-b",
			DisplayName: stringPtr("Tenant B"),
		})
		svc.On("Create", mock.Anything, "tenant-b", stringPtr("Tenant B"), mock.Anything, mock.Anything).
			Return(&domain.Tenant{TenantID: "tenant-b"}, nil).Once()

		resB, err := handler.CreateTenant(ctx, reqB)
		require.NoError(t, err)
		assert.Equal(t, "tenant-b", resB.Msg.Tenant.TenantId)

		svc.AssertExpectations(t)
	})

	t.Run("AuthEnabled_AdminCanCreateAny", func(t *testing.T) {
		// Simulate Admin: SystemAdminTenant in context
		ctx := context.WithValue(context.Background(), utils.TenantIDKey, utils.SystemAdminTenant)

		req := connect.NewRequest(&CreateTenantRequest{
			TenantId: "new-tenant",
		})
		svc.On("Create", mock.Anything, "new-tenant", mock.Anything, mock.Anything, mock.Anything).
			Return(&domain.Tenant{TenantID: "new-tenant"}, nil).Once()

		res, err := handler.CreateTenant(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, "new-tenant", res.Msg.Tenant.TenantId)

		svc.AssertExpectations(t)
	})

	t.Run("AuthEnabled_TenantCannotCreateOther", func(t *testing.T) {
		// Simulate Tenant A: "tenant-a" in context
		ctx := context.WithValue(context.Background(), utils.TenantIDKey, "tenant-a")

		req := connect.NewRequest(&CreateTenantRequest{
			TenantId: "tenant-b",
		})

		res, err := handler.CreateTenant(ctx, req)
		require.Error(t, err)
		assert.Nil(t, res)
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})
}

func TestTenantHandler_GetTenant(t *testing.T) {
	log := zap.NewNop()
	svc := &domainmocks.MockTenantService{}
	handler := NewTenantHandler(log, svc)

	t.Run("AdminCanGetAny", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), utils.TenantIDKey, utils.SystemAdminTenant)
		req := connect.NewRequest(&GetTenantRequest{TenantId: "some-tenant"})

		svc.On("Get", mock.Anything, "some-tenant").
			Return(&domain.Tenant{TenantID: "some-tenant"}, nil).Once()

		res, err := handler.GetTenant(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, "some-tenant", res.Msg.Tenant.TenantId)
	})

	t.Run("TenantCanGetSelf", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), utils.TenantIDKey, "my-tenant")
		req := connect.NewRequest(&GetTenantRequest{TenantId: "my-tenant"})

		svc.On("Get", mock.Anything, "my-tenant").
			Return(&domain.Tenant{TenantID: "my-tenant"}, nil).Once()

		res, err := handler.GetTenant(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, "my-tenant", res.Msg.Tenant.TenantId)
	})

	t.Run("TenantCannotGetOther", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), utils.TenantIDKey, "my-tenant")
		req := connect.NewRequest(&GetTenantRequest{TenantId: "other-tenant"})

		_, err := handler.GetTenant(ctx, req)
		require.Error(t, err)
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})
}

func stringPtr(s string) *string {
	return &s
}
