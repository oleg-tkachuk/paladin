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

func TestObjectHandler_Isolation(t *testing.T) {
	log := zap.NewNop()
	svc := &domainmocks.MockObjectsService{}
	handler := &ObjectHandler{log: log, svc: svc}

	t.Run("Auth Disabled - Admin can upload to any tenant", func(t *testing.T) {
		ctx := utils.WithTenantID(context.Background(), utils.DefaultTenant)
		req := connect.NewRequest(&UploadObjectRequest{
			TenantId: "tenant-a",
			Bucket:   "bucket-1",
		})

		svc.On("CreateSingle", mock.Anything, "tenant-a", "bucket-1", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(domain.CreateObjectResponse{}, nil).Once()

		_, err := handler.UploadObject(ctx, req)
		assert.NoError(t, err)
		svc.AssertExpectations(t)
	})

	t.Run("Auth Enabled - Tenant cannot list objects of other tenant", func(t *testing.T) {
		ctx := utils.WithTenantID(context.Background(), "tenant-a")
		req := connect.NewRequest(&ListObjectsRequest{
			TenantId: "tenant-b",
		})

		_, err := handler.ListObjects(ctx, req)
		assert.Error(t, err)
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})

	t.Run("Auth Enabled - Tenant can list own objects", func(t *testing.T) {
		ctx := utils.WithTenantID(context.Background(), "tenant-a")
		req := connect.NewRequest(&ListObjectsRequest{
			TenantId: "tenant-a",
		})

		svc.On("List", mock.Anything, "tenant-a", mock.Anything).
			Return([]domain.Object{}, "", int64(0), nil).Once()

		_, err := handler.ListObjects(ctx, req)
		assert.NoError(t, err)
		svc.AssertExpectations(t)
	})
}
