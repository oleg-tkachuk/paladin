package service_test

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/fault"
	"github.com/oleg-tkachuk/paladin/internal/service"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
)

// Mocks
type MockObjectsRepo struct {
	mock.Mock
}

func (m *MockObjectsRepo) Create(ctx context.Context, rec domain.Object) error {
	args := m.Called(ctx, rec)

	return args.Error(0)
}

func (m *MockObjectsRepo) Get(ctx context.Context, tenantID string, id uuid.UUID) (*domain.Object, error) {
	args := m.Called(ctx, tenantID, id)

	return args.Get(0).(*domain.Object), args.Error(1)
}

func (m *MockObjectsRepo) GetByExternalRef(ctx context.Context, tenantID string, externalRef string) (*domain.Object, error) {
	args := m.Called(ctx, tenantID, externalRef)

	return args.Get(0).(*domain.Object), args.Error(1)
}

func (m *MockObjectsRepo) GetByKey(ctx context.Context, tenantID, bucket, key string) (*domain.Object, error) {
	args := m.Called(ctx, tenantID, bucket, key)

	return args.Get(0).(*domain.Object), args.Error(1)
}

func (m *MockObjectsRepo) MarkActive(ctx context.Context, tenantID string, id uuid.UUID) (bool, error) {
	args := m.Called(ctx, tenantID, id)

	return args.Bool(0), args.Error(1)
}

func (m *MockObjectsRepo) MarkDeleted(ctx context.Context, tenantID string, id uuid.UUID) (bool, error) {
	args := m.Called(ctx, tenantID, id)

	return args.Bool(0), args.Error(1)
}

func (m *MockObjectsRepo) MarkSoftDeleted(ctx context.Context, tenantID string, id uuid.UUID) (bool, error) {
	args := m.Called(ctx, tenantID, id)

	return args.Bool(0), args.Error(1)
}

func (m *MockObjectsRepo) BulkMarkSoftDeleted(ctx context.Context, tenantID string, ids []uuid.UUID) (int64, error) {
	args := m.Called(ctx, tenantID, ids)

	return args.Get(0).(int64), args.Error(1)
}

func (m *MockObjectsRepo) MarkHardDeleted(ctx context.Context, tenantID string, id uuid.UUID) (bool, error) {
	args := m.Called(ctx, tenantID, id)

	return args.Bool(0), args.Error(1)
}

func (m *MockObjectsRepo) MarkComplete(ctx context.Context, tenantID string, id uuid.UUID, etag string, sizeBytes int64) (bool, error) {
	args := m.Called(ctx, tenantID, id, etag, sizeBytes)

	return args.Bool(0), args.Error(1)
}

func (m *MockObjectsRepo) List(ctx context.Context, tenantID string, filter domain.ListObjectsFilter) ([]domain.Object, string, int64, error) {
	args := m.Called(ctx, tenantID, filter)

	return args.Get(0).([]domain.Object), args.String(1), args.Get(2).(int64), args.Error(3)
}

func (m *MockObjectsRepo) BulkCreate(ctx context.Context, items []domain.Object) error {
	args := m.Called(ctx, items)
	return args.Error(0)
}

func (m *MockObjectsRepo) BulkPatch(ctx context.Context, tenantID string, items []domain.BulkPatchItem) (int64, error) {
	args := m.Called(ctx, tenantID, items)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockObjectsRepo) Patch(ctx context.Context, tenantID string, id uuid.UUID, labels map[string]string, externalRef *string) (*domain.Object, error) {
	args := m.Called(ctx, tenantID, id, labels, externalRef)

	return args.Get(0).(*domain.Object), args.Error(1)
}

func (m *MockObjectsRepo) ListExpiredPending(ctx context.Context, cutoff time.Time, limit int) ([]domain.Object, error) {
	args := m.Called(ctx, cutoff, limit)

	return args.Get(0).([]domain.Object), args.Error(1)
}

