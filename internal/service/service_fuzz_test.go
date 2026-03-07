package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/fault"
	"github.com/oleg-tkachuk/paladin/internal/service"
	"github.com/stretchr/testify/mock"
)

// Minimal Mocks for Fuzzing
type FuzzMockRepo struct{ mock.Mock }

func (m *FuzzMockRepo) Create(ctx context.Context, rec domain.Object) error {
	return m.Called(ctx, rec).Error(0)
}
func (m *FuzzMockRepo) Get(ctx context.Context, tID string, id uuid.UUID) (*domain.Object, error) {
	args := m.Called(ctx, tID, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Object), args.Error(1)
}
func (m *FuzzMockRepo) List(ctx context.Context, tID string, f domain.ListObjectsFilter) ([]domain.Object, string, int64, error) {
	args := m.Called(ctx, tID, f)
	return args.Get(0).([]domain.Object), args.String(1), args.Get(2).(int64), args.Error(3)
}
func (m *FuzzMockRepo) GetByExternalRef(ctx context.Context, tID string, ref string) (*domain.Object, error) {
	args := m.Called(ctx, tID, ref)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Object), args.Error(1)
}
func (m *FuzzMockRepo) Patch(ctx context.Context, tID string, id uuid.UUID, l map[string]string, r *string) (*domain.Object, error) {
	args := m.Called(ctx, tID, id, l, r)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Object), args.Error(1)
}
func (m *FuzzMockRepo) BulkCreate(ctx context.Context, items []domain.Object) error { return nil }
func (m *FuzzMockRepo) BulkPatch(ctx context.Context, tID string, items []domain.BulkPatchItem) (int64, error) {
	return 0, nil
}
func (m *FuzzMockRepo) ListExpiredPending(ctx context.Context, cutoff time.Time, limit int) ([]domain.Object, error) {
	return nil, nil
}
func (m *FuzzMockRepo) MarkComplete(ctx context.Context, tID string, id uuid.UUID, etag string, size int64) (bool, error) {
	return true, nil
}
func (m *FuzzMockRepo) MarkSoftDeleted(ctx context.Context, tID string, id uuid.UUID) (bool, error) {
	return true, nil
}
func (m *FuzzMockRepo) MarkHardDeleted(ctx context.Context, tID string, id uuid.UUID) (bool, error) {
	return true, nil
}
func (m *FuzzMockRepo) Restore(ctx context.Context, tID string, id uuid.UUID) (bool, error) {
	return true, nil
}
func (m *FuzzMockRepo) MarkDeleted(ctx context.Context, tID string, id uuid.UUID) (bool, error) {
	return true, nil
}
func (m *FuzzMockRepo) Delete(ctx context.Context, tID string, id uuid.UUID) (bool, error) {
	return true, nil
}
func (m *FuzzMockRepo) BulkMarkSoftDeleted(ctx context.Context, tID string, ids []uuid.UUID) (int64, error) {
	return 0, nil
}
func (m *FuzzMockRepo) BulkDelete(ctx context.Context, tID string, ids []uuid.UUID) (int64, error) {
	return 0, nil
}
func (m *FuzzMockRepo) BulkRestore(ctx context.Context, tID string, ids []uuid.UUID) (int64, error) {
	return 0, nil
}
func (m *FuzzMockRepo) GetStats(ctx context.Context, tID string) (*domain.ObjectStats, error) {
	return nil, nil
}
func (m *FuzzMockRepo) UpdateStatus(ctx context.Context, tID string, id uuid.UUID, s string) (bool, error) {
	return true, nil
}

type FuzzMockMPRepo struct{ mock.Mock }

func (m *FuzzMockMPRepo) Create(ctx context.Context, rec domain.Multipart) error { return nil }
func (m *FuzzMockMPRepo) GetByUploadID(ctx context.Context, tID string, uID string) (*domain.Multipart, error) {
	return nil, nil
}
func (m *FuzzMockMPRepo) UpsertPartETag(ctx context.Context, mID uuid.UUID, pN int, e string, sB *int64) error {
	return nil
}
func (m *FuzzMockMPRepo) MarkCompleted(ctx context.Context, tID string, uID string) error { return nil }
func (m *FuzzMockMPRepo) MarkAborted(ctx context.Context, tID string, uID string) error   { return nil }
func (m *FuzzMockMPRepo) ListExpired(ctx context.Context, limit int) ([]domain.Multipart, error) {
	return nil, nil
}
func (m *FuzzMockMPRepo) ListParts(ctx context.Context, mID uuid.UUID) ([]domain.MultipartPart, error) {
	return nil, nil
}
func (m *FuzzMockMPRepo) CompleteUpload(ctx context.Context, tID string, uID string, oID uuid.UUID) error {
	return nil
}

type FuzzMockS3Client struct{ mock.Mock }

