package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/cache"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/metrics"

	"github.com/google/uuid"
)

// CachedObjectsRepo wraps ObjectsRepo with an LRU cache
type CachedObjectsRepo struct {
	repo  domain.ObjectsRepository
	cache *cache.Cache[string, *domain.Object]
	ttl   time.Duration
}

// NewCachedObjectsRepo creates a cached repository wrapper
func NewCachedObjectsRepo(repo domain.ObjectsRepository, cacheSize int, ttl time.Duration) *CachedObjectsRepo {
	return &CachedObjectsRepo{
		repo:  repo,
		cache: cache.NewCache[string, *domain.Object](cacheSize, ttl),
		ttl:   ttl,
	}
}

// Get retrieves an object with caching
func (r *CachedObjectsRepo) Get(ctx context.Context, tenantID string, id uuid.UUID) (*domain.Object, error) {
	cacheKey := fmt.Sprintf("obj:%s:%s", tenantID, id.String())

	// Try cache first
	if rec, ok := r.cache.Get(ctx, cacheKey); ok {
		metrics.RecordCacheOp(ctx, "get", "hit")
		return rec, nil
	}

	metrics.RecordCacheOp(ctx, "get", "miss")

	// Cache miss - fetch from database
	rec, err := r.repo.Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}

	// Update cache
	_ = r.cache.Set(ctx, cacheKey, rec, r.ttl)

	return rec, nil
}

// GetByExternalRef retrieves an object by external ref with caching
func (r *CachedObjectsRepo) GetByExternalRef(ctx context.Context, tenantID string, externalRef string) (*domain.Object, error) {
	cacheKey := fmt.Sprintf("obj:ext:%s:%s", tenantID, externalRef)

	// Try cache first
	if rec, ok := r.cache.Get(ctx, cacheKey); ok {
		metrics.RecordCacheOp(ctx, "get", "hit")
		return rec, nil
	}

	metrics.RecordCacheOp(ctx, "get", "miss")

	// Cache miss - fetch from database
	rec, err := r.repo.GetByExternalRef(ctx, tenantID, externalRef)
	if err != nil {
		return nil, err
	}

	// Update cache (if found)
	if rec != nil {
		_ = r.cache.Set(ctx, cacheKey, rec, r.ttl)
		// Also cache by ID
		idKey := fmt.Sprintf("obj:%s:%s", tenantID, rec.ID.String())
		_ = r.cache.Set(ctx, idKey, rec, r.ttl)
	}

	return rec, nil
}

// Create creates an object and caches it
func (r *CachedObjectsRepo) Create(ctx context.Context, rec domain.Object) error {
	err := r.repo.Create(ctx, rec)
	if err != nil {
		return err
	}

	// Cache the newly created object
	cacheKey := fmt.Sprintf("obj:%s:%s", rec.TenantID, rec.ID.String())
	_ = r.cache.Set(ctx, cacheKey, &rec, r.ttl)

	return nil
}

// MarkComplete updates object status and writes through to cache
func (r *CachedObjectsRepo) MarkComplete(ctx context.Context, tenantID string, id uuid.UUID, etag string, sizeBytes int64) (bool, error) {
	updated, err := r.repo.MarkComplete(ctx, tenantID, id, etag, sizeBytes)
	if err != nil {
		return false, err
	}

	// Write-through: re-fetch completed object and cache it
	if updated {
		if rec, err := r.repo.Get(ctx, tenantID, id); err == nil {
			cacheKey := fmt.Sprintf("obj:%s:%s", tenantID, id.String())
			_ = r.cache.Set(ctx, cacheKey, rec, r.ttl)
		}
	}

	return updated, nil
}

func (r *CachedObjectsRepo) MarkSoftDeleted(ctx context.Context, tenantID string, id uuid.UUID) (bool, error) {
	updated, err := r.repo.MarkSoftDeleted(ctx, tenantID, id)
	if err != nil {
		return false, err
	}

	// Invalidate cache
	cacheKey := fmt.Sprintf("obj:%s:%s", tenantID, id.String())
	_ = r.cache.Delete(ctx, cacheKey)

	return updated, nil
}

func (r *CachedObjectsRepo) MarkHardDeleted(ctx context.Context, tenantID string, id uuid.UUID) (bool, error) {
	updated, err := r.repo.MarkHardDeleted(ctx, tenantID, id)
	if err != nil {
		return false, err
	}

	// Invalidate cache
	cacheKey := fmt.Sprintf("obj:%s:%s", tenantID, id.String())
	_ = r.cache.Delete(ctx, cacheKey)

	return updated, nil
}

func (r *CachedObjectsRepo) Restore(ctx context.Context, tenantID string, id uuid.UUID) (bool, error) {
	updated, err := r.repo.Restore(ctx, tenantID, id)
	if err != nil {
		return false, err
	}

	// Invalidate cache
	cacheKey := fmt.Sprintf("obj:%s:%s", tenantID, id.String())
	_ = r.cache.Delete(ctx, cacheKey)

	return updated, nil
}

// MarkDeleted marks object as deleted and invalidates cache
func (r *CachedObjectsRepo) MarkDeleted(ctx context.Context, tenantID string, id uuid.UUID) (bool, error) {
	updated, err := r.repo.MarkDeleted(ctx, tenantID, id)
	if err != nil {
		return false, err
	}

	// Invalidate cache
	cacheKey := fmt.Sprintf("obj:%s:%s", tenantID, id.String())
	_ = r.cache.Delete(ctx, cacheKey)

	return updated, nil
}

// Patch updates object metadata and writes through to cache
func (r *CachedObjectsRepo) Patch(ctx context.Context, tenantID string, id uuid.UUID, labels map[string]string, externalRef *string) (*domain.Object, error) {
	rec, err := r.repo.Patch(ctx, tenantID, id, labels, externalRef)
	if err != nil {
		return nil, err
	}

	// Write-through: cache the updated result
	cacheKey := fmt.Sprintf("obj:%s:%s", tenantID, id.String())
	_ = r.cache.Set(ctx, cacheKey, rec, r.ttl)

	// If external_ref changed, update external_ref cache too
	if externalRef != nil {
		extKey := fmt.Sprintf("obj:ext:%s:%s", tenantID, *externalRef)
		_ = r.cache.Set(ctx, extKey, rec, r.ttl)
	}

	return rec, nil
}

// List delegates to underlying repo (no caching for list operations)
func (r *CachedObjectsRepo) List(ctx context.Context, tenantID string, filter domain.ListObjectsFilter, limit int, cursor string) ([]domain.Object, string, error) {
	return r.repo.List(ctx, tenantID, filter, limit, cursor)
}

// ListExpiredPending delegates to underlying repo
func (r *CachedObjectsRepo) ListExpiredPending(ctx context.Context, cutoff time.Time, limit int) ([]domain.Object, error) {
	return r.repo.ListExpiredPending(ctx, cutoff, limit)
}

// CacheStats returns cache statistics
func (r *CachedObjectsRepo) CacheStats() cache.CacheStats {
	return r.cache.Stats()
}
