package service_test

import (
	"context"
	"paladin/internal/config"
	"paladin/internal/fault"
	"paladin/internal/service"
	"paladin/internal/storage/s3"
	"paladin/internal/store/postgres"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
)

// Mocks
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

type MockMultipartRepo struct {
	mock.Mock
}

func (m *MockMultipartRepo) Create(ctx context.Context, rec postgres.MultipartRecord) error {
	args := m.Called(ctx, rec)
	return args.Error(0)
}

func (m *MockMultipartRepo) GetByUploadID(ctx context.Context, tenantID string, uploadID string) (*postgres.MultipartRecord, error) {
	args := m.Called(ctx, tenantID, uploadID)
	return args.Get(0).(*postgres.MultipartRecord), args.Error(1)
}

func (m *MockMultipartRepo) UpsertPartETag(ctx context.Context, multipartID uuid.UUID, partNumber int, etag string, sizeBytes *int64) error {
	args := m.Called(ctx, multipartID, partNumber, etag, sizeBytes)
	return args.Error(0)
}

func (m *MockMultipartRepo) MarkCompleted(ctx context.Context, tenantID string, uploadID string) error {
	args := m.Called(ctx, tenantID, uploadID)
	return args.Error(0)
}

func (m *MockMultipartRepo) MarkAborted(ctx context.Context, tenantID string, uploadID string) error {
	args := m.Called(ctx, tenantID, uploadID)
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

type MockBreakerFactory struct {
	mock.Mock
}

func (m *MockBreakerFactory) Get(name string) *fault.CircuitBreakerWrapper {
	args := m.Called(name)
	return args.Get(0).(*fault.CircuitBreakerWrapper)
}

func (m *MockBreakerFactory) CheckHealth() map[string]string {
	args := m.Called()
	return args.Get(0).(map[string]string)
}

var _ = Describe("ObjectsService", func() {
	var (
		mockRepo    *MockObjectsRepo
		mockMPRepo  *MockMultipartRepo
		mockS3      *MockS3Client
		mockBreaker *MockBreakerFactory
		svc         service.ObjectsService
		ctx         context.Context
	)

	BeforeEach(func() {
		mockRepo = new(MockObjectsRepo)
		mockMPRepo = new(MockMultipartRepo)
		mockS3 = new(MockS3Client)
		mockBreaker = new(MockBreakerFactory)
		policy := service.NewPolicy(config.Policy{
			MaxObjectSizeBytes:  100 * 1024 * 1024,
			AllowedContentTypes: []string{"application/json"},
		})
		svc = service.NewObjectsService(policy, mockS3, mockRepo, mockMPRepo, mockBreaker, 5*1024*1024)
		ctx = context.Background()
	})

	Describe("CreateSingle", func() {
		It("should successfully create a single object upload", func() {
			tenantID := "test-tenant"
			contentType := "application/json"
			sizeBytes := int64(100)

			mockS3.On("BucketName").Return("test-bucket")
			mockRepo.On("Create", ctx, mock.Anything).Return(nil)
			mockS3.On("PresignPutObject", mock.Anything, mock.Anything, contentType, sizeBytes).Return(s3.Presigned{URL: "http://example.com"}, nil)

			id, key, p, err := svc.CreateSingle(ctx, tenantID, contentType, sizeBytes, nil)

			Expect(err).NotTo(HaveOccurred())
			Expect(id).NotTo(Equal(uuid.Nil))
			Expect(key).To(ContainSubstring(tenantID))
			Expect(p.URL).To(Equal("http://example.com"))

			mockRepo.AssertExpectations(GinkgoT())
			mockS3.AssertExpectations(GinkgoT())
		})

		It("should fail if policy validation fails", func() {
			tenantID := "test-tenant"
			contentType := "text/plain" // Not allowed
			sizeBytes := int64(100)

			_, _, _, err := svc.CreateSingle(ctx, tenantID, contentType, sizeBytes, nil)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("content_type not allowed"))
		})
	})

	Describe("Multipart Uploads", func() {
		It("should successfully initiate a multipart upload", func() {
			tenantID := "test-tenant"
			contentType := "application/json"
			sizeBytes := int64(10000000) // 10MB

			mockRepo.On("Create", ctx, mock.Anything).Return(nil)
			mockMPRepo.On("Create", ctx, mock.Anything).Return(nil)
			mockS3.On("BucketName").Return("test-bucket")
			mockS3.On("CreateMultipartUpload", ctx, mock.Anything, contentType).Return(s3.MultipartInit{
				UploadID: "test-upload-id",
				Key:      "test-key",
				Bucket:   "test-bucket",
			}, nil)
			mockS3.On("PresignTTLDuration").Return(1 * time.Hour)

			resp, err := svc.InitiateMultipart(ctx, tenantID, contentType, sizeBytes)

			Expect(err).NotTo(HaveOccurred())
			Expect(resp.UploadID).To(Equal("test-upload-id"))
			Expect(resp.ObjectID).NotTo(Equal(uuid.Nil))
		})

		It("should successfully sign a part", func() {
			tenantID := "test-tenant"
			uploadID := "test-upload-id"
			partNumber := int32(1)

			mockMPRepo.On("GetByUploadID", ctx, tenantID, uploadID).Return(&postgres.MultipartRecord{
				UploadID:  uploadID,
				ObjectKey: "test-key",
			}, nil)
			mockS3.On("PresignUploadPart", ctx, "test-key", uploadID, partNumber).Return(s3.Presigned{URL: "http://example.com/part1"}, nil)

			p, err := svc.SignPart(ctx, tenantID, uploadID, partNumber)

			Expect(err).NotTo(HaveOccurred())
			Expect(p.URL).To(Equal("http://example.com/part1"))
		})
		It("should successfully complete a multipart upload", func() {
			tenantID := "test-tenant"
			uploadID := "test-upload-id"
			parts := []service.CompletePart{{PartNumber: 1, ETag: "etag1"}}

			objID := uuid.New()
			mpu := &postgres.MultipartRecord{
				ID:        uuid.New(),
				UploadID:  uploadID,
				ObjectID:  objID,
				ObjectKey: "test-key",
			}

			mockMPRepo.On("GetByUploadID", ctx, tenantID, uploadID).Return(mpu, nil)
			mockMPRepo.On("UpsertPartETag", ctx, mpu.ID, 1, "etag1", mock.Anything).Return(nil)

			brk := fault.GetWithConfig(fault.BreakerConfig{Name: "test"})
			mockBreaker.On("Get", "s3.complete_multipart").Return(brk)

			mockS3.On("CompleteMultipartUpload", ctx, "test-key", uploadID, mock.Anything).Return(nil)
			mockMPRepo.On("MarkCompleted", ctx, tenantID, uploadID).Return(nil)
			mockRepo.On("MarkActive", ctx, tenantID, objID).Return(nil)

			resID, err := svc.CompleteMultipart(ctx, tenantID, uploadID, parts)

			Expect(err).NotTo(HaveOccurred())
			Expect(resID).To(Equal(objID))
		})
	})
})
