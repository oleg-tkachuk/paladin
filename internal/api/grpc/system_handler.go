package grpcapi

import (
	"context"
	"os"
	"runtime"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/process"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/service"
	"google.golang.org/grpc"
)

const (
	healthStatusOK = "ok"
	pongMessage    = "pong"
)

// SystemHandler implements grpcapiconnect.SystemServiceHandler.
type SystemHandler struct {
	log         *zap.Logger
	healthSvc   *service.HealthService
	metadata    domain.AppMetadata
	started     *atomic.Bool
	startTime   time.Time
	serviceName string
	environment string
	cfg         *config.Config
	auditRepo   domain.AuditLogRepository
}

// NewSystemHandler creates a new system handler.
func NewSystemHandler(
	log *zap.Logger,
	healthSvc *service.HealthService,
	metadata domain.AppMetadata,
	started *atomic.Bool,
	startTime time.Time,
	cfg *config.Config,
	auditRepo domain.AuditLogRepository,
) *SystemHandler {
	return &SystemHandler{
		log:         log,
		healthSvc:   healthSvc,
		metadata:    metadata,
		started:     started,
		startTime:   startTime,
		serviceName: cfg.Server.Name,
		environment: cfg.Env,
		cfg:         cfg,
		auditRepo:   auditRepo,
	}
}

func (h *SystemHandler) Ping(ctx context.Context, req *connect.Request[PingRequest]) (*connect.Response[PingResponse], error) {
	return connect.NewResponse(&PingResponse{Message: pongMessage}), nil
}

func (h *SystemHandler) GetLivez(ctx context.Context, req *connect.Request[GetLivezRequest]) (*connect.Response[GetLivezResponse], error) {
	return connect.NewResponse(&GetLivezResponse{
		Status: healthStatusOK,
		Health: HealthStatus_HEALTH_STATUS_HEALTHY,
	}), nil
}

func (h *SystemHandler) GetStartupz(ctx context.Context, req *connect.Request[GetStartupzRequest]) (*connect.Response[GetStartupzResponse], error) {
	if !h.started.Load() {
		return connect.NewResponse(&GetStartupzResponse{
			Status: "starting",
			Health: HealthStatus_HEALTH_STATUS_UNHEALTHY,
		}), nil
	}

	return connect.NewResponse(&GetStartupzResponse{
		Status: healthStatusOK,
		Health: HealthStatus_HEALTH_STATUS_HEALTHY,
	}), nil
}

func (h *SystemHandler) GetReadyz(ctx context.Context, req *connect.Request[GetReadyzRequest]) (*connect.Response[GetReadyzResponse], error) {
	ready, deps := h.healthSvc.CheckReady(ctx)

	overall := HealthStatus_HEALTH_STATUS_HEALTHY
	healthStatus := healthStatusOK

	if !ready {
		overall = HealthStatus_HEALTH_STATUS_UNHEALTHY
		healthStatus = "not_ready"
	}

	protoDeps := make([]*DependencyStatus, 0, 2)

	// Map PostgreSQL dependency.
	pgDep := &DependencyStatus{
		Name:    "postgresql",
		Message: deps.PostgreSQL.Message,
	}
	if deps.PostgreSQL.LatencyMs != nil {
		pgDep.LatencyMs = *deps.PostgreSQL.LatencyMs
	}
	switch deps.PostgreSQL.Status {
	case "ok":
		pgDep.Health = HealthStatus_HEALTH_STATUS_HEALTHY
	case "degraded":
		pgDep.Health = HealthStatus_HEALTH_STATUS_DEGRADED
	default:
		pgDep.Health = HealthStatus_HEALTH_STATUS_UNHEALTHY
	}
	protoDeps = append(protoDeps, pgDep)

	// Map SeaweedFS dependency.
	s3Dep := &DependencyStatus{
		Name:    "seaweedfs",
		Message: deps.SeaweedFS.Message,
	}
	if deps.SeaweedFS.LatencyMs != nil {
		s3Dep.LatencyMs = *deps.SeaweedFS.LatencyMs
	}
	switch deps.SeaweedFS.Status {
	case "ok":
		s3Dep.Health = HealthStatus_HEALTH_STATUS_HEALTHY
	case "degraded":
		s3Dep.Health = HealthStatus_HEALTH_STATUS_DEGRADED
	default:
		s3Dep.Health = HealthStatus_HEALTH_STATUS_UNHEALTHY
	}
	protoDeps = append(protoDeps, s3Dep)

	return connect.NewResponse(&GetReadyzResponse{
		Status:       healthStatus,
		Health:       overall,
		Dependencies: protoDeps,
	}), nil
}

