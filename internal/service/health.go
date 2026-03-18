package service

import (
	"context"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/breaker"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/metrics"
	"golang.org/x/sync/errgroup"
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

const (
	statusDown = "down"
)

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

	// Run PostgreSQL and SeaweedFS pings in parallel using errgroup
	g, gCtx := errgroup.WithContext(ctx)

	// Check PostgreSQL with latency tracking
	g.Go(func() error {
		pStart := time.Now()
		if err := s.db.Ping(gCtx); err != nil {
			metrics.RecordDbQuery(gCtx, "ping", "error", pStart)
			status.PostgreSQL.Status = domain.StatusDown
			status.PostgreSQL.Message = err.Error()
			ready = false
		} else {
			metrics.RecordDbQuery(gCtx, "ping", "success", pStart)
			latency := time.Since(pStart).Milliseconds()
			status.PostgreSQL.LatencyMs = &latency

			// Get detailed pool stats if available
			if s.poolDB != nil {
				if stats, err := s.poolDB.HealthWithStats(gCtx); err == nil {
					status.PoolStats = stats
				}
			}
		}

		return nil
	})

	// Check SeaweedFS with detailed ping logic
	g.Go(func() error {
		sStart := time.Now()
		pingResult, err := s.s3.Ping(gCtx)
		sLatency := time.Since(sStart).Milliseconds()
		status.SeaweedFS.LatencyMs = &sLatency

		if err != nil {
			metrics.RecordS3Op(gCtx, "health", "error", sStart)
			status.SeaweedFS.Status = statusDown
			status.SeaweedFS.Message = err.Error()
			ready = false
		} else {
			metrics.RecordS3Op(gCtx, "health", "success", sStart)

			// Map S3PingResult status to DetailedDependencyStatus status
			switch pingResult.Status {
			case "healthy":
				status.SeaweedFS.Status = "ok"
			case "degraded":
				status.SeaweedFS.Status = "degraded"
				// Degraded S3 (e.g. bucket missing) means not ready
				ready = false
			case "unavailable":
				status.SeaweedFS.Status = statusDown
				ready = false
			default:
				status.SeaweedFS.Status = statusDown
				ready = false
			}

			if pingResult.Message != "" {
				status.SeaweedFS.Message = pingResult.Message
			}
			status.SeaweedFS.S3Ping = &pingResult
		}

		return nil
	})

	_ = g.Wait()

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
