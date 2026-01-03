package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"

	httpapi "paladin/internal/api/http"
	"paladin/internal/fault"
	"paladin/internal/service"
	"paladin/internal/storage/s3"
	"paladin/internal/store/postgres"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"
)

// MockObjectsService is a mock implementation of the ObjectsService interface
type MockObjectsService struct {
	mock.Mock
}

func (m *MockObjectsService) CreateSingle(ctx context.Context, tenantID string, contentType string, sizeBytes int64, checksum []byte) (uuid.UUID, string, s3.Presigned, error) {
	args := m.Called(ctx, tenantID, contentType, sizeBytes, checksum)
	return args.Get(0).(uuid.UUID), args.String(1), args.Get(2).(s3.Presigned), args.Error(3)
}

func (m *MockObjectsService) Get(ctx context.Context, tenantID string, id uuid.UUID) (*postgres.ObjectRecord, s3.Presigned, error) {
	args := m.Called(ctx, tenantID, id)
	return args.Get(0).(*postgres.ObjectRecord), args.Get(1).(s3.Presigned), args.Error(2)
}

func (m *MockObjectsService) MarkComplete(ctx context.Context, tenantID string, id uuid.UUID) error {
	args := m.Called(ctx, tenantID, id)
	return args.Error(0)
}

func (m *MockObjectsService) Delete(ctx context.Context, tenantID string, id uuid.UUID) error {
	args := m.Called(ctx, tenantID, id)
	return args.Error(0)
}

func (m *MockObjectsService) InitiateMultipart(ctx context.Context, tenantID string, contentType string, sizeBytes int64) (service.MultipartInitResponse, error) {
	args := m.Called(ctx, tenantID, contentType, sizeBytes)
	return args.Get(0).(service.MultipartInitResponse), args.Error(1)
}

func (m *MockObjectsService) SignPart(ctx context.Context, tenantID string, uploadID string, partNumber int32) (s3.Presigned, error) {
	args := m.Called(ctx, tenantID, uploadID, partNumber)
	return args.Get(0).(s3.Presigned), args.Error(1)
}

func (m *MockObjectsService) CompleteMultipart(ctx context.Context, tenantID string, uploadID string, parts []service.CompletePart) (uuid.UUID, error) {
	args := m.Called(ctx, tenantID, uploadID, parts)
	return args.Get(0).(uuid.UUID), args.Error(1)
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

		server = httpapi.NewServer("test", zap.NewNop(), mockSvc, "1.0.0", "deadbeef", "2023-01-01", false, hs, started)
		recorder = httptest.NewRecorder()
	})

	Describe("GET /version", func() {
		It("returns version information", func() {
			req, _ := http.NewRequest("GET", "/version", nil)
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
				server.Handler().ServeHTTP(recorder, req)

				Expect(recorder.Code).To(Equal(http.StatusServiceUnavailable))
				Expect(recorder.Body.String()).To(ContainSubstring("starting"))
			})
		})

		Context("when started", func() {
			It("returns 200 started", func() {
				req, _ := http.NewRequest("GET", "/health/startupz", nil)
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
				mockSvc.On("CreateSingle", mock.Anything, "default", "image/png", int64(1024), mock.Anything).
					Return(id, "default/"+id.String(), s3.Presigned{URL: "http://upload"}, nil)
			})

			It("returns 200 and upload URL", func() {
				body := `{"content_type": "image/png", "size_bytes": 1024}`
				req, _ := http.NewRequest("POST", "/v1/objects", strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				server.Handler().ServeHTTP(recorder, req)

				Expect(recorder.Code).To(Equal(http.StatusOK))
				Expect(recorder.Body.String()).To(ContainSubstring("http://upload"))
			})
		})
	})

	Describe("POST /v1/multipart", func() {
		It("initiates multipart upload", func() {
			objID := uuid.New()
			mockSvc.On("InitiateMultipart", mock.Anything, "default", "application/octet-stream", int64(100*1024*1024)).
				Return(service.MultipartInitResponse{
					ObjectID: objID, UploadID: "up123", PartSize: 5 * 1024 * 1024,
				}, nil)

			body := `{"content_type": "application/octet-stream", "size_bytes": 104857600}`
			req, _ := http.NewRequest("POST", "/v1/multipart", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Body.String()).To(ContainSubstring("up123"))
		})
	})

	Describe("POST /v1/multipart/:upload_id/parts/:part_number/sign", func() {
		It("signs a part", func() {
			mockSvc.On("SignPart", mock.Anything, "default", "up123", int32(1)).
				Return(s3.Presigned{URL: "http://sign"}, nil)

			req, _ := http.NewRequest("POST", "/v1/multipart/up123/parts/1/sign", nil)
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Body.String()).To(ContainSubstring("http://sign"))
		})
	})

	Describe("POST /v1/multipart/:upload_id/complete", func() {
		It("completes multipart upload", func() {
			objID := uuid.New()
			mockSvc.On("CompleteMultipart", mock.Anything, "default", "up123", mock.Anything).
				Return(objID, nil)

			body := `{"parts": [{"part_number": 1, "etag": "etag1"}]}`
			req, _ := http.NewRequest("POST", "/v1/multipart/up123/complete", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Body.String()).To(ContainSubstring("active"))
		})
	})

	Describe("POST /v1/multipart/:upload_id/abort", func() {
		It("aborts multipart upload", func() {
			mockSvc.On("AbortMultipart", mock.Anything, "default", "up123").
				Return(nil)

			req, _ := http.NewRequest("POST", "/v1/multipart/up123/abort", nil)
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Body.String()).To(ContainSubstring("aborted"))
		})
	})
})
