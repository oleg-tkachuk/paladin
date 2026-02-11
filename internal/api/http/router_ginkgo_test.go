package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	httpapi "github.com/oleg-tkachuk/paladin/internal/api/http"
	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/fault"
	"github.com/oleg-tkachuk/paladin/internal/service"
	"github.com/oleg-tkachuk/paladin/internal/storage/s3"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"
)

// MockObjectsService is a mock implementation of the ObjectsService interface
type MockObjectsService struct {
	mock.Mock
}

func (m *MockObjectsService) CreateSingle(ctx context.Context, tenantID string, contentType string, sizeBytes int64, labels map[string]string, externalRef *string, uploadTTL int, idempotencyKey *string) (service.CreateObjectResponse, error) {
	args := m.Called(ctx, tenantID, contentType, sizeBytes, labels, externalRef, uploadTTL, idempotencyKey)
	return args.Get(0).(service.CreateObjectResponse), args.Error(1)
}

func (m *MockObjectsService) Get(ctx context.Context, tenantID string, id openapi_types.UUID) (*postgres.ObjectRecord, error) {
	args := m.Called(ctx, tenantID, id)
	return args.Get(0).(*postgres.ObjectRecord), args.Error(1)
}

func (m *MockObjectsService) GetMeta(ctx context.Context, tenantID string, id openapi_types.UUID) (*postgres.ObjectRecord, error) {
	args := m.Called(ctx, tenantID, id)
	return args.Get(0).(*postgres.ObjectRecord), args.Error(1)
}

func (m *MockObjectsService) CompleteObject(ctx context.Context, tenantID string, id openapi_types.UUID, etag *string, sizeBytes *int64) (*postgres.ObjectRecord, error) {
	args := m.Called(ctx, tenantID, id, etag, sizeBytes)
	return args.Get(0).(*postgres.ObjectRecord), args.Error(1)
}

func (m *MockObjectsService) List(ctx context.Context, tenantID string, filter postgres.ListObjectsFilter, limit int, cursor string) ([]postgres.ObjectRecord, string, error) {
	args := m.Called(ctx, tenantID, filter, limit, cursor)
	return args.Get(0).([]postgres.ObjectRecord), args.String(1), args.Error(2)
}

func (m *MockObjectsService) PatchMeta(ctx context.Context, tenantID string, id openapi_types.UUID, labels map[string]string, externalRef *string) (*postgres.ObjectRecord, error) {
	args := m.Called(ctx, tenantID, id, labels, externalRef)
	return args.Get(0).(*postgres.ObjectRecord), args.Error(1)
}

func (m *MockObjectsService) HardDelete(ctx context.Context, tenantID string, id openapi_types.UUID, idempotencyKey *string) error {
	args := m.Called(ctx, tenantID, id, idempotencyKey)
	return args.Error(0)
}

func (m *MockObjectsService) UpdateStatus(ctx context.Context, tenantID string, id openapi_types.UUID, status string, idempotencyKey *string) error {
	args := m.Called(ctx, tenantID, id, status, idempotencyKey)
	return args.Error(0)
}

func (m *MockObjectsService) SignUpload(ctx context.Context, tenantID string, id openapi_types.UUID, uploadTTL int) (s3.Presigned, error) {
	args := m.Called(ctx, tenantID, id, uploadTTL)
	return args.Get(0).(s3.Presigned), args.Error(1)
}

func (m *MockObjectsService) SignDownload(ctx context.Context, tenantID string, id openapi_types.UUID, downloadTTL int) (s3.Presigned, error) {
	args := m.Called(ctx, tenantID, id, downloadTTL)
	return args.Get(0).(s3.Presigned), args.Error(1)
}

func (m *MockObjectsService) InitiateMultipart(ctx context.Context, tenantID string, contentType string, sizeBytes int64, labels map[string]string, externalRef *string, uploadTTL int, idempotencyKey *string) (service.MultipartInitResponse, error) {
	args := m.Called(ctx, tenantID, contentType, sizeBytes, labels, externalRef, uploadTTL, idempotencyKey)
	return args.Get(0).(service.MultipartInitResponse), args.Error(1)
}

func (m *MockObjectsService) GetMultipart(ctx context.Context, tenantID string, uploadID string) (*postgres.MultipartRecord, error) {
	args := m.Called(ctx, tenantID, uploadID)
	return args.Get(0).(*postgres.MultipartRecord), args.Error(1)
}

