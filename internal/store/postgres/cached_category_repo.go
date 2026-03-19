package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/cache"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/metrics"
	"go.uber.org/zap"
)

// CachedCategoryRepo wraps CategoryRepo with an LRU cache
type CachedCategoryRepo struct {
	log   *zap.Logger
	repo  domain.CategoryRepository
	cache *cache.Cache[string, any] // stores either *domain.Category or bool
	ttl   time.Duration
}

// NewCachedCategoryRepo creates a cached repository wrapper for categories
func NewCachedCategoryRepo(repo domain.CategoryRepository, cacheSize int, ttl time.Duration, log *zap.Logger) *CachedCategoryRepo {
	return &CachedCategoryRepo{
		repo:  repo,
		cache: cache.NewCache[string, any](cacheSize, ttl),
		log:   log,
		ttl:   ttl,
	}
}

// Create creates a category and invalidates related caches
func (r *CachedCategoryRepo) Create(ctx context.Context, rec domain.Category) error {
	err := r.repo.Create(ctx, rec)
	if err != nil {
		return err
	}

	// Invalidate caches
	existsKey := fmt.Sprintf("cat:exists:%s:%s", rec.TenantID, rec.Slug)
	getKey := fmt.Sprintf("cat:get:%s:%s", rec.TenantID, rec.Slug)
	if err := r.cache.Delete(ctx, existsKey); err != nil {
		r.log.Warn("cache delete failed", zap.Error(err))
	}
	if err := r.cache.Delete(ctx, getKey); err != nil {
		r.log.Warn("cache delete failed", zap.Error(err))
	}

	return nil
}

// Update modifies a category and invalidates related caches
func (r *CachedCategoryRepo) Update(ctx context.Context, rec domain.Category) error {
	err := r.repo.Update(ctx, rec)
	if err != nil {
		return err
	}

	// Invalidate caches
	getKey := fmt.Sprintf("cat:get:%s:%s", rec.TenantID, rec.Slug)
	if err := r.cache.Delete(ctx, getKey); err != nil {
		r.log.Warn("cache delete failed", zap.Error(err))
	}

	return nil
}

// Get retrieves a category with caching
func (r *CachedCategoryRepo) Get(ctx context.Context, tenantID, slug string) (*domain.Category, error) {
	cacheKey := fmt.Sprintf("cat:get:%s:%s", tenantID, slug)

	if val, ok := r.cache.Get(ctx, cacheKey); ok {
		if cat, ok := val.(*domain.Category); ok {
			metrics.RecordCacheOp(ctx, "category_get", "hit")

			return cat, nil
		}
	}

	metrics.RecordCacheOp(ctx, "category_get", "miss")

	cat, err := r.repo.Get(ctx, tenantID, slug)
	if err != nil {
		return nil, err
	}

	if err := r.cache.Set(ctx, cacheKey, cat, r.ttl); err != nil {
		r.log.Warn("cache set failed", zap.Error(err))
	}

	return cat, nil
}

// List delegates to underlying repo (no caching for list)
func (r *CachedCategoryRepo) List(ctx context.Context, tenantID string, filter domain.ListCategoriesFilter) ([]domain.Category, string, int64, error) {
	return r.repo.List(ctx, tenantID, filter)
}

// Delete removes a category and invalidates related caches
func (r *CachedCategoryRepo) Delete(ctx context.Context, tenantID, slug string) (bool, error) {
	deleted, err := r.repo.Delete(ctx, tenantID, slug)
	if err != nil {
		return false, err
	}

	if deleted {
		// Invalidate caches
		existsKey := fmt.Sprintf("cat:exists:%s:%s", tenantID, slug)
		getKey := fmt.Sprintf("cat:get:%s:%s", tenantID, slug)
		if err := r.cache.Delete(ctx, existsKey); err != nil {
			r.log.Warn("cache delete failed", zap.Error(err))
		}
		if err := r.cache.Delete(ctx, getKey); err != nil {
			r.log.Warn("cache delete failed", zap.Error(err))
		}
	}

	return deleted, nil
}

// Exists checks category existence with caching
func (r *CachedCategoryRepo) Exists(ctx context.Context, tenantID, slug string) (bool, error) {
	cacheKey := fmt.Sprintf("cat:exists:%s:%s", tenantID, slug)

	if val, ok := r.cache.Get(ctx, cacheKey); ok {
		if exists, ok := val.(bool); ok {
			metrics.RecordCacheOp(ctx, "category_exists", "hit")

			return exists, nil
		}
	}

	metrics.RecordCacheOp(ctx, "category_exists", "miss")

	exists, err := r.repo.Exists(ctx, tenantID, slug)
	if err != nil {
		return false, err
	}

	if err := r.cache.Set(ctx, cacheKey, exists, r.ttl); err != nil {
		r.log.Warn("cache set failed", zap.Error(err))
	}

	return exists, nil
}

func (r *CachedCategoryRepo) ObjectCount(ctx context.Context, tenantID, slug string) (int64, error) {
	return r.repo.ObjectCount(ctx, tenantID, slug)
}

// GetStats retrieves category statistics with caching
func (r *CachedCategoryRepo) GetStats(ctx context.Context, tenantID, slug string) (*domain.CategoryStats, error) {
	cacheKey := fmt.Sprintf("cat:stats:%s:%s", tenantID, slug)

	if val, ok := r.cache.Get(ctx, cacheKey); ok {
		if stats, ok := val.(*domain.CategoryStats); ok {
			metrics.RecordCacheOp(ctx, "category_stats", "hit")

			return stats, nil
		}
	}

	metrics.RecordCacheOp(ctx, "category_stats", "miss")

	stats, err := r.repo.GetStats(ctx, tenantID, slug)
	if err != nil {
		return nil, err
	}

	if err := r.cache.Set(ctx, cacheKey, stats, r.ttl); err != nil {
		r.log.Warn("cache set failed", zap.Error(err))
	}

	return stats, nil
}

// ListTenants delegates to underlying repo
func (r *CachedCategoryRepo) ListTenants(ctx context.Context, limit int, cursor string) ([]string, string, int64, error) {
	return r.repo.ListTenants(ctx, limit, cursor)
}

// CacheStats returns cache statistics
func (r *CachedCategoryRepo) CacheStats() cache.CacheStats {
	return r.cache.Stats()
}