func (h *SystemHandler) GetInfo(ctx context.Context, req *connect.Request[GetInfoRequest]) (*connect.Response[GetInfoResponse], error) {
	buildTime, _ := time.Parse(time.RFC3339, h.metadata.BuildTime)
	var buildTimeProto *timestamppb.Timestamp
	if !buildTime.IsZero() {
		buildTimeProto = timestamppb.New(buildTime)
	}

	return connect.NewResponse(&GetInfoResponse{
		Service: &ServiceInfo{
			Name:    h.serviceName,
			Version: h.metadata.Version,
		},
		Build: &BuildInfo{
			Commit:    h.metadata.Commit,
			BuildTime: buildTimeProto,
		},
		Runtime: &RuntimeInfo{
			GoVersion:        runtime.Version(),
			Os:               runtime.GOOS,
			Arch:             runtime.GOARCH,
			StartTime:        timestamppb.New(h.startTime),
			Uptime:           time.Since(h.startTime).String(),
			CpuUsagePercent:  h.getCPUUsage(ctx),
			MemoryRssBytes:   h.getMemoryRSS(ctx),
			MemoryHeapBytes:  h.getMemoryHeap(),
			ActiveGoroutines: int32(runtime.NumGoroutine()), //nolint:gosec // Always fits in int32
		},
		Environment: h.environment,
	}), nil
}

func (h *SystemHandler) GetConfig(ctx context.Context, req *connect.Request[GetConfigRequest]) (*connect.Response[GetConfigResponse], error) {
	c := h.cfg.Sanitize()

	return connect.NewResponse(&GetConfigResponse{
		Config: &SystemConfig{
			App: &AppConfig{
				Name: c.App.Name,
				Env:  c.App.Env,
			},
			Server: &ServerConfig{
				Name: c.Server.Name,
				Mode: c.Server.Mode,
				Http: &HttpConfig{
					Addr:               c.Server.HTTP.Addr,
					CorsAllowedOrigins: c.Server.HTTP.CORSAllowedOrigins,
					ReadTimeout:        c.Server.HTTP.ReadTimeout,
					WriteTimeout:       c.Server.HTTP.WriteTimeout,
					RequestIdHeader:    c.Server.HTTP.RequestIDHeader,
				},
			},
			Datastores: &DatastoreConfig{
				Postgres: &PostgresConfig{
					Host:    c.Datastores.Postgres.Host,
					Port:    c.Datastores.Postgres.Port,
					User:    c.Datastores.Postgres.User,
					Dbname:  c.Datastores.Postgres.Dbname,
					SslMode: c.Datastores.Postgres.SslMode,
				},
				S3: &S3Config{
					Bucket:         c.Datastores.S3.Bucket,
					Endpoint:       c.Datastores.S3.Endpoint,
					PublicEndpoint: c.Datastores.S3.PublicEndpoint,
					ForcePathStyle: c.Datastores.S3.ForcePathStyle,
					PresignTtl:     c.Datastores.S3.PresignTTL,
					PartSize:       c.Datastores.S3.PartSize,
					SseType:        c.Datastores.S3.SSEType,
				},
			},
			Policy: &PolicyConfig{
				MaxObjectSize:       c.Policy.MaxObjectSize,
				MaxMultipartSize:    c.Policy.MaxMultipartSize,
				MinPartSize:         c.Policy.MinPartSize,
				MaxPartSize:         c.Policy.MaxPartSize,
				PresignPutTtl:       c.Policy.PresignPutTTL,
				PresignGetTtl:       c.Policy.PresignGetTTL,
				AllowedContentTypes: c.Policy.AllowedContentTypes,
			},
			Security: &SecurityConfig{
				TrustTenantIdFromRequest: c.Security.TrustTenantIDFromRequest,
				RejectTenantMismatch:     c.Security.RejectTenantMismatch,
				EnableRls:                c.Security.EnableRLS,
			},
			Housekeeping: &HousekeepingConfig{
				EnableReaper: c.Housekeeping.EnableReaper,
				PendingTtl:   c.Housekeeping.PendingTTL,
				MultipartTtl: c.Housekeeping.MultipartTTL,
				GcInterval:   c.Housekeeping.GCInterval,
			},
			RateLimit: &RateLimitConfig{
				RequestsPerSecond: c.RateLimit.RequestsPerSecond,
				Burst:             int32(c.RateLimit.Burst),      //nolint:gosec
				MaxTenants:        int32(c.RateLimit.MaxTenants), //nolint:gosec
			},
			Cache: &CacheConfig{
				Enabled: c.Cache.Enabled,
				MaxSize: int32(c.Cache.MaxSize), //nolint:gosec
				Ttl:     c.Cache.TTL,
			},
			Timeouts: &TimeoutConfig{
				FastOperation:    c.Timeouts.FastOperation,
				DefaultOperation: c.Timeouts.DefaultOperation,
				S3Operation:      c.Timeouts.S3Operation,
				LongOperation:    c.Timeouts.LongOperation,
			},
			Idempotency: &IdempotencyConfig{
				Enabled: c.Idempotency.Enabled,
				Ttl:     c.Idempotency.TTL,
			},
			Otel: &OTelConfig{
				Enabled:  c.OTel.Enabled,
				Endpoint: c.OTel.Endpoint,
				Protocol: c.OTel.Protocol,
				Insecure: c.OTel.Insecure,
			},
			AuthEnabled: c.Auth.Enabled,
		},
	}), nil
}

