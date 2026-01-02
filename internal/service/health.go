package service

import (
	"context"
	"paladin/internal/breaker"
)

type DependencyStatus struct {
	PostgreSQL string            `json:"postgresql"`
	SeaweedFS  string            `json:"seaweedfs"`
	Breakers   map[string]string `json:"breakers,omitempty"`
}

type Pinger interface {
	Ping(ctx context.Context) error
}

type S3HealthChecker interface {
	Health(ctx context.Context) error
}

type HealthService struct {
	db      Pinger
	s3      S3HealthChecker
	breaker breaker.Factory
}

func NewHealthService(db Pinger, s3 S3HealthChecker, breaker breaker.Factory) *HealthService {
	return &HealthService{
		db:      db,
		s3:      s3,
		breaker: breaker,
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
