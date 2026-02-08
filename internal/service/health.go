package service

import (
	"context"
	"github.com/oleg-tkachuk/paladin/internal/breaker"
	"time"
)

// DetailedDependencyStatus provides detailed health information for a single dependency
type DetailedDependencyStatus struct {
	Status    string `json:"status"`               // ok, degraded, down
	LatencyMs *int64 `json:"latency_ms,omitempty"` // Response time in milliseconds
	Message   string `json:"message,omitempty"`    // Additional context if not ok
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
	Health(ctx context.Context) error
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
		status.PostgreSQL.Status = "down"
		status.PostgreSQL.Message = err.Error()
		ready = false
	} else {
		latency := time.Since(start).Milliseconds()
		status.PostgreSQL.LatencyMs = &latency

		// Get detailed pool stats if available
		if s.poolDB != nil {
			if stats, err := s.poolDB.HealthWithStats(ctx); err == nil {
				status.PoolStats = stats
			}
		}
	}

	// Check SeaweedFS with latency tracking
	start = time.Now()
	if err := s.s3.Health(ctx); err != nil {
		status.SeaweedFS.Status = "down"
		status.SeaweedFS.Message = err.Error()
		ready = false
	} else {
		latency := time.Since(start).Milliseconds()
		status.SeaweedFS.LatencyMs = &latency
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
