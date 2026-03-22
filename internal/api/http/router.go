package httpapi

import (
	"net/http"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	"github.com/oleg-tkachuk/paladin/internal/api/connect/paladinapi"
	"github.com/oleg-tkachuk/paladin/internal/api/connect/paladinapi/paladinapiconnect"
	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/middleware"
	"github.com/oleg-tkachuk/paladin/internal/service"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/cors"
	"go.uber.org/zap"
)

// Server exposes Connect RPC and operational endpoints (health, metrics, version)
// on a plain net/http mux — no Gin dependency.
type Server struct {
	mux http.Handler
}

// NewServer creates a ConnectRPC+ops HTTP mux.
func NewServer(
	cfg *config.Config,
	log *zap.Logger,
	objSvc domain.ObjectsService,
	catSvc domain.CategoryService,
	tenantSvc domain.TenantService,
	metadata domain.AppMetadata,
	hs *service.HealthService,
	started *atomic.Bool,
	startTime time.Time,
	auditRepo domain.AuditLogRepository,
) *Server {
	mux := http.NewServeMux()

	interceptors := connect.WithInterceptors(middleware.SetupConnectInterceptors(cfg, log.Named("middleware"), auditRepo)...)

	// ─── ObjectService ─────────────────────────────────────────────────────
	objectHandler := paladinapi.NewObjectHandler(log.Named("object_handler"), objSvc)
	path, handler := paladinapiconnect.NewObjectServiceHandler(objectHandler, interceptors)
	mux.Handle(path, handler)

	// ─── CategoryService ───────────────────────────────────────────────────
	categoryHandler := paladinapi.NewCategoryHandler(log.Named("category_handler"), catSvc)
	path, handler = paladinapiconnect.NewCategoryServiceHandler(categoryHandler, interceptors)
	mux.Handle(path, handler)

	// ─── TenantService ─────────────────────────────────────────────────────
	tenantHandler := paladinapi.NewTenantHandler(log.Named("tenant_handler"), tenantSvc)
	path, handler = paladinapiconnect.NewTenantServiceHandler(tenantHandler, interceptors)
	mux.Handle(path, handler)

	// ─── MultipartUploadService ────────────────────────────────────────────
	multipartHandler := paladinapi.NewMultipartHandler(log.Named("multipart_handler"), objSvc)
	path, handler = paladinapiconnect.NewMultipartUploadServiceHandler(multipartHandler, interceptors)
	mux.Handle(path, handler)

	// ─── PresignService ────────────────────────────────────────────────────
	presignHandler := paladinapi.NewPresignHandler(log.Named("presign_handler"), objSvc)
	path, handler = paladinapiconnect.NewPresignServiceHandler(presignHandler, interceptors)
	mux.Handle(path, handler)

	// ─── BulkService ───────────────────────────────────────────────────────
	bulkHandler := paladinapi.NewBulkHandler(log.Named("bulk_handler"), objSvc)
	path, handler = paladinapiconnect.NewBulkServiceHandler(bulkHandler, interceptors)
	mux.Handle(path, handler)

	// ─── BucketService ─────────────────────────────────────────────────────
	bucketHandler := paladinapi.NewBucketHandler(log.Named("bucket_handler"), objSvc)
	path, handler = paladinapiconnect.NewBucketServiceHandler(bucketHandler, interceptors)
	mux.Handle(path, handler)

	// ─── SystemService ─────────────────────────────────────────────────────
	systemHandler := paladinapi.NewSystemHandler(log.Named("system_handler"), hs, metadata, started, startTime, cfg, auditRepo)
	path, handler = paladinapiconnect.NewSystemServiceHandler(systemHandler, interceptors)
	mux.Handle(path, handler)

	// ─── Operational endpoints ─────────────────────────────────────────────
	mux.Handle(RouteMetrics, promhttp.Handler())

	// ─── CORS ──────────────────────────────────────────────────────────────
	origins := cfg.Server.HTTP.CORSAllowedOrigins
	if len(origins) == 0 {
		origins = []string{"*"}
	}

	c := cors.New(cors.Options{
		AllowedOrigins: origins,
		AllowedMethods: []string{
			http.MethodGet,
			http.MethodPost,
			http.MethodOptions,
		},
		AllowedHeaders: []string{
			"Content-Type",
			"Connect-Protocol-Version",
			"Connect-Timeout-Ms",
			"X-User-Agent",
			"X-Request-Id",
			"X-Tenant-Id",
			"Authorization",
		},
		ExposedHeaders: []string{
			"X-Request-Id",
		},
	})

	return &Server{mux: c.Handler(mux)}
}

// Handler returns the composed http.Handler.
func (s *Server) Handler() http.Handler { return s.mux }
