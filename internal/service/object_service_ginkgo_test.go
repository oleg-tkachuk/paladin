package service_test

import (
	"context"
	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/fault"
	"github.com/oleg-tkachuk/paladin/internal/service"
	"github.com/oleg-tkachuk/paladin/internal/storage/s3"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres"
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

func (m *MockObjectsRepo) GetByExternalRef(ctx context.Context, tenantID string, externalRef string) (*postgres.ObjectRecord, error) {
	args := m.Called(ctx, tenantID, externalRef)
	return args.Get(0).(*postgres.ObjectRecord), args.Error(1)
}

func (m *MockObjectsRepo) MarkActive(ctx context.Context, tenantID string, id uuid.UUID) (bool, error) {
	args := m.Called(ctx, tenantID, id)
	return args.Bool(0), args.Error(1)
}

func (m *MockObjectsRepo) MarkDeleted(ctx context.Context, tenantID string, id uuid.UUID) (bool, error) {
	args := m.Called(ctx, tenantID, id)
	return args.Bool(0), args.Error(1)
}

func (m *MockObjectsRepo) MarkComplete(ctx context.Context, tenantID string, id uuid.UUID, etag string, sizeBytes int64) (bool, error) {
	args := m.Called(ctx, tenantID, id, etag, sizeBytes)
	return args.Bool(0), args.Error(1)
}

func (m *MockObjectsRepo) List(ctx context.Context, tenantID string, filter postgres.ListObjectsFilter, limit int, cursor string) ([]postgres.ObjectRecord, string, error) {
	args := m.Called(ctx, tenantID, filter, limit, cursor)
	return args.Get(0).([]postgres.ObjectRecord), args.String(1), args.Error(2)
}

func (m *MockObjectsRepo) Patch(ctx context.Context, tenantID string, id uuid.UUID, labels map[string]string, externalRef *string) (*postgres.ObjectRecord, error) {
	args := m.Called(ctx, tenantID, id, labels, externalRef)
	return args.Get(0).(*postgres.ObjectRecord), args.Error(1)
}

func (m *MockObjectsRepo) ListExpiredPending(ctx context.Context, cutoff time.Time, limit int) ([]postgres.ObjectRecord, error) {
	args := m.Called(ctx, cutoff, limit)
	return args.Get(0).([]postgres.ObjectRecord), args.Error(1)
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

func (m *MockMultipartRepo) ListExpired(ctx context.Context, limit int) ([]postgres.MultipartRecord, error) {
	args := m.Called(ctx, limit)
	return args.Get(0).([]postgres.MultipartRecord), args.Error(1)
}

func (m *MockMultipartRepo) CompleteUpload(ctx context.Context, tenantID string, uploadID string, objectID uuid.UUID) error {
	args := m.Called(ctx, tenantID, uploadID, objectID)
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

func (m *MockS3Client) PresignPutObject(ctx context.Context, key string, contentType string, sizeBytes int64, ttl time.Duration) (s3.Presigned, error) {
	args := m.Called(ctx, key, contentType, sizeBytes, ttl)
	return args.Get(0).(s3.Presigned), args.Error(1)
}

func (m *MockS3Client) PresignGetObject(ctx context.Context, key string, ttl time.Duration) (s3.Presigned, error) {
	args := m.Called(ctx, key, ttl)
	return args.Get(0).(s3.Presigned), args.Error(1)
}

func (m *MockS3Client) PresignUploadPart(ctx context.Context, key, uploadID string, partNumber int32, ttl time.Duration) (s3.Presigned, error) {
	args := m.Called(ctx, key, uploadID, partNumber, ttl)
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

func (m *MockS3Client) HeadObject(ctx context.Context, key string) (*s3.HeadRecord, error) {
	args := m.Called(ctx, key)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*s3.HeadRecord), args.Error(1)
}

func (m *MockS3Client) DeleteObject(ctx context.Context, key string) error {
	args := m.Called(ctx, key)
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
		svc         service.ObjectsService
		ctx         context.Context
	)

	BeforeEach(func() {
		mockRepo = new(MockObjectsRepo)
		mockMPRepo = new(MockMultipartRepo)
		mockS3 = new(MockS3Client)
		policy := service.NewPolicy(config.Policy{
			MaxObjectSizeBytes:  100 * 1024 * 1024,
			AllowedContentTypes: []string{"application/json"},
		})
		svc = service.NewObjectsService(
			mockRepo,
			mockMPRepo,
			mockS3,
			policy,
			nil,            // idempotency repo
			5*1024*1024,    // part size
			5*time.Second,  // fast timeout
			30*time.Second, // default timeout
			60*time.Second, // s3 timeout
			2*time.Minute,  // long timeout
			24*time.Hour,   // idempotency TTL
		)
		ctx = context.Background()

		// Default expectations for common calls
		mockS3.On("PresignTTLDuration").Return(15 * time.Minute).Maybe()
		mockS3.On("BucketName").Return("test-bucket").Maybe()
	})

	Describe("CreateSingle", func() {
		It("should successfully create a single object upload", func() {
			tenantID := "test-tenant"
			contentType := "application/json"
			sizeBytes := int64(100)

			mockS3.On("BucketName").Return("test-bucket")
			mockS3.On("PresignTTLDuration").Return(15 * time.Minute)
			mockRepo.On("Create", mock.Anything, mock.Anything).Return(nil)
			mockS3.On("PresignPutObject", mock.Anything, mock.Anything, contentType, sizeBytes, mock.Anything).Return(s3.Presigned{URL: "http://example.com"}, nil)

			out, err := svc.CreateSingle(ctx, tenantID, contentType, sizeBytes, nil, nil, 0, nil)

			Expect(err).NotTo(HaveOccurred())
			Expect(out.ID).NotTo(Equal(uuid.Nil))
			Expect(out.Key).To(ContainSubstring(tenantID))
			Expect(out.Upload.URL).To(Equal("http://example.com"))

			mockRepo.AssertExpectations(GinkgoT())
			mockS3.AssertExpectations(GinkgoT())
		})

		It("should fail if policy validation fails", func() {
			tenantID := "test-tenant"
			contentType := "text/plain" // Not allowed
			sizeBytes := int64(100)

			_, err := svc.CreateSingle(ctx, tenantID, contentType, sizeBytes, nil, nil, 0, nil)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("content_type not allowed"))
		})
	})

	Describe("Multipart Uploads", func() {
		It("should successfully initiate a multipart upload", func() {
			tenantID := "test-tenant"
			contentType := "application/json"
			sizeBytes := int64(10000000) // 10MB

			mockRepo.On("Create", mock.Anything, mock.Anything).Return(nil)
			mockMPRepo.On("Create", mock.Anything, mock.Anything).Return(nil)
			mockS3.On("BucketName").Return("test-bucket")
			mockS3.On("CreateMultipartUpload", mock.Anything, mock.Anything, contentType).Return(s3.MultipartInit{
				UploadID: "test-upload-id",
				Key:      "test-key",
				Bucket:   "test-bucket",
			}, nil)
			mockS3.On("PresignTTLDuration").Return(1 * time.Hour)

			resp, err := svc.InitiateMultipart(ctx, tenantID, contentType, sizeBytes, nil, nil, 0, nil)

			Expect(err).NotTo(HaveOccurred())
			Expect(resp.UploadID).To(Equal("test-upload-id"))
			Expect(resp.ObjectID).NotTo(Equal(uuid.Nil))
		})

		It("should successfully sign a part", func() {
			tenantID := "test-tenant"
			uploadID := "test-upload-id"
			partNumber := int32(1)

			mockMPRepo.On("GetByUploadID", mock.Anything, tenantID, uploadID).Return(&postgres.MultipartRecord{
				UploadID:  uploadID,
				ObjectKey: "test-key",
			}, nil)
			mockS3.On("PresignTTLDuration").Return(15 * time.Minute)
			mockS3.On("PresignUploadPart", mock.Anything, "test-key", uploadID, partNumber, mock.Anything).Return(s3.Presigned{URL: "http://example.com/part1"}, nil)

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
				Status:    postgres.MultipartInitiated, // Needs to be Initiated
			}

			mockMPRepo.On("GetByUploadID", mock.Anything, tenantID, uploadID).Return(mpu, nil)
			mockS3.On("CompleteMultipartUpload", mock.Anything, "test-key", uploadID, mock.Anything).Return(nil)
			mockS3.On("HeadObject", mock.Anything, "test-key").Return(&s3.HeadRecord{
				ETag:      "etag1",
				SizeBytes: 100,
			}, nil)
			mockRepo.On("MarkComplete", mock.Anything, tenantID, objID, "etag1", int64(100)).Return(true, nil)
			mockRepo.On("Get", mock.Anything, tenantID, objID).Return(&postgres.ObjectRecord{ID: objID, Status: postgres.ObjectComplete}, nil)
			mockMPRepo.On("MarkCompleted", mock.Anything, tenantID, uploadID).Return(nil)

			rec, err := svc.CompleteMultipart(ctx, tenantID, uploadID, parts)

			Expect(err).NotTo(HaveOccurred())
			Expect(rec.ID).To(Equal(objID))
		})
	})
})
