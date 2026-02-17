package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	httpapi "github.com/oleg-tkachuk/paladin/internal/api/http"
	"github.com/oleg-tkachuk/paladin/internal/breaker"
	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/generated/api"
	"github.com/oleg-tkachuk/paladin/internal/service"
	"github.com/stretchr/testify/mock"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/zap"
)

var _ = Describe("API Integration Tests", func() {
	var (
		cfg        *config.Config
		mockObjSvc *MockObjectsService
		hs         *service.HealthService
		server     *httpapi.Server
		ts         *httptest.Server
		client     *http.Client
	)

	BeforeEach(func() {
		cfg = &config.Config{
			Server: config.Server{
				Mode: "release",
				HTTP: config.HTTPServer{
					Addr: ":8080",
				},
			},
			Security: config.Security{
				TrustTenantIDFromRequest: true, // Allow X-Tenant-ID header
			},
			App: config.App{
				Name: "paladin",
			},
			RateLimit: config.RateLimit{
				RequestsPerSecond: 100,
				Burst:             200,
				MaxTenants:        1000,
				CleanupTTL:        10 * time.Minute,
				CleanupInterval:   5 * time.Minute,
			},
			Datastores: config.Datastores{
				Postgres: config.Postgres{
					DSN: "postgres://testuser:testpass@localhost:5432/testdb?sslmode=disable",
				},
			},
		}
		mockObjSvc = &MockObjectsService{}

		mockPinger := new(MockPinger)
		mockPinger.On("Ping", mock.Anything).Return(nil)

		mockS3Health := new(MockS3Health)
		mockS3Health.On("Health", mock.Anything).Return(nil)

		// Mock dependencies for HealthService (or pass nil if safe/ignored)
		hs = service.NewHealthService(mockPinger, mockS3Health, breaker.NewFactory(*cfg))

		started := atomic.Bool{}
		started.Store(true)

		server = httpapi.NewServer(cfg, zap.NewNop(), mockObjSvc, "v1.0.0", "HEAD", "now", hs, &started)
		ts = httptest.NewServer(server.Handler())
		client = ts.Client()
	})

	AfterEach(func() {
		ts.Close()
	})

	Context("Health Checks", func() {
		It("should return 200 OK for /health/livez", func() {
			resp, err := client.Get(ts.URL + "/health/livez")
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
		})

		It("should return 200 OK for /health/readyz", func() {
			resp, err := client.Get(ts.URL + "/health/readyz")
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
		})

		It("should return 200 OK for /health/startupz", func() {
			resp, err := client.Get(ts.URL + "/health/startupz")
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
		})
	})

	Context("Version", func() {
		It("should return version info", func() {
			resp, err := client.Get(ts.URL + "/v1/version")
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			var body map[string]interface{}
			json.NewDecoder(resp.Body).Decode(&body)
			Expect(body["version"]).To(Equal("v1.0.0"))
		})
	})

	Context("Admin Config", func() {
		It("should return redacted config without auth", func() {
			// /admin/config should be accessible without tenant header if excluded correctly
			resp, err := client.Get(ts.URL + "/v1/admin/config")
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			var cfgResp api.ConfigResponse
			err = json.NewDecoder(resp.Body).Decode(&cfgResp)
			Expect(err).NotTo(HaveOccurred())

			// Verify structure matches generated API model
			Expect(cfgResp.App).NotTo(BeNil())
			Expect(cfgResp.App.Name).NotTo(BeNil())
			Expect(*cfgResp.App.Name).To(Equal("paladin"))

			// Verify Postgres details are parsed from DSN
			Expect(cfgResp.Datastores).NotTo(BeNil())
			Expect(cfgResp.Datastores.Postgres).NotTo(BeNil())
			Expect(cfgResp.Datastores.Postgres.Host).NotTo(BeNil())
			Expect(*cfgResp.Datastores.Postgres.Host).To(Equal("localhost"))
			Expect(cfgResp.Datastores.Postgres.Port).NotTo(BeNil())
			Expect(*cfgResp.Datastores.Postgres.Port).To(Equal("5432"))
			Expect(cfgResp.Datastores.Postgres.User).NotTo(BeNil())
			Expect(*cfgResp.Datastores.Postgres.User).To(Equal("testuser"))
			Expect(cfgResp.Datastores.Postgres.Dbname).NotTo(BeNil())
			Expect(*cfgResp.Datastores.Postgres.Dbname).To(Equal("testdb"))
			Expect(cfgResp.Datastores.Postgres.SslMode).NotTo(BeNil())
			Expect(*cfgResp.Datastores.Postgres.SslMode).To(Equal("disable"))
		})
	})

	Context("Objects", func() {
		var (
			tenantID = "test-tenant"
			objectID = uuid.New()
		)

		It("should create an object", func() {
			// Setup mock expectation
			mockObjSvc.On("CreateSingle", mock.Anything, tenantID, "text/plain", int64(10), mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return(domain.CreateObjectResponse{
					ID:     objectID,
					Upload: domain.Presigned{URL: "http://s3/upload"},
				}, nil)

			reqBody := `{"content_type": "text/plain", "size_bytes": 10, "external_ref": "ref-123"}`
			req, _ := http.NewRequest("POST", ts.URL+"/v1/objects", strings.NewReader(reqBody))
			req.Header.Set("X-Tenant-ID", tenantID)
			req.Header.Set("Content-Type", "application/json")
			// Body is empty for this test as we mocking service level

			resp, err := client.Do(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusCreated))
		})

		It("should get an object meta", func() {
			mockObjSvc.On("GetMeta", mock.Anything, tenantID, objectID).
				Return(&domain.Object{ID: objectID, TenantID: tenantID, Status: "uploaded"}, nil)

			req, _ := http.NewRequest("GET", ts.URL+"/v1/objects/"+objectID.String()+"/meta", nil)
			req.Header.Set("X-Tenant-ID", tenantID)

			resp, err := client.Do(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
		})
	})
})