func (m *MockObjectsService) SignPart(ctx context.Context, tenantID string, uploadID string, partNumber int32) (s3.Presigned, error) {
	args := m.Called(ctx, tenantID, uploadID, partNumber)
	return args.Get(0).(s3.Presigned), args.Error(1)
}

func (m *MockObjectsService) SignPartsBatch(ctx context.Context, tenantID string, uploadID string, partNumbers []int32) ([]service.SignPartResponse, error) {
	args := m.Called(ctx, tenantID, uploadID, partNumbers)
	return args.Get(0).([]service.SignPartResponse), args.Error(1)
}

func (m *MockObjectsService) CompleteMultipart(ctx context.Context, tenantID string, uploadID string, parts []service.CompletePart) (*postgres.ObjectRecord, error) {
	args := m.Called(ctx, tenantID, uploadID, parts)
	return args.Get(0).(*postgres.ObjectRecord), args.Error(1)
}

func (m *MockObjectsService) AbortMultipart(ctx context.Context, tenantID string, uploadID string) error {
	args := m.Called(ctx, tenantID, uploadID)
	return args.Error(0)
}

// MockPinger
type MockPinger struct {
	mock.Mock
}

func (m *MockPinger) Ping(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

// MockS3Health
type MockS3Health struct {
	mock.Mock
}

func (m *MockS3Health) Health(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

// MockBreakerFactory
type MockBreakerFactory struct {
	mock.Mock
}

func (m *MockBreakerFactory) Get(name string) *fault.CircuitBreakerWrapper {
	return m.Called(name).Get(0).(*fault.CircuitBreakerWrapper)
}

func (m *MockBreakerFactory) CheckHealth() map[string]string {
	return m.Called().Get(0).(map[string]string)
}

var _ = Describe("Router", func() {
	var (
		mockSvc     *MockObjectsService
		mockPing    *MockPinger
		mockS3      *MockS3Health
		mockBreaker *MockBreakerFactory
		hs          *service.HealthService
		started     *atomic.Bool
		server      *httpapi.Server
		recorder    *httptest.ResponseRecorder
	)

	BeforeEach(func() {
		mockSvc = new(MockObjectsService)
		mockPing = new(MockPinger)
		mockS3 = new(MockS3Health)
		mockBreaker = new(MockBreakerFactory)
		hs = service.NewHealthService(mockPing, mockS3, mockBreaker)
		started = &atomic.Bool{}
		started.Store(true)

		cfg := &config.Config{
			Server: config.Server{
				Mode: "test",
				Name: "test-server",
			},
			RateLimit: config.RateLimit{
				RequestsPerSecond: 100,
				Burst:             200,
				MaxTenants:        1000,
				CleanupTTL:        10 * time.Minute,
				CleanupInterval:   5 * time.Minute,
			},
			Security: config.Security{
				TrustTenantIDFromRequest: true,
			},
			Auth: config.Auth{
				Mode:                "disabled",
				DevPrincipalEnabled: true,
			},
		}

		logger, _ := zap.NewDevelopment()
		server = httpapi.NewServer(cfg, logger, mockSvc, "1.0.0", "deadbeef", "2023-01-01", hs, started)
		recorder = httptest.NewRecorder()
	})

	Describe("GET /version", func() {
		It("returns version information", func() {
			req, _ := http.NewRequest("GET", "/version", nil)
			req.Header.Set("X-Tenant-ID", "default")

			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))

			var resp map[string]string
			err := json.Unmarshal(recorder.Body.Bytes(), &resp)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp["version"]).To(Equal("1.0.0"))
			Expect(resp["commit"]).To(Equal("deadbeef"))
		})
	})

	Describe("GET /health/livez", func() {
		It("returns 200 alive", func() {
			req, _ := http.NewRequest("GET", "/health/livez", nil)
			req.Header.Set("X-Tenant-ID", "default")
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Body.String()).To(ContainSubstring("alive"))
		})
	})

	Describe("GET /health/startupz", func() {
		Context("when not started", func() {
			BeforeEach(func() {
				started.Store(false)
			})

			It("returns 503 starting", func() {
				req, _ := http.NewRequest("GET", "/health/startupz", nil)
				req.Header.Set("X-Tenant-ID", "default")
				server.Handler().ServeHTTP(recorder, req)

				Expect(recorder.Code).To(Equal(http.StatusServiceUnavailable))
				Expect(recorder.Body.String()).To(ContainSubstring("starting"))
			})
		})

		Context("when started", func() {
			It("returns 200 started", func() {
				req, _ := http.NewRequest("GET", "/health/startupz", nil)
				req.Header.Set("X-Tenant-ID", "default")
				server.Handler().ServeHTTP(recorder, req)

				Expect(recorder.Code).To(Equal(http.StatusOK))
				Expect(recorder.Body.String()).To(ContainSubstring("started"))
			})
		})
	})

	Describe("GET /health/readyz", func() {
		Context("when dependencies are healthy", func() {
			BeforeEach(func() {
				mockPing.On("Ping", mock.Anything).Return(nil)
				mockS3.On("Health", mock.Anything).Return(nil)
				mockBreaker.On("CheckHealth").Return(map[string]string{"s3": "closed"})
			})

			It("returns 200 ready", func() {
				req, _ := http.NewRequest("GET", "/health/readyz", nil)
				req.Header.Set("X-Tenant-ID", "default")
				server.Handler().ServeHTTP(recorder, req)

				Expect(recorder.Code).To(Equal(http.StatusOK))
				Expect(recorder.Body.String()).To(ContainSubstring("ready"))
			})
		})

		Context("when a dependency fails", func() {
			BeforeEach(func() {
				mockPing.On("Ping", mock.Anything).Return(nil)
				mockS3.On("Health", mock.Anything).Return(http.ErrHandlerTimeout)
				mockBreaker.On("CheckHealth").Return(map[string]string{"s3": "closed"})
			})

			It("returns 503 not_ready", func() {
				req, _ := http.NewRequest("GET", "/health/readyz", nil)
				req.Header.Set("X-Tenant-ID", "default")
				server.Handler().ServeHTTP(recorder, req)

				Expect(recorder.Code).To(Equal(http.StatusServiceUnavailable))
				Expect(recorder.Body.String()).To(ContainSubstring("not_ready"))
			})
		})
	})

	Describe("POST /v1/objects", func() {
		Context("with valid request", func() {
			BeforeEach(func() {
				id := uuid.New()
				mockSvc.On("CreateSingle", mock.Anything, mock.Anything, "image/png", int64(1024), mock.Anything, mock.Anything, 0, mock.Anything).
					Return(service.CreateObjectResponse{ID: id, Key: "default/" + id.String(), Upload: s3.Presigned{URL: "http://upload"}}, nil)
			})

			It("returns 200 and upload URL", func() {
				body := `{"content_type": "image/png", "size_bytes": 1024}`
				req, _ := http.NewRequest("POST", "/v1/objects", strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Tenant-ID", "default")
				server.Handler().ServeHTTP(recorder, req)

				Expect(recorder.Code).To(Equal(http.StatusCreated))
				Expect(recorder.Body.String()).To(ContainSubstring("http://upload"))
			})
		})
	})

	Describe("POST /v1/multipart", func() {
		It("initiates multipart upload", func() {
			objID := uuid.New()
			mockSvc.On("InitiateMultipart", mock.Anything, mock.Anything, "application/octet-stream", int64(100*1024*1024), mock.Anything, mock.Anything, 0, mock.Anything).
				Return(service.MultipartInitResponse{
					ObjectID: objID, UploadID: "up123", PartSize: 5 * 1024 * 1024,
				}, nil)

			body := `{"content_type": "application/octet-stream", "size_bytes": 104857600}`
			req, _ := http.NewRequest("POST", "/v1/multipart", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Tenant-ID", "default")
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Body.String()).To(ContainSubstring("up123"))
		})
	})

	Describe("POST /v1/multipart/:upload_id/parts/:part_number/sign", func() {
		It("signs a part", func() {
			mockSvc.On("SignPart", mock.Anything, mock.Anything, "up123", int32(1)).
				Return(s3.Presigned{URL: "http://sign"}, nil)

			req, _ := http.NewRequest("POST", "/v1/multipart/up123/parts/1/sign", nil)
			req.Header.Set("X-Tenant-ID", "default")
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Body.String()).To(ContainSubstring("http://sign"))
		})
	})

	Describe("POST /v1/multipart/:upload_id/complete", func() {
		It("completes multipart upload", func() {
			objID := uuid.New()
			mockSvc.On("CompleteMultipart", mock.Anything, mock.Anything, "up123", mock.Anything).
				Return(&postgres.ObjectRecord{ID: objID, Status: postgres.ObjectComplete}, nil)

			body := `{"parts": [{"part_number": 1, "etag": "etag1"}]}`
			req, _ := http.NewRequest("POST", "/v1/multipart/up123/complete", strings.NewReader(body))
			req.Header.Set("X-Tenant-ID", "default")
			req.Header.Set("Content-Type", "application/json")
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Body.String()).To(ContainSubstring("complete"))
		})
	})

	Describe("POST /v1/multipart/:upload_id/abort", func() {
		It("aborts multipart upload", func() {
			mockSvc.On("AbortMultipart", mock.Anything, mock.Anything, "up123").
				Return(nil)

			req, _ := http.NewRequest("POST", "/v1/multipart/up123/abort", nil)
			req.Header.Set("X-Tenant-ID", "default")
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Body.String()).To(ContainSubstring("aborted"))
		})
	})

	Describe("GET /v1/objects/:id", func() {
		It("returns object details", func() {
			id := uuid.New()
			mockSvc.On("Get", mock.Anything, mock.Anything, openapi_types.UUID(id)).Return(&postgres.ObjectRecord{
				ID: openapi_types.UUID(id), Status: postgres.ObjectComplete,
			}, nil)

			req, _ := http.NewRequest("GET", "/v1/objects/"+id.String(), nil)
			req.Header.Set("X-Tenant-ID", "default")
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Body.String()).To(ContainSubstring(id.String()))
		})
	})

	Describe("DELETE /v1/objects/:id", func() {
		It("deletes the object", func() {
			id := uuid.New()
			mockSvc.On("HardDelete", mock.Anything, mock.Anything, openapi_types.UUID(id), (*string)(nil)).Return(nil)

			req, _ := http.NewRequest("DELETE", "/v1/objects/"+id.String(), nil)
			req.Header.Set("X-Tenant-ID", "default")
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusNoContent))
		})
	})

	Describe("POST /v1/objects/:id/sign-upload", func() {
		It("returns presigned upload URL", func() {
			id := uuid.New()
			mockSvc.On("SignUpload", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(s3.Presigned{URL: "http://upload"}, nil)

			req, _ := http.NewRequest("POST", "/v1/objects/"+id.String()+"/sign-upload", strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Tenant-ID", "default")
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Body.String()).To(ContainSubstring("http://upload"))
		})
	})

	Describe("PATCH /v1/objects/:id", func() {
		It("updates the object status", func() {
			id := uuid.New()
			mockSvc.On("UpdateStatus", mock.Anything, mock.Anything, openapi_types.UUID(id), "soft_deleted", (*string)(nil)).Return(nil)
			mockSvc.On("Get", mock.Anything, mock.Anything, openapi_types.UUID(id)).Return(&postgres.ObjectRecord{
				ID: openapi_types.UUID(id), Status: postgres.ObjectSoftDeleted,
			}, nil)

			body := `{"status": "soft_deleted"}`
			req, _ := http.NewRequest("PATCH", "/v1/objects/"+id.String(), strings.NewReader(body))
			req.Header.Set("X-Tenant-ID", "default")
			req.Header.Set("Content-Type", "application/json")
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Body.String()).To(ContainSubstring("soft_deleted"))
		})
	})

	Describe("POST /v1/objects/:id/sign-download", func() {
		It("returns presigned download URL", func() {
			id := uuid.New()
			mockSvc.On("SignDownload", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(s3.Presigned{URL: "http://download"}, nil)

			req, _ := http.NewRequest("POST", "/v1/objects/"+id.String()+"/sign-download", strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Tenant-ID", "default")
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Body.String()).To(ContainSubstring("http://download"))
		})
	})
	Describe("Error Handling", func() {
		It("returns unified error response for 404", func() {
			mockSvc.On("Get", mock.Anything, mock.Anything, mock.Anything).
				Return(&postgres.ObjectRecord{}, errors.New("not found"))

			req, _ := http.NewRequest("GET", "/v1/objects/"+uuid.NewString(), nil)
			req.Header.Set("X-Tenant-ID", "default")
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusNotFound))

			var resp map[string]interface{}
			err := json.Unmarshal(recorder.Body.Bytes(), &resp)
			Expect(err).NotTo(HaveOccurred())

			// Verify structure: {"error": {"code": "...", "message": "...", "request_id": "..."}}
			Expect(resp).To(HaveKey("error"))
			errObj, ok := resp["error"].(map[string]interface{})
			Expect(ok).To(BeTrue())
			Expect(errObj).To(HaveKey("code"))
			Expect(errObj).To(HaveKey("message"))
			Expect(errObj).To(HaveKey("request_id"))
			Expect(errObj).To(HaveKey("trace_id"))
		})
	})
})
