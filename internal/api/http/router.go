package httpapi

import (
	"net/http"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	grpcapi "github.com/oleg-tkachuk/paladin/internal/api/grpc"
	"github.com/oleg-tkachuk/paladin/internal/api/grpc/grpcapiconnect"
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
) *Server {
	mux := http.NewServeMux()

	interceptors := connect.WithInterceptors(middleware.SetupConnectInterceptors(cfg, log.Named("middleware"))...)

	// ─── ObjectService ─────────────────────────────────────────────────────
	objectHandler := grpcapi.NewObjectHandler(log.Named("object_handler"), objSvc)
	path, handler := grpcapiconnect.NewObjectServiceHandler(objectHandler, interceptors)
	mux.Handle(path, handler)

	// ─── CategoryService ───────────────────────────────────────────────────
	categoryHandler := grpcapi.NewCategoryHandler(log.Named("category_handler"), catSvc)
	path, handler = grpcapiconnect.NewCategoryServiceHandler(categoryHandler, interceptors)
	mux.Handle(path, handler)

	// ─── TenantService ─────────────────────────────────────────────────────
	tenantHandler := grpcapi.NewTenantHandler(log.Named("tenant_handler"), tenantSvc)
	path, handler = grpcapiconnect.NewTenantServiceHandler(tenantHandler, interceptors)
	mux.Handle(path, handler)

	// ─── MultipartUploadService ────────────────────────────────────────────
	multipartHandler := grpcapi.NewMultipartHandler(log.Named("multipart_handler"), objSvc)
	path, handler = grpcapiconnect.NewMultipartUploadServiceHandler(multipartHandler, interceptors)
	mux.Handle(path, handler)

	// ─── PresignService ────────────────────────────────────────────────────
	presignHandler := grpcapi.NewPresignHandler(log.Named("presign_handler"), objSvc)
	path, handler = grpcapiconnect.NewPresignServiceHandler(presignHandler, interceptors)
	mux.Handle(path, handler)

	// ─── BulkService ───────────────────────────────────────────────────────
	bulkHandler := grpcapi.NewBulkHandler(log.Named("bulk_handler"), objSvc)
	path, handler = grpcapiconnect.NewBulkServiceHandler(bulkHandler, interceptors)
	mux.Handle(path, handler)

	// ─── BucketService ─────────────────────────────────────────────────────
	bucketHandler := grpcapi.NewBucketHandler(log.Named("bucket_handler"), objSvc)
	path, handler = grpcapiconnect.NewBucketServiceHandler(bucketHandler, interceptors)
	mux.Handle(path, handler)

	// ─── SystemService ─────────────────────────────────────────────────────
	systemHandler := grpcapi.NewSystemHandler(log.Named("system_handler"), hs, metadata, started, startTime, cfg, nil)
	path, handler = grpcapiconnect.NewSystemServiceHandler(systemHandler, interceptors)
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
			"Grpc-Timeout",
			"X-Grpc-Web",
			"X-User-Agent",
			"X-Request-Id",
			"X-Tenant-Id",
			"Authorization",
		},
		ExposedHeaders: []string{
			"Grpc-Status",
			"Grpc-Message",
			"Grpc-Status-Details-Bin",
			"X-Request-Id",
		},
	})

	return &Server{mux: c.Handler(mux)}
}

// Handler returns the composed http.Handler.
func (s *Server) Handler() http.Handler { return s.mux }