func (m *MockObjectsRepo) Restore(ctx context.Context, tenantID string, id uuid.UUID) (bool, error) {
	args := m.Called(ctx, tenantID, id)

	return args.Bool(0), args.Error(1)
}

func (m *MockObjectsRepo) BulkRestore(ctx context.Context, tenantID string, ids []uuid.UUID) (int64, error) {
	args := m.Called(ctx, tenantID, ids)

	return args.Get(0).(int64), args.Error(1)
}

func (m *MockObjectsRepo) Delete(ctx context.Context, tenantID string, id uuid.UUID) (bool, error) {
	args := m.Called(ctx, tenantID, id)

	return args.Bool(0), args.Error(1)
}

func (m *MockObjectsRepo) BulkDelete(ctx context.Context, tenantID string, ids []uuid.UUID) (int64, error) {
	args := m.Called(ctx, tenantID, ids)

	return args.Get(0).(int64), args.Error(1)
}

func (m *MockObjectsRepo) UpdateStatus(ctx context.Context, tenantID string, id uuid.UUID, status string) (bool, error) {
	args := m.Called(ctx, tenantID, id, status)

	return args.Bool(0), args.Error(1)
}

func (m *MockObjectsRepo) GetStats(ctx context.Context, tenantID string) (*domain.ObjectStats, error) {
	args := m.Called(ctx, tenantID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}

	return args.Get(0).(*domain.ObjectStats), args.Error(1)
}

type MockCategoryRepo struct {
	mock.Mock
}

func (m *MockCategoryRepo) Create(ctx context.Context, cat domain.Category) error {
	args := m.Called(ctx, cat)

	return args.Error(0)
}

func (m *MockCategoryRepo) Update(ctx context.Context, cat domain.Category) error {
	args := m.Called(ctx, cat)

	return args.Error(0)
}

func (m *MockCategoryRepo) Get(ctx context.Context, tenantID, id string) (*domain.Category, error) {
	args := m.Called(ctx, tenantID, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}

	return args.Get(0).(*domain.Category), args.Error(1)
}

func (m *MockCategoryRepo) GetBySlug(ctx context.Context, tenantID, slug string) (*domain.Category, error) {
	args := m.Called(ctx, tenantID, slug)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}

	return args.Get(0).(*domain.Category), args.Error(1)
}

func (m *MockCategoryRepo) Exists(ctx context.Context, tenantID, slug string) (bool, error) {
	args := m.Called(ctx, tenantID, slug)

	return args.Bool(0), args.Error(1)
}

func (m *MockCategoryRepo) List(ctx context.Context, tenantID string, filter domain.ListCategoriesFilter) ([]domain.Category, string, int64, error) {
	args := m.Called(ctx, tenantID, filter)

	return args.Get(0).([]domain.Category), args.String(1), args.Get(2).(int64), args.Error(3)
}

func (m *MockCategoryRepo) Delete(ctx context.Context, tenantID, id string) (bool, error) {
	args := m.Called(ctx, tenantID, id)

	return args.Bool(0), args.Error(1)
}

func (m *MockCategoryRepo) HasObjects(ctx context.Context, tenantID, id string) (bool, error) {
	args := m.Called(ctx, tenantID, id)

	return args.Bool(0), args.Error(1)
}

func (m *MockCategoryRepo) ObjectCount(ctx context.Context, tenantID, id string) (int64, error) {
	args := m.Called(ctx, tenantID, id)

	return args.Get(0).(int64), args.Error(1)
}

func (m *MockCategoryRepo) ListTenants(ctx context.Context, limit int, cursor string) ([]string, string, int64, error) {
	args := m.Called(ctx, limit, cursor)
	if args.Get(0) == nil {
		return nil, "", 0, args.Error(3)
	}

	return args.Get(0).([]string), args.String(1), args.Get(2).(int64), args.Error(3)
}

func (m *MockCategoryRepo) GetStats(ctx context.Context, tenantID, slug string) (*domain.CategoryStats, error) {
	args := m.Called(ctx, tenantID, slug)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}

	return args.Get(0).(*domain.CategoryStats), args.Error(1)
}

