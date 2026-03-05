package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/errors"
)

type categoryService struct {
	repo domain.CategoryRepository
}

// NewCategoryService creates a new CategoryService.
func NewCategoryService(repo domain.CategoryRepository) domain.CategoryService {
	return &categoryService{
		repo: repo,
	}
}

func (s *categoryService) Create(ctx context.Context, tenantID, slug, name string, description *string) (*domain.Category, error) {
	cat := domain.Category{
		ID:          uuid.New(),
		TenantID:    tenantID,
		Slug:        slug,
		Name:        name,
		Description: description,
	}
	if err := s.repo.Create(ctx, cat); err != nil {
		return nil, err
	}
	// Fetch back to get created_at / updated_at / id
	return s.repo.Get(ctx, tenantID, slug)
}

func (s *categoryService) Get(ctx context.Context, tenantID, slug string) (*domain.Category, error) {
	return s.repo.Get(ctx, tenantID, slug)
}

func (s *categoryService) List(ctx context.Context, tenantID string, limit int, cursor string) ([]domain.Category, string, int64, error) {
	return s.repo.List(ctx, tenantID, limit, cursor)
}

func (s *categoryService) Delete(ctx context.Context, tenantID, slug string) error {
	count, err := s.repo.ObjectCount(ctx, tenantID, slug)
	if err != nil {
		return err
	}
	if count > 0 {
		return errors.Conflict("Cannot delete category because it still contains active objects", nil)
	}

	_, err = s.repo.Delete(ctx, tenantID, slug)

	return err
}

func (s *categoryService) GetStats(ctx context.Context, tenantID, slug string) (*domain.CategoryStats, error) {
	return s.repo.GetStats(ctx, tenantID, slug)
}

func (s *categoryService) ListTenants(ctx context.Context, limit int, cursor string) ([]string, string, int64, error) {
	return s.repo.ListTenants(ctx, limit, cursor)
}
