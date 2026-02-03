package service

import (
	"context"
	"paladin/internal/breaker"
)

type DependencyStatus struct {
	PostgreSQL string                 `json:"postgresql"`
	SeaweedFS  string                 `json:"seaweedfs"`
	Breakers   map[string]string      `json:"breakers,omitempty"`
	PoolStats  map[string]interface{} `json:"pool_stats,omitempty"`
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
		PostgreSQL: "ok",
		SeaweedFS:  "ok",
		Breakers:   s.breaker.CheckHealth(),
	}
	ready := true

	if err := s.db.Ping(ctx); err != nil {
		status.PostgreSQL = err.Error()
		ready = false
	} else if s.poolDB != nil {
		// Get detailed pool stats if available
		if stats, err := s.poolDB.HealthWithStats(ctx); err == nil {
			status.PoolStats = stats
		}
	}

	if err := s.s3.Health(ctx); err != nil {
		status.SeaweedFS = err.Error()
		ready = false
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
