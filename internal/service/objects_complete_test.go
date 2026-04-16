package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	domainmocks "github.com/oleg-tkachuk/paladin/internal/domain/mocks"
	"github.com/oleg-tkachuk/paladin/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const testTenantStr = "test-tenant"

func TestObjectsService_CompleteObject_ETagNormalization(t *testing.T) {
	tenantID := testTenantStr
	objID := uuid.New()
	key := "test-key"
	etagQuoted := "\"quoted-etag\""
	etagUnquoted := "quoted-etag"

	setupMock := func() (*domainmocks.MockObjectsRepository, *domainmocks.MockStorageClient, *domainmocks.MockPolicy, *domainmocks.MockUploadIntentsRepository, domain.ObjectsService) {
		objRepo := &domainmocks.MockObjectsRepository{}
		multiRepo := &domainmocks.MockMultipartRepository{}
		intentRepo := &domainmocks.MockUploadIntentsRepository{}
		s3Client := &domainmocks.MockStorageClient{}
		policy := &domainmocks.MockPolicy{}
		uowf := &domainmocks.MockUoWFactory{}
		idemRepo := &domainmocks.MockIdempotencyRepository{}
		catRepo := &domainmocks.MockCategoryRepository{}
		breakerFactory := &MockBreakerFactory{}

		svc := service.NewObjectsService(service.ObjectsServiceConfig{
			ObjRepo: objRepo, MultiRepo: multiRepo, IntentRepo: intentRepo, S3: s3Client, Policy: policy,
			UoWF: uowf, IdemRepo: idemRepo, CatRepo: catRepo, Breaker: breakerFactory,
			PartSize: 1024 * 1024, FastTimeout: 2 * time.Second, DefaultTimeout: 2 * time.Second,
			S3Timeout: 2 * time.Second, LongTimeout: 2 * time.Second, Log: zap.NewNop(),
		})

		return objRepo, s3Client, policy, intentRepo, svc
	}

	tests := []struct {
		name     string
		sentEtag string
	}{
		{"Match with unquoted etag", etagUnquoted},
		{"Match with quoted etag", etagQuoted},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			objRepo, s3Client, policy, intentRepo, svc := setupMock()

			obj := &domain.Object{
				ID:        objID,
				TenantID:  tenantID,
				ObjectKey: key,
				Status:    domain.ObjectPending,
			}
			objCompleted := *obj
			objCompleted.Status = domain.ObjectComplete

			policy.On("Authorize", mock.Anything, tenantID, domain.ActionUpdate).Return(nil)
			// Intent lookup returns not-found, so legacy path is taken.
			intentRepo.On("Get", mock.Anything, tenantID, objID).Return(nil, domain.ErrNotFound)
			objRepo.On("Get", mock.Anything, tenantID, objID).Return(obj, nil).Once()
			s3Client.On("HeadObject", mock.Anything, key).Return(&domain.HeadRecord{
				ETag:      etagQuoted,
				SizeBytes: 100,
			}, nil)
			objRepo.On("MarkComplete", mock.Anything, tenantID, objID, etagQuoted, int64(100)).Return(true, nil)
			objRepo.On("Get", mock.Anything, tenantID, objID).Return(&objCompleted, nil).Once()

			res, err := svc.CompleteObject(ctx, tenantID, objID, &tt.sentEtag, nil)
			require.NoError(t, err)
			assert.NotNil(t, res)
			assert.Equal(t, domain.ObjectComplete, res.Status)
		})
	}
}
