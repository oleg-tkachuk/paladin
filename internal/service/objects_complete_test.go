package service_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	domainmocks "github.com/oleg-tkachuk/paladin/internal/domain/mocks"
	"github.com/oleg-tkachuk/paladin/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestObjectsService_CompleteObject_ETagNormalization(t *testing.T) {
	tenantID := "test-tenant"
	objID := uuid.New()
	key := "test-key"
	etagQuoted := "\"quoted-etag\""
	etagUnquoted := "quoted-etag"

	setupMock := func() (*domainmocks.MockObjectsRepository, *domainmocks.MockStorageClient, *domainmocks.MockPolicy, domain.ObjectsService) {
		objRepo := &domainmocks.MockObjectsRepository{}
		multiRepo := &domainmocks.MockMultipartRepository{}
		s3Client := &domainmocks.MockStorageClient{}
		policy := &domainmocks.MockPolicy{}
		uowf := &domainmocks.MockUoWFactory{}
		idemRepo := &domainmocks.MockIdempotencyRepository{}
		catRepo := &domainmocks.MockCategoryRepository{}
		breakerFactory := &MockBreakerFactory{}

		svc := service.NewObjectsService(
			objRepo, multiRepo, s3Client, policy, uowf, idemRepo, catRepo, breakerFactory,
			5*1024*1024, 0, 0, 0, 0, 0,
		)
		return objRepo, s3Client, policy, svc
	}

	t.Run("Match with unquoted etag", func(t *testing.T) {
		ctx := context.Background()
		objRepo, s3Client, policy, svc := setupMock()

		obj := &domain.Object{
			ID:        objID,
			TenantID:  tenantID,
			ObjectKey: key,
			Status:    domain.ObjectPending,
		}
		objCompleted := *obj
		objCompleted.Status = domain.ObjectComplete

		policy.On("Authorize", mock.Anything, tenantID, domain.ActionUpdate).Return(nil)
		objRepo.On("Get", mock.Anything, tenantID, objID).Return(obj, nil).Once()
		s3Client.On("HeadObject", mock.Anything, key).Return(&domain.HeadRecord{
			ETag:      etagQuoted,
			SizeBytes: 100,
		}, nil)
		objRepo.On("MarkComplete", mock.Anything, tenantID, objID, etagQuoted, int64(100)).Return(true, nil)
		objRepo.On("Get", mock.Anything, tenantID, objID).Return(&objCompleted, nil).Once()

		res, err := svc.CompleteObject(ctx, tenantID, objID, &etagUnquoted, nil)
		assert.NoError(t, err)
		assert.NotNil(t, res)
		assert.Equal(t, domain.ObjectComplete, res.Status)
	})

	t.Run("Match with quoted etag", func(t *testing.T) {
		ctx := context.Background()
		objRepo, s3Client, policy, svc := setupMock()

		obj := &domain.Object{
			ID:        objID,
			TenantID:  tenantID,
			ObjectKey: key,
			Status:    domain.ObjectPending,
		}
		objCompleted := *obj
		objCompleted.Status = domain.ObjectComplete

		policy.On("Authorize", mock.Anything, tenantID, domain.ActionUpdate).Return(nil)
		objRepo.On("Get", mock.Anything, tenantID, objID).Return(obj, nil).Once()
		s3Client.On("HeadObject", mock.Anything, key).Return(&domain.HeadRecord{
			ETag:      etagQuoted,
			SizeBytes: 100,
		}, nil)
		objRepo.On("MarkComplete", mock.Anything, tenantID, objID, etagQuoted, int64(100)).Return(true, nil)
		objRepo.On("Get", mock.Anything, tenantID, objID).Return(&objCompleted, nil).Once()

		res, err := svc.CompleteObject(ctx, tenantID, objID, &etagQuoted, nil)
		assert.NoError(t, err)
		assert.NotNil(t, res)
		assert.Equal(t, domain.ObjectComplete, res.Status)
	})
}
