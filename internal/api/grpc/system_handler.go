package grpcapi

import (
	"context"
	"runtime"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/service"
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
}

// NewSystemHandler creates a new system handler.
func NewSystemHandler(
	log *zap.Logger,
	healthSvc *service.HealthService,
	metadata domain.AppMetadata,
	started *atomic.Bool,
	startTime time.Time,
	cfg *config.Config,
) *SystemHandler {
	return &SystemHandler{
		log:         log,
		healthSvc:   healthSvc,
		metadata:    metadata,
		started:     started,
		startTime:   startTime,
		serviceName: cfg.Server.Name,
		environment: cfg.Env,
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

	var protoDeps []*DependencyStatus

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
			GoVersion: runtime.Version(),
			Os:        runtime.GOOS,
			Arch:      runtime.GOARCH,
			StartTime: timestamppb.New(h.startTime),
			Uptime:    time.Since(h.startTime).String(),
		},
		Environment: h.environment,
	}), nil
}