type MockMultipartRepo struct {
	mock.Mock
}

func (m *MockMultipartRepo) Create(ctx context.Context, rec domain.Multipart) error {
	args := m.Called(ctx, rec)

	return args.Error(0)
}

func (m *MockMultipartRepo) GetByUploadID(ctx context.Context, tenantID string, uploadID string) (*domain.Multipart, error) {
	args := m.Called(ctx, tenantID, uploadID)

	return args.Get(0).(*domain.Multipart), args.Error(1)
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

func (m *MockMultipartRepo) ListExpired(ctx context.Context, limit int) ([]domain.Multipart, error) {
	args := m.Called(ctx, limit)

	return args.Get(0).([]domain.Multipart), args.Error(1)
}

func (m *MockMultipartRepo) ListParts(ctx context.Context, multipartID uuid.UUID) ([]domain.MultipartPart, error) {
	args := m.Called(ctx, multipartID)

	return args.Get(0).([]domain.MultipartPart), args.Error(1)
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

func (m *MockS3Client) PresignPutObject(ctx context.Context, key string, contentType string, sizeBytes int64, ttl time.Duration) (domain.Presigned, error) {
	args := m.Called(ctx, key, contentType, sizeBytes, ttl)

	return args.Get(0).(domain.Presigned), args.Error(1)
}

func (m *MockS3Client) PresignGetObject(ctx context.Context, key string, ttl time.Duration) (domain.Presigned, error) {
	args := m.Called(ctx, key, ttl)

	return args.Get(0).(domain.Presigned), args.Error(1)
}

func (m *MockS3Client) PresignUploadPart(ctx context.Context, key, uploadID string, partNumber int32, ttl time.Duration) (domain.Presigned, error) {
	args := m.Called(ctx, key, uploadID, partNumber, ttl)

	return args.Get(0).(domain.Presigned), args.Error(1)
}

func (m *MockS3Client) CreateMultipartUpload(ctx context.Context, key string, contentType string) (domain.MultipartInit, error) {
	args := m.Called(ctx, key, contentType)

	return args.Get(0).(domain.MultipartInit), args.Error(1)
}

func (m *MockS3Client) CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []domain.CompletePart) error {
	args := m.Called(ctx, key, uploadID, parts)

	return args.Error(0)
}

func (m *MockS3Client) AbortMultipartUpload(ctx context.Context, key, uploadID string) error {
	args := m.Called(ctx, key, uploadID)

	return args.Error(0)
}

func (m *MockS3Client) HeadObject(ctx context.Context, key string) (*domain.HeadRecord, error) {
	args := m.Called(ctx, key)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}

	return args.Get(0).(*domain.HeadRecord), args.Error(1)
}

func (m *MockS3Client) DeleteObject(ctx context.Context, key string) error {
	args := m.Called(ctx, key)

	return args.Error(0)
}

func (m *MockS3Client) CopyObject(ctx context.Context, srcKey, dstKey string) error {
	args := m.Called(ctx, srcKey, dstKey)

	return args.Error(0)
}

func (m *MockS3Client) Ping(ctx context.Context) (domain.S3PingResult, error) {
	args := m.Called(ctx)

	return args.Get(0).(domain.S3PingResult), args.Error(1)
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

type MockIdempotencyRepo struct {
	mock.Mock
}

func (m *MockIdempotencyRepo) Get(ctx context.Context, tenantID, key string) (*domain.IdempotencyRecord, error) {
	args := m.Called(ctx, tenantID, key)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}

	return args.Get(0).(*domain.IdempotencyRecord), args.Error(1)
}

func (m *MockIdempotencyRepo) Save(ctx context.Context, rec domain.IdempotencyRecord) error {
	args := m.Called(ctx, rec)

	return args.Error(0)
}

func (m *MockIdempotencyRepo) Delete(ctx context.Context, tenantID, key string) error {
	args := m.Called(ctx, tenantID, key)

	return args.Error(0)
}

