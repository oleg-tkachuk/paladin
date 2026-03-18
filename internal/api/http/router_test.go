package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	domainmocks "github.com/oleg-tkachuk/paladin/internal/domain/mocks"
	"github.com/oleg-tkachuk/paladin/internal/middleware"
	"github.com/oleg-tkachuk/paladin/internal/service"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestNewServer(t *testing.T) {
	cfg := &config.Config{
		Server: config.Server{
			HTTP: config.HTTPServer{
				CORSAllowedOrigins: []string{"http://localhost:3000"},
			},
		},
		RateLimit: config.RateLimit{
			RequestsPerSecond: 100,
			Burst:             100,
			MaxTenants:        1000,
			CleanupTTL:        time.Hour,
			CleanupInterval:   time.Minute,
		},
	}
	log := zap.NewNop()
	objSvc := &domainmocks.MockObjectsService{}
	catSvc := &domainmocks.MockCategoryService{}
	tenantSvc := &domainmocks.MockTenantService{}
	metadata := domain.AppMetadata{Version: "1.0.0"}
	hs := &service.HealthService{}
	var started atomic.Bool
	startTime := time.Now()
	auditRepo := &domainmocks.MockAuditLogRepository{}
	auditWriter := middleware.NewAuditBatchWriter(auditRepo, log)
	defer auditWriter.Close()

	server := NewServer(cfg, log, objSvc, catSvc, tenantSvc, metadata, hs, &started, startTime, auditWriter)
	assert.NotNil(t, server)

	// Test operational endpoint
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)

	// Test CORS
	req2, _ := http.NewRequestWithContext(context.Background(), http.MethodOptions, "/paladin.v1.ObjectService/GetObject", nil)
	req2.Header.Set("Origin", "http://localhost:3000")
	req2.Header.Set("Access-Control-Request-Method", "POST")
	rr2 := httptest.NewRecorder()
	server.Handler().ServeHTTP(rr2, req2)
	assert.Equal(t, http.StatusNoContent, rr2.Code)
	assert.Equal(t, "http://localhost:3000", rr2.Header().Get("Access-Control-Allow-Origin"))
}