func (h *SystemHandler) ListAuditLogs(ctx context.Context, req *connect.Request[ListAuditLogsRequest]) (*connect.Response[ListAuditLogsResponse], error) {
	limit := int(req.Msg.Limit)
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	logs, nextCursor, totalCount, err := h.auditRepo.List(ctx, req.Msg.TenantId, domain.ListAuditLogsFilter{
		LogType: req.Msg.LogType,
	}, limit, req.Msg.Cursor)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	protoLogs := make([]*AuditLog, 0, len(logs))
	for _, l := range logs {
		var httpStatus int32
		if l.HTTPStatus != nil {
			httpStatus = int32(*l.HTTPStatus) //nolint:gosec
		}

		protoLogs = append(protoLogs, &AuditLog{
			AuditId:        l.ID.String(),
			TenantId:       l.TenantID,
			RequestId:      l.RequestID,
			TraceId:        l.TraceID,
			ActorSubject:   l.ActorSubject,
			ActorType:      string(l.ActorType),
			ClientIp:       l.ClientIP,
			Method:         l.Method,
			Path:           l.Path,
			HttpStatus:     httpStatus,
			ResponseStatus: l.ResponseStatus,
			CreatedAt:      timestamppb.New(l.CreatedAt),
			LogType:        l.LogType,
		})
	}

	return connect.NewResponse(&ListAuditLogsResponse{
		Logs:       protoLogs,
		NextCursor: nextCursor,
		TotalCount: totalCount,
	}), nil
}

// ────────────────────────────────────────────────────────────────────────────
// gRPC Bridge
// ────────────────────────────────────────────────────────────────────────────

type systemGRPCServer struct {
	UnimplementedSystemServiceServer
	h *SystemHandler
}

// RegisterGRPC registers the handler as a native gRPC server.
func (h *SystemHandler) RegisterGRPC(srv *grpc.Server) {
	RegisterSystemServiceServer(srv, &systemGRPCServer{h: h})
}

func (s *systemGRPCServer) Ping(ctx context.Context, req *PingRequest) (*PingResponse, error) {
	res, err := s.h.Ping(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (s *systemGRPCServer) GetLivez(ctx context.Context, req *GetLivezRequest) (*GetLivezResponse, error) {
	res, err := s.h.GetLivez(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (s *systemGRPCServer) GetStartupz(ctx context.Context, req *GetStartupzRequest) (*GetStartupzResponse, error) {
	res, err := s.h.GetStartupz(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (s *systemGRPCServer) GetReadyz(ctx context.Context, req *GetReadyzRequest) (*GetReadyzResponse, error) {
	res, err := s.h.GetReadyz(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (s *systemGRPCServer) GetInfo(ctx context.Context, req *GetInfoRequest) (*GetInfoResponse, error) {
	res, err := s.h.GetInfo(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (s *systemGRPCServer) GetConfig(ctx context.Context, req *GetConfigRequest) (*GetConfigResponse, error) {
	res, err := s.h.GetConfig(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (s *systemGRPCServer) ListAuditLogs(ctx context.Context, req *ListAuditLogsRequest) (*ListAuditLogsResponse, error) {
	res, err := s.h.ListAuditLogs(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (h *SystemHandler) getCPUUsage(ctx context.Context) float64 {
	perc, err := cpu.PercentWithContext(ctx, 0, false)
	if err != nil || len(perc) == 0 {
		return 0
	}

	return perc[0]
}

func (h *SystemHandler) getMemoryRSS(ctx context.Context) uint64 {
	p, err := process.NewProcess(int32(os.Getpid())) //nolint:gosec
	if err != nil {
		return 0
	}
	info, err := p.MemoryInfoWithContext(ctx)
	if err != nil {
		return 0
	}

	return info.RSS
}

func (h *SystemHandler) getMemoryHeap() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return m.HeapAlloc
}
