package service

import (
	"context"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/breaker"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/metrics"
)

// DetailedDependencyStatus provides detailed health information for a single dependency
type DetailedDependencyStatus struct {
	Status    string               `json:"status"`               // ok, degraded, down
	LatencyMs *int64               `json:"latency_ms,omitempty"` // Response time in milliseconds
	Message   string               `json:"message,omitempty"`    // Additional context if not ok
	S3Ping    *domain.S3PingResult `json:"s3_ping,omitempty"`    // Detailed S3 ping result
}

// DependencyStatus represents the overall health status of all dependencies
type DependencyStatus struct {
	PostgreSQL DetailedDependencyStatus `json:"postgresql"`
	SeaweedFS  DetailedDependencyStatus `json:"seaweedfs"`
	Breakers   map[string]string        `json:"breakers,omitempty"`
	PoolStats  map[string]interface{}   `json:"pool_stats,omitempty"`
}

type Pinger interface {
	Ping(ctx context.Context) error
}

type S3HealthChecker interface {
	Ping(ctx context.Context) (domain.S3PingResult, error)
}

type PoolStatsProvider interface {
	HealthWithStats(ctx context.Context) (map[string]interface{}, error)
}

type HealthService struct {
	db      Pinger
	s3      S3HealthChecker
	breaker breaker.Factory
	poolDB  PoolStatsProvider // Optional, for detailed pool stats
}

func NewHealthService(db Pinger, s3 S3HealthChecker, breaker breaker.Factory) *HealthService {
	// Try to cast db to PoolStatsProvider for enhanced stats
	var poolDB PoolStatsProvider
	if p, ok := db.(PoolStatsProvider); ok {
		poolDB = p
	}

	return &HealthService{
		db:      db,
		s3:      s3,
		breaker: breaker,
		poolDB:  poolDB,
	}
}

func (s *HealthService) CheckReady(ctx context.Context) (bool, DependencyStatus) {
	status := DependencyStatus{
		PostgreSQL: DetailedDependencyStatus{Status: "ok"},
		SeaweedFS:  DetailedDependencyStatus{Status: "ok"},
		Breakers:   s.breaker.CheckHealth(),
	}
	ready := true

	// Check PostgreSQL with latency tracking
	start := time.Now()
	if err := s.db.Ping(ctx); err != nil {
		metrics.RecordDbQuery(ctx, "ping", "error", start)
		status.PostgreSQL.Status = "down"
		status.PostgreSQL.Message = err.Error()
		ready = false
	} else {
		metrics.RecordDbQuery(ctx, "ping", "success", start)
		latency := time.Since(start).Milliseconds()
		status.PostgreSQL.LatencyMs = &latency

		// Get detailed pool stats if available
		if s.poolDB != nil {
			if stats, err := s.poolDB.HealthWithStats(ctx); err == nil {
				status.PoolStats = stats
			}
		}
	}

	// Check SeaweedFS with detailed ping logic
	start = time.Now()
	pingResult, err := s.s3.Ping(ctx)
	latency := time.Since(start).Milliseconds()
	status.SeaweedFS.LatencyMs = &latency

	if err != nil {
		metrics.RecordS3Op(ctx, "health", "error", start)
		status.SeaweedFS.Status = "down"
		status.SeaweedFS.Message = err.Error()
		ready = false
	} else {
		metrics.RecordS3Op(ctx, "health", "success", start)

		// Map S3PingResult status to DetailedDependencyStatus status
		switch pingResult.Status {
		case "healthy":
			status.SeaweedFS.Status = "ok"
		case "degraded":
			status.SeaweedFS.Status = "degraded"
			// Degraded S3 (e.g. bucket missing) means not ready
			ready = false
		case "unavailable":
			status.SeaweedFS.Status = "down"
			ready = false
		default:
			status.SeaweedFS.Status = "down"
			ready = false
		}

		if pingResult.Message != "" {
			status.SeaweedFS.Message = pingResult.Message
		}
		status.SeaweedFS.S3Ping = &pingResult
	}

	// Check if any breakers are open
	for _, bState := range status.Breakers {
		if bState == "open" {
			ready = false
			break
		}
	}

	return ready, status
}

func (s *HealthService) PingS3(ctx context.Context) (domain.S3PingResult, error) {
	return s.s3.Ping(ctx)
}