func (m *FuzzMockS3Client) BucketName() string                { return "fuzz-bucket" }
func (m *FuzzMockS3Client) PresignTTLDuration() time.Duration { return 15 * time.Minute }
func (m *FuzzMockS3Client) PresignPutObject(ctx context.Context, key string, contentType string, sizeBytes int64, ttl time.Duration) (domain.Presigned, error) {
	return domain.Presigned{}, nil
}
func (m *FuzzMockS3Client) PresignGetObject(ctx context.Context, key string, ttl time.Duration) (domain.Presigned, error) {
	return domain.Presigned{}, nil
}
func (m *FuzzMockS3Client) PresignUploadPart(ctx context.Context, key, uploadID string, partNumber int32, ttl time.Duration) (domain.Presigned, error) {
	return domain.Presigned{}, nil
}
func (m *FuzzMockS3Client) CreateMultipartUpload(ctx context.Context, key string, contentType string) (domain.MultipartInit, error) {
	return domain.MultipartInit{}, nil
}
func (m *FuzzMockS3Client) CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []domain.CompletePart) error {
	return nil
}
func (m *FuzzMockS3Client) AbortMultipartUpload(ctx context.Context, key, uploadID string) error {
	return nil
}
func (m *FuzzMockS3Client) HeadObject(ctx context.Context, key string) (*domain.HeadRecord, error) {
	return nil, nil
}
func (m *FuzzMockS3Client) DeleteObject(ctx context.Context, key string) error { return nil }
func (m *FuzzMockS3Client) Ping(ctx context.Context) (domain.S3PingResult, error) {
	return domain.S3PingResult{}, nil
}

type FuzzMockPolicy struct{ mock.Mock }

func (m *FuzzMockPolicy) Authorize(ctx context.Context, tenantID string, action domain.Action) error {
	return nil
}
func (m *FuzzMockPolicy) Validate(contentType string, sizeBytes int64) error { return nil }

type FuzzMockUoWFactory struct{ mock.Mock }

func (m *FuzzMockUoWFactory) Begin(ctx context.Context) (domain.UnitOfWork, error) { return nil, nil }

type FuzzMockCategoryRepo struct{ mock.Mock }

func (m *FuzzMockCategoryRepo) Create(ctx context.Context, cat domain.Category) error { return nil }
func (m *FuzzMockCategoryRepo) Get(ctx context.Context, tID, id string) (*domain.Category, error) {
	return nil, nil
}
func (m *FuzzMockCategoryRepo) GetBySlug(ctx context.Context, tID, slug string) (*domain.Category, error) {
	return nil, nil
}
func (m *FuzzMockCategoryRepo) Exists(ctx context.Context, tID, slug string) (bool, error) {
	return true, nil
}
func (m *FuzzMockCategoryRepo) List(ctx context.Context, tID string, f domain.ListCategoriesFilter) ([]domain.Category, string, int64, error) {
	return nil, "", 0, nil
}
func (m *FuzzMockCategoryRepo) Delete(ctx context.Context, tID, id string) (bool, error) {
	return true, nil
}
func (m *FuzzMockCategoryRepo) ObjectCount(ctx context.Context, tID, id string) (int64, error) {
	return 0, nil
}
func (m *FuzzMockCategoryRepo) ListTenants(ctx context.Context, limit int, cursor string) ([]string, string, int64, error) {
	return nil, "", 0, nil
}
func (m *FuzzMockCategoryRepo) GetStats(ctx context.Context, tID, slug string) (*domain.CategoryStats, error) {
	return nil, nil
}

type FuzzMockBreaker struct{ mock.Mock }

func (m *FuzzMockBreaker) Get(name string) *fault.CircuitBreakerWrapper {
	args := m.Called(name)
	return args.Get(0).(*fault.CircuitBreakerWrapper)
}
func (m *FuzzMockBreaker) CheckHealth() map[string]string { return nil }

// Fuzz Tests
func FuzzCreateObject(f *testing.F) {
	f.Add("test-tenant", "invoices", "image/jpeg", int64(1048576), "ref-001")
	f.Fuzz(func(t *testing.T, tenantID, category, contentType string, sizeBytes int64, externalRef string) {
		mockRepo := new(FuzzMockRepo)
		mockMPRepo := new(FuzzMockMPRepo)
		mockS3 := new(FuzzMockS3Client)
		mockPolicy := new(FuzzMockPolicy)
		mockCatRepo := new(FuzzMockCategoryRepo)
		mockBreaker := new(FuzzMockBreaker)

		mockBreaker.On("Get", mock.Anything).Return(fault.GetWithConfig(fault.BreakerConfig{Name: "fuzz"})).Maybe()

		svc := service.NewObjectsService(mockRepo, mockMPRepo, mockS3, mockPolicy, nil, nil, mockCatRepo, mockBreaker, 0, 0, 0, 0, 0, 0)

		mockRepo.On("GetByExternalRef", mock.Anything, mock.Anything, mock.Anything).Return((*domain.Object)(nil), nil).Maybe()
		mockRepo.On("Create", mock.Anything, mock.Anything).Return(nil).Maybe()

		var extRefPtr *string
		if externalRef != "" {
			extRefPtr = &externalRef
		}

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, _ = svc.CreateSingle(ctx, tenantID, category, contentType, sizeBytes, nil, extRefPtr, 0, nil)
	})
}

func FuzzListObjects(f *testing.F) {
	f.Add("tenant-1", "cat-1", "pending", 10, "")
	f.Fuzz(func(t *testing.T, tenantID, category, status string, limit int, cursor string) {
		mockRepo := new(FuzzMockRepo)
		mockBreaker := new(FuzzMockBreaker)
		mockBreaker.On("Get", mock.Anything).Return(fault.GetWithConfig(fault.BreakerConfig{Name: "fuzz"})).Maybe()

		svc := service.NewObjectsService(mockRepo, nil, nil, nil, nil, nil, nil, mockBreaker, 0, 0, 0, 0, 0, 0)

		filter := domain.ListObjectsFilter{
			Category: &category,
			Status:   (*domain.ObjectStatus)(&status),
			Limit:    limit,
			Cursor:   cursor,
		}

		mockRepo.On("List", mock.Anything, tenantID, mock.Anything).Return([]domain.Object{}, "", int64(0), nil).Maybe()

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, _, _, _ = svc.List(ctx, tenantID, filter)
	})
}