type MockPolicy struct {
	mock.Mock
}

func (m *MockPolicy) Authorize(ctx context.Context, tenantID string, action domain.Action) error {
	args := m.Called(ctx, tenantID, action)

	return args.Error(0)
}

func (m *MockPolicy) Validate(contentType string, sizeBytes int64) error {
	args := m.Called(contentType, sizeBytes)

	return args.Error(0)
}

type MockUoWFactory struct {
	mock.Mock
}

func (m *MockUoWFactory) Begin(ctx context.Context) (domain.UnitOfWork, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}

	return args.Get(0).(domain.UnitOfWork), args.Error(1)
}

type MockUnitOfWork struct {
	mock.Mock
}

func (m *MockUnitOfWork) Objects() domain.ObjectsRepository {
	args := m.Called()

	return args.Get(0).(domain.ObjectsRepository)
}

func (m *MockUnitOfWork) Multipart() domain.MultipartRepository {
	args := m.Called()

	return args.Get(0).(domain.MultipartRepository)
}

func (m *MockUnitOfWork) Idempotency() domain.IdempotencyRepository {
	args := m.Called()

	return args.Get(0).(domain.IdempotencyRepository)
}

func (m *MockUnitOfWork) AuditLogs() domain.AuditLogRepository {
	args := m.Called()

	return args.Get(0).(domain.AuditLogRepository)
}

func (m *MockUnitOfWork) Categories() domain.CategoryRepository {
	args := m.Called()

	return args.Get(0).(domain.CategoryRepository)
}

func (m *MockUnitOfWork) Commit(ctx context.Context) error {
	args := m.Called(ctx)

	return args.Error(0)
}

func (m *MockUnitOfWork) Rollback(ctx context.Context) error {
	args := m.Called(ctx)

	return args.Error(0)
}

