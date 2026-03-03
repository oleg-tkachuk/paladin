package service

import (
	"context"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/domain"
)

type systemService struct {
	cfg *config.Config
}

func NewSystemService(cfg *config.Config) domain.SystemService {
	return &systemService{cfg: cfg}
}

func (s *systemService) GetConfig(ctx context.Context) (domain.SystemConfig, error) {
	// Note: We avoid importing pgx here to follow layer rules.
	// DSN parsing logic is moved to a helper in internal/config or similar if needed,
	// but for now, we'll just return the structured config.
	// The actual DSN parsing for the admin view will be handled by a specialized method in internal/config
	// or we can pass a pre-parsed info if we want to be very strict.

	return s.cfg.Sanitize(), nil
}
