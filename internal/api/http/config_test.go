package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	domainmocks "github.com/oleg-tkachuk/paladin/internal/domain/mocks"
	"github.com/oleg-tkachuk/paladin/internal/generated/api"
	"github.com/oleg-tkachuk/paladin/internal/service"
	servicemocks "github.com/oleg-tkachuk/paladin/internal/service/mocks"
	"github.com/stretchr/testify/assert"
)

type mockSysSvc struct {
	cfg domain.SystemConfig
}

func (m *mockSysSvc) GetConfig(ctx context.Context) (domain.SystemConfig, error) {
	return m.cfg, nil
}

func TestGetAdminConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Setup config with some sensitive data
	cfg := &config.Config{
		App: config.App{
			Name: "test-app",
			Env:  "test",
		},
		Server: config.Server{
			Mode: "test",
			HTTP: config.HTTPServer{
				Addr: ":8080",
			},
		},
		Datastores: config.Datastores{
			Postgres: config.Postgres{
				DSN: "postgres://user:password@localhost:5432/db", // Sensitive
				Pool: config.PostgresPool{
					MaxConns: 10,
				},
				Timeouts: config.PostgresTimeouts{
					Connect:   5 * time.Second,
					Statement: 30 * time.Second,
				},
			},
			S3: config.S3{
				AccessKey:  "HIDDEN_ACCESS_KEY", // Sensitive
				SecretKey:  "HIDDEN_SECRET_KEY", // Sensitive
				Bucket:     "test-bucket",
				PresignTTL: 15 * time.Minute,
			},
		},
		Policy: config.Policy{
			PresignPutTTL: 15 * time.Minute,
			PresignGetTTL: 15 * time.Minute,
		},
		Housekeeping: config.Housekeeping{
			PendingTTL:   24 * time.Hour,
			MultipartTTL: 24 * time.Hour,
			GCInterval:   1 * time.Hour,
		},
		Timeouts: config.Timeouts{
			FastOperation:    100 * time.Millisecond,
			DefaultOperation: 5 * time.Second,
			S3Operation:      10 * time.Second,
			LongOperation:    1 * time.Minute,
		},
		Idempotency: config.Idempotency{
			TTL: 24 * time.Hour,
		},
		Cache: config.Cache{
			TTL: 5 * time.Minute,
		},
	}

	// Mocks
	mockSvc := domainmocks.NewMockObjectsService(t)
	mockCat := domainmocks.NewMockCategoryService(t)
	mockAuditRepo := domainmocks.NewMockAuditLogRepository(t)
	mockS3Checker := servicemocks.NewMockS3HealthChecker(t)
	mockPinger := servicemocks.NewMockPinger(t)
	mockSysSvc := &mockSysSvc{cfg: cfg.Sanitize()}

	// Setup health service with mocks
	hs := service.NewHealthService(
		mockPinger,
		mockS3Checker,
		nil, // Breaker factory
	)

	started := atomic.Bool{}
	started.Store(true)

	// Initialize adapter
	meta := domain.AppMetadata{Version: "v1.0.0", Commit: "sha", BuildTime: "now"}
	adapter := NewOpenAPIAdapter(cfg, mockSvc, mockCat, mockAuditRepo, hs, mockSysSvc, &started, meta)

	// Setup router
	r := gin.New()
	api.RegisterHandlers(r, adapter)

	// Create request
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/admin/config", nil)
	r.ServeHTTP(w, req)

	// Assertions
	assert.Equal(t, http.StatusOK, w.Code)

	var resp api.ConfigResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)

	// Verify non-sensitive fields
	assert.Equal(t, "test-app", *resp.App.Name)
	assert.Equal(t, "test-bucket", *resp.Datastores.S3.Bucket)

	// Verify sensitive fields are NOT present (struct should not even have them)
	// We verify implicitly because they are not in the generated struct.
	// But let's verify via JSON map to be sure keys are largely correct.
	var rawMap map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &rawMap)

	datastores := rawMap["datastores"].(map[string]interface{})
	postgres := datastores["postgres"].(map[string]interface{})

	// Ensure sensitive fields are not mapped
	_, hasDSN := postgres["dsn"]
	assert.False(t, hasDSN, "DSN should not be exposed")

	s3 := datastores["s3"].(map[string]interface{})
	_, hasAccessKey := s3["access_key"]
	assert.False(t, hasAccessKey, "AccessKey should not be exposed")
	_, hasSecretKey := s3["secret_key"]
	assert.False(t, hasSecretKey, "SecretKey should not be exposed")
}