var _ = Describe("ObjectsService", func() {
	var (
		mockRepo    *MockObjectsRepo
		mockMPRepo  *MockMultipartRepo
		mockS3      *MockS3Client
		mockPolicy  *MockPolicy
		mockUoWf    *MockUoWFactory
		mockCatRepo *MockCategoryRepo
		mockBreaker *MockBreakerFactory
		svc         domain.ObjectsService
		ctx         context.Context
	)

	BeforeEach(func() {
		mockRepo = new(MockObjectsRepo)
		mockMPRepo = new(MockMultipartRepo)
		mockS3 = new(MockS3Client)
		mockPolicy = new(MockPolicy)
		mockUoWf = new(MockUoWFactory)
		mockCatRepo = new(MockCategoryRepo)
		mockBreaker = new(MockBreakerFactory)

		mockBreaker.On("Get", mock.Anything).Return(fault.GetWithConfig(fault.BreakerConfig{
			Name:                "test",
			Timeout:             10 * time.Second,
			MaxConsecutiveFails: 10,
			FailureRatio:        0.9,
			WindowDuration:      1 * time.Minute,
			Persistent:          false,
		})).Maybe()

		svc = service.NewObjectsService(
			mockRepo,
			mockMPRepo,
			mockS3,
			mockPolicy,
			mockUoWf,
			nil, // idempotency repo
			mockCatRepo,
			mockBreaker,
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
		mockPolicy.On("Authorize", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		mockPolicy.On("Validate", mock.Anything, mock.Anything).Return(nil).Maybe()
		mockCatRepo.On("Exists", mock.Anything, mock.Anything, mock.Anything).Return(true, nil).Maybe()
	})

	Describe("CreateSingle", func() {
		It("should successfully create a single object upload", func() {
			tenantID := "test-tenant"
			contentType := "application/json"
			sizeBytes := int64(100)

			mockS3.On("BucketName").Return("test-bucket")
			mockS3.On("PresignTTLDuration").Return(15 * time.Minute)
			mockRepo.On("Create", mock.Anything, mock.Anything).Return(nil)
			mockS3.On("PresignPutObject", mock.Anything, mock.Anything, contentType, sizeBytes, mock.Anything).Return(domain.Presigned{URL: "http://example.com"}, nil)

			out, err := svc.CreateSingle(ctx, tenantID, "objects", contentType, sizeBytes, nil, nil, 0, nil)

			Expect(err).NotTo(HaveOccurred())
			Expect(out.ID).NotTo(Equal(uuid.Nil))
			Expect(out.Key).To(ContainSubstring(tenantID))
			Expect(out.Upload.URL).To(Equal("http://example.com"))

			mockRepo.AssertExpectations(GinkgoT())
			mockS3.AssertExpectations(GinkgoT())
		})

		It("should fail if policy validation fails", func() {
			tenantID := "test-tenant"
			contentType := "text/plain"
			sizeBytes := int64(100)

			// Overwrite the default Validate expectation
			mockPolicy.ExpectedCalls = nil // Reset expectations for this specific test
			mockPolicy.On("Authorize", mock.Anything, mock.Anything, mock.Anything).Return(nil)
			mockPolicy.On("Validate", contentType, sizeBytes).Return(fmt.Errorf("content_type not allowed"))

			_, err := svc.CreateSingle(ctx, tenantID, "objects", contentType, sizeBytes, nil, nil, 0, nil)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("content_type not allowed"))
		})

		It("should return cached response for same idempotency key", func() {
			tenantID := "test-tenant"
			key := "idem-key"
			res := domain.CreateObjectResponse{ID: uuid.New(), Key: "cached"}
			body, _ := json.Marshal(res)

			mockIdem := new(MockIdempotencyRepo)
			mockIdem.On("Get", mock.Anything, tenantID, key).Return(&domain.IdempotencyRecord{ResponseBody: body}, nil)

			// Re-create service with mockIdem and mockPolicy
			svc = service.NewObjectsService(mockRepo, mockMPRepo, mockS3, mockPolicy, mockUoWf, mockIdem, mockCatRepo, mockBreaker, 1024*1024,
				time.Second, time.Second, time.Second, time.Second, time.Hour)

			out, err := svc.CreateSingle(ctx, tenantID, "objects", "image/png", 100, nil, nil, 0, &key)

			Expect(err).NotTo(HaveOccurred())
			Expect(out.ID).To(Equal(res.ID))
			Expect(out.Key).To(Equal("cached"))
		})

		It("should re-presign if object with external_ref exists and parameters match", func() {
			tenantID := "test-tenant"
			extRef := "ref123"
			existing := &domain.Object{ID: uuid.New(), ObjectKey: "key123", ContentType: "image/png", SizeBytes: 100}

			mockRepo.On("GetByExternalRef", mock.Anything, tenantID, extRef).Return(existing, nil)
			mockS3.On("PresignPutObject", mock.Anything, "key123", "image/png", int64(100), mock.Anything).Return(domain.Presigned{URL: "http://renewed"}, nil)

			out, err := svc.CreateSingle(ctx, tenantID, "objects", "image/png", 100, nil, &extRef, 0, nil)

			Expect(err).NotTo(HaveOccurred())
			Expect(out.ID).To(Equal(existing.ID))
			Expect(out.Upload.URL).To(Equal("http://renewed"))
		})

		It("should fail if external_ref exists with different parameters", func() {
			tenantID := "test-tenant"
			extRef := "ref123"
			existing := &domain.Object{ID: uuid.New(), ObjectKey: "key123", ContentType: "image/png", SizeBytes: 100}

			mockRepo.On("GetByExternalRef", mock.Anything, tenantID, extRef).Return(existing, nil)

			_, err := svc.CreateSingle(ctx, tenantID, "objects", "image/png", 200, nil, &extRef, 0, nil) // Different size

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("conflict"))
		})
	})

	Describe("Multipart Uploads", func() {
		It("should successfully initiate a multipart upload", func() {
			tenantID := "test-tenant"
			contentType := "application/json"
			sizeBytes := int64(10000000) // 10MB

			mockRepo.On("Create", mock.Anything, mock.Anything).Return(nil)
			mockMPRepo.On("Create", mock.Anything, mock.Anything).Return(nil)

			mockUoW := new(MockUnitOfWork)
			mockUoW.On("Objects").Return(mockRepo)
			mockUoW.On("Multipart").Return(mockMPRepo)
			mockUoW.On("Commit", mock.Anything).Return(nil)
			mockUoW.On("Rollback", mock.Anything).Return(nil)
			mockUoWf.On("Begin", mock.Anything).Return(mockUoW, nil)

			mockS3.On("BucketName").Return("test-bucket")
			mockS3.On("CreateMultipartUpload", mock.Anything, mock.Anything, contentType).Return(domain.MultipartInit{
				UploadID: "test-upload-id",
				Key:      "test-key",
				Bucket:   "test-bucket",
			}, nil)
			mockS3.On("PresignTTLDuration").Return(1 * time.Hour)

			resp, err := svc.InitiateMultipart(ctx, tenantID, "objects", contentType, sizeBytes, nil, nil, 0, nil)

			Expect(err).NotTo(HaveOccurred())
			Expect(resp.UploadID).To(Equal("test-upload-id"))
			Expect(resp.ObjectID).NotTo(Equal(uuid.Nil))
		})

		It("should successfully sign a part", func() {
			tenantID := "test-tenant"
			uploadID := "test-upload-id"
			partNumber := int32(1)

			mockMPRepo.On("GetByUploadID", mock.Anything, tenantID, uploadID).Return(&domain.Multipart{
				UploadID:  uploadID,
				ObjectKey: "test-key",
			}, nil)
			mockS3.On("PresignTTLDuration").Return(15 * time.Minute)
			mockS3.On("PresignUploadPart", mock.Anything, "test-key", uploadID, partNumber, mock.Anything).Return(domain.Presigned{URL: "http://example.com/part1"}, nil)

			p, err := svc.SignPart(ctx, tenantID, uploadID, partNumber)

			Expect(err).NotTo(HaveOccurred())
			Expect(p.URL).To(Equal("http://example.com/part1"))
		})
		It("should successfully complete a multipart upload", func() {
			tenantID := "test-tenant"
			uploadID := "test-upload-id"
			parts := []domain.CompletePart{{PartNumber: 1, ETag: "etag1"}}

			objID := uuid.New()
			mpu := &domain.Multipart{
				ID:        uuid.New(),
				UploadID:  uploadID,
				ObjectID:  objID,
				ObjectKey: "test-key",
				Status:    domain.MultipartInitiated, // Needs to be Initiated
			}

			mockMPRepo.On("GetByUploadID", mock.Anything, tenantID, uploadID).Return(mpu, nil)
			mockS3.On("CompleteMultipartUpload", mock.Anything, "test-key", uploadID, mock.Anything).Return(nil)
			mockS3.On("HeadObject", mock.Anything, "test-key").Return(&domain.HeadRecord{
				ETag:      "etag1",
				SizeBytes: 100,
			}, nil)
			mockRepo.On("MarkComplete", mock.Anything, tenantID, objID, "etag1", int64(100)).Return(true, nil)
			mockMPRepo.On("MarkCompleted", mock.Anything, tenantID, uploadID).Return(nil)

			mockUoW := new(MockUnitOfWork)
			mockUoW.On("Objects").Return(mockRepo)
			mockUoW.On("Multipart").Return(mockMPRepo)
			mockUoW.On("Commit", mock.Anything).Return(nil)
			mockUoW.On("Rollback", mock.Anything).Return(nil)
			mockUoWf.On("Begin", mock.Anything).Return(mockUoW, nil)

			mockRepo.On("Get", mock.Anything, tenantID, objID).Return(&domain.Object{ID: objID, Status: domain.ObjectComplete}, nil)

			rec, err := svc.CompleteMultipart(ctx, tenantID, uploadID, parts)

			Expect(err).NotTo(HaveOccurred())
			Expect(rec.ID).To(Equal(objID))
		})
	})

	Describe("SignUpload", func() {
		It("should successfully generate a presigned upload URL", func() {
			tenantID := "test-tenant"
			objID := uuid.New()

			mockRepo.On("Get", mock.Anything, tenantID, objID).Return(&domain.Object{
				ID:          objID,
				ObjectKey:   "test-key",
				ContentType: "application/json",
				SizeBytes:   100,
			}, nil)
			mockS3.On("PresignPutObject", mock.Anything, "test-key", "application/json", int64(100), mock.Anything).Return(domain.Presigned{URL: "http://example.com/upload"}, nil)

			p, err := svc.SignUpload(ctx, tenantID, objID, 0)

			Expect(err).NotTo(HaveOccurred())
			Expect(p.URL).To(Equal("http://example.com/upload"))
		})
	})

	Describe("SignDownload", func() {
		It("should successfully generate a presigned download URL for complete objects", func() {
			tenantID := "test-tenant"
			objID := uuid.New()

			mockRepo.On("Get", mock.Anything, tenantID, objID).Return(&domain.Object{
				ID:        objID,
				ObjectKey: "test-key",
				Status:    domain.ObjectComplete,
			}, nil)
			mockS3.On("PresignGetObject", mock.Anything, "test-key", mock.Anything).Return(domain.Presigned{URL: "http://example.com/download"}, nil)

			p, err := svc.SignDownload(ctx, tenantID, objID, 0)

			Expect(err).NotTo(HaveOccurred())
			Expect(p.URL).To(Equal("http://example.com/download"))
		})

		It("should fail for non-complete objects", func() {
			tenantID := "test-tenant"
			objID := uuid.New()

			mockRepo.On("Get", mock.Anything, tenantID, objID).Return(&domain.Object{
				ID:     objID,
				Status: domain.ObjectPending,
			}, nil)

			_, err := svc.SignDownload(ctx, tenantID, objID, 0)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not complete"))
		})
	})

	Describe("Delete", func() {
		It("should successfully mark object as deleted (soft delete)", func() {
			tenantID := "test-tenant"
			objID := uuid.New()

			mockRepo.On("Get", mock.Anything, tenantID, objID).Return(&domain.Object{ID: objID, Status: domain.ObjectComplete}, nil)
			mockRepo.On("MarkSoftDeleted", mock.Anything, tenantID, objID).Return(true, nil)

			err := svc.Delete(ctx, tenantID, objID)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should return an error if the object is hard deleted", func() {
			tenantID := "test-tenant"
			objID := uuid.New()

			mockRepo.On("Get", mock.Anything, tenantID, objID).Return(&domain.Object{ID: objID, Status: domain.ObjectHardDeleted}, nil)
			// Mocking MarkSoftDeleted is no longer needed since Get will result in transition error

			err := svc.Delete(ctx, tenantID, objID)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("Restore", func() {
		It("should restore soft deleted object", func() {
			tenantID := "test-tenant"
			objID := uuid.New()

			mockRepo.On("Get", mock.Anything, tenantID, objID).Return(&domain.Object{ID: objID, Status: domain.ObjectSoftDeleted}, nil)
			mockRepo.On("Restore", mock.Anything, tenantID, objID).Return(true, nil)

			err := svc.Restore(ctx, tenantID, objID)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("Purge", func() {
		It("should successfully delete object record and content from S3", func() {
			tenantID := "test-tenant"
			objID := uuid.New()

			// Expect Delete (and Get before it)
			mockRepo.On("Get", mock.Anything, tenantID, objID).Return(&domain.Object{ObjectKey: "some-key", Status: domain.ObjectSoftDeleted}, nil)
			mockS3.On("DeleteObject", mock.Anything, "some-key").Return(nil)
			mockRepo.On("Delete", mock.Anything, tenantID, objID).Return(true, nil)

			// We are testing service method Purge
			err := svc.Purge(ctx, tenantID, objID, nil)
			Expect(err).NotTo(HaveOccurred())
		})
	})

})
