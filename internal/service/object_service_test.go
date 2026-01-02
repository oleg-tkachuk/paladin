package service

import (
	"context"
	"testing"
	"time"

	"paladin/internal/config"
	"paladin/internal/storage/s3"
	"paladin/internal/store/postgres"

	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type MockObjectsRepo struct {
	mock.Mock
}

func (m *MockObjectsRepo) Create(ctx context.Context, rec postgres.ObjectRecord) error {
	args := m.Called(ctx, rec)
	return args.Error(0)
}

func (m *MockObjectsRepo) Get(ctx context.Context, tenantID string, id uuid.UUID) (*postgres.ObjectRecord, error) {
	args := m.Called(ctx, tenantID, id)
	return args.Get(0).(*postgres.ObjectRecord), args.Error(1)
}

func (m *MockObjectsRepo) MarkActive(ctx context.Context, tenantID string, id uuid.UUID) error {
	args := m.Called(ctx, tenantID, id)
	return args.Error(0)
}

type MockS3Client struct {
	mock.Mock
}

func (m *MockS3Client) BucketName() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockS3Client) PresignTTLDuration() time.Duration {
	args := m.Called()
	return args.Get(0).(time.Duration)
}

func (m *MockS3Client) PresignPutObject(ctx context.Context, key string, contentType string, sizeBytes int64) (s3.Presigned, error) {
	args := m.Called(ctx, key, contentType, sizeBytes)
	return args.Get(0).(s3.Presigned), args.Error(1)
}

func (m *MockS3Client) PresignGetObject(ctx context.Context, key string) (s3.Presigned, error) {
	args := m.Called(ctx, key)
	return args.Get(0).(s3.Presigned), args.Error(1)
}

func (m *MockS3Client) PresignUploadPart(ctx context.Context, key, uploadID string, partNumber int32) (s3.Presigned, error) {
	args := m.Called(ctx, key, uploadID, partNumber)
	return args.Get(0).(s3.Presigned), args.Error(1)
}

func (m *MockS3Client) CreateMultipartUpload(ctx context.Context, key string, contentType string) (s3.MultipartInit, error) {
	args := m.Called(ctx, key, contentType)
	return args.Get(0).(s3.MultipartInit), args.Error(1)
}

func (m *MockS3Client) CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []types.CompletedPart) error {
	args := m.Called(ctx, key, uploadID, parts)
	return args.Error(0)
}

func (m *MockS3Client) AbortMultipartUpload(ctx context.Context, key, uploadID string) error {
	args := m.Called(ctx, key, uploadID)
	return args.Error(0)
}

func TestObjectsService_CreateSingle(t *testing.T) {
	mockRepo := new(MockObjectsRepo)
	mockS3 := new(MockS3Client)
	policy := NewPolicy(config.Policy{
		MaxObjectSizeBytes:  1000,
		AllowedContentTypes: []string{"application/json"},
	})

	svc := NewObjectsService(policy, mockS3, mockRepo, nil, nil, 5*1024*1024)

	ctx := context.Background()
	tenantID := "test-tenant"
	contentType := "application/json"
	sizeBytes := int64(100)

	mockS3.On("BucketName").Return("test-bucket")
	mockRepo.On("Create", ctx, mock.Anything).Return(nil)
	mockS3.On("PresignPutObject", ctx, mock.Anything, contentType, sizeBytes).Return(s3.Presigned{URL: "http://example.com"}, nil)

	id, key, p, err := svc.CreateSingle(ctx, tenantID, contentType, sizeBytes, nil)

	assert.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, id)
	assert.Contains(t, key, tenantID)
	assert.Equal(t, "http://example.com", p.URL)

	mockRepo.AssertExpectations(t)
	mockS3.AssertExpectations(t)
}
