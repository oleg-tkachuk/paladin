package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	apperrors "github.com/oleg-tkachuk/paladin/internal/errors"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/validation"
	"go.uber.org/zap"
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
	if err := validation.CategorySlug(slug); err != nil {
		return nil, apperrors.BadRequest("invalid category slug", err)
	}
	if err := validation.CategoryName(name); err != nil {
		return nil, apperrors.BadRequest("invalid category name", err)
	}
	if description != nil {
		if err := validation.CategoryDescription(*description); err != nil {
			return nil, apperrors.BadRequest("invalid category description", err)
		}
	}

	// If slug contains slashes, check if parent categories exist
	if lastSlash := strings.LastIndex(slug, domain.CategorySeparator); lastSlash > 0 {
		parentSlug := slug[:lastSlash]
		exists, err := s.repo.Exists(ctx, tenantID, parentSlug)
		if err != nil {
			logger.FromContext(ctx).Error("Failed to check parent category existence", zap.Error(err), zap.String("parent_slug", parentSlug))
			return nil, err
		}
		if !exists {
			return nil, apperrors.NotFound(fmt.Sprintf("parent category %q not found", parentSlug), nil)
		}
	}

	cat := domain.Category{
		ID:          uuid.New(),
		TenantID:    tenantID,
		Slug:        slug,
		Name:        name,
		Description: description,
	}
	if err := s.repo.Create(ctx, cat); err != nil {
		logger.FromContext(ctx).Error("Failed to create category", zap.Error(err), zap.String("slug", slug))
		return nil, err
	}
	// Fetch back to get created_at / updated_at / id
	return s.repo.Get(ctx, tenantID, slug)
}

func (s *categoryService) Update(ctx context.Context, tenantID, slug, name string, description *string) (*domain.Category, error) {
	if err := validation.CategoryName(name); err != nil {
		return nil, apperrors.BadRequest("invalid category name", err)
	}
	if description != nil {
		if err := validation.CategoryDescription(*description); err != nil {
			return nil, apperrors.BadRequest("invalid category description", err)
		}
	}

	cat := domain.Category{
		TenantID:    tenantID,
		Slug:        slug,
		Name:        name,
		Description: description,
	}

	if err := s.repo.Update(ctx, cat); err != nil {
		return nil, err
	}

	return s.repo.Get(ctx, tenantID, slug)
}

func (s *categoryService) Get(ctx context.Context, tenantID, slug string) (*domain.Category, error) {
	return s.repo.Get(ctx, tenantID, slug)
}

func (s *categoryService) List(ctx context.Context, tenantID string, filter domain.ListCategoriesFilter) ([]domain.Category, string, int64, error) {
	return s.repo.List(ctx, tenantID, filter)
}

func (s *categoryService) Delete(ctx context.Context, tenantID, slug string) error {
	count, err := s.repo.ObjectCount(ctx, tenantID, slug)
	if err != nil {
		logger.FromContext(ctx).Error("Failed to count objects in category", zap.Error(err), zap.String("slug", slug))
		return err
	}
	if count > 0 {
		return apperrors.Conflict("Cannot delete category because it still contains active objects", nil)
	}

	deleted, err := s.repo.Delete(ctx, tenantID, slug)
	if err != nil {
		logger.FromContext(ctx).Error("Failed to delete category", zap.Error(err), zap.String("slug", slug))
		return err
	}
	if !deleted {
		return apperrors.NotFound(fmt.Sprintf("category %q not found", slug), nil)
	}

	return nil
}

func (s *categoryService) GetStats(ctx context.Context, tenantID, slug string) (*domain.CategoryStats, error) {
	return s.repo.GetStats(ctx, tenantID, slug)
}

func (s *categoryService) ListTenants(ctx context.Context, limit int, cursor string) ([]string, string, int64, error) {
	return s.repo.ListTenants(ctx, limit, cursor)
}
