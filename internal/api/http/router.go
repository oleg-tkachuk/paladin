package httpapi

import (
	"encoding/json"
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
	svc domain.ObjectsService,
	metadata domain.AppMetadata,
	hs *service.HealthService,
	started *atomic.Bool,
	startTime time.Time,
) *Server {
	mux := http.NewServeMux()

	interceptors := connect.WithInterceptors(middleware.SetupConnectInterceptors(cfg, log)...)

	// ─── ObjectService ─────────────────────────────────────────────────────
	objectHandler := grpcapi.NewObjectHandler(log, svc)
	path, handler := grpcapiconnect.NewObjectServiceHandler(objectHandler, interceptors)
	mux.Handle(path, handler)

	// ─── MultipartUploadService ────────────────────────────────────────────
	multipartHandler := grpcapi.NewMultipartHandler(log, svc)
	path, handler = grpcapiconnect.NewMultipartUploadServiceHandler(multipartHandler, interceptors)
	mux.Handle(path, handler)

	// ─── PresignService ────────────────────────────────────────────────────
	presignHandler := grpcapi.NewPresignHandler(log, svc)
	path, handler = grpcapiconnect.NewPresignServiceHandler(presignHandler, interceptors)
	mux.Handle(path, handler)

	// ─── BulkService ───────────────────────────────────────────────────────
	bulkHandler := grpcapi.NewBulkHandler(log, svc)
	path, handler = grpcapiconnect.NewBulkServiceHandler(bulkHandler, interceptors)
	mux.Handle(path, handler)

	// ─── BucketService ─────────────────────────────────────────────────────
	bucketHandler := grpcapi.NewBucketHandler()
	path, handler = grpcapiconnect.NewBucketServiceHandler(bucketHandler, interceptors)
	mux.Handle(path, handler)

	// ─── SystemService ─────────────────────────────────────────────────────
	systemHandler := grpcapi.NewSystemHandler(log, hs, metadata, started, startTime, cfg)
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

// writeJSON is a minimal JSON response helper (replaces gin.Context.JSON).
func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
