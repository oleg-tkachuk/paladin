package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/cache"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/metrics"
	"go.uber.org/zap"

	"github.com/google/uuid"
)

// CachedObjectsRepo wraps ObjectsRepo with an LRU cache
type CachedObjectsRepo struct {
	repo  domain.ObjectsRepository
	cache *cache.Cache[string, *domain.Object]
	log   *zap.Logger
	ttl   time.Duration
}

// NewCachedObjectsRepo creates a cached repository wrapper
func NewCachedObjectsRepo(repo domain.ObjectsRepository, cacheSize int, ttl time.Duration, log *zap.Logger) *CachedObjectsRepo {
	return &CachedObjectsRepo{
		repo:  repo,
		cache: cache.NewCache[string, *domain.Object](cacheSize, ttl),
		log:   log,
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
	if err := r.cache.Set(ctx, cacheKey, rec, r.ttl); err != nil {
		r.log.Error("cache set failed", zap.Error(err))
	}

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
		if err := r.cache.Set(ctx, cacheKey, rec, r.ttl); err != nil {
			r.log.Error("cache set failed", zap.Error(err))
		}
		// Also cache by ID
		idKey := fmt.Sprintf("obj:%s:%s", tenantID, rec.ID.String())
		if err := r.cache.Set(ctx, idKey, rec, r.ttl); err != nil {
			r.log.Error("cache set failed", zap.Error(err))
		}
	}

	return rec, nil
}

// GetByKey retrieves an object by bucket+key with caching
func (r *CachedObjectsRepo) GetByKey(ctx context.Context, tenantID, bucket, key string) (*domain.Object, error) {
	cacheKey := fmt.Sprintf("obj:key:%s:%s:%s", tenantID, bucket, key)

	// Try cache first
	if rec, ok := r.cache.Get(ctx, cacheKey); ok {
		metrics.RecordCacheOp(ctx, "get", "hit")

		return rec, nil
	}

	metrics.RecordCacheOp(ctx, "get", "miss")

	// Cache miss - fetch from database
	rec, err := r.repo.GetByKey(ctx, tenantID, bucket, key)
	if err != nil {
		return nil, err
	}

	// Update cache (if found)
	if rec != nil {
		if err := r.cache.Set(ctx, cacheKey, rec, r.ttl); err != nil {
			r.log.Error("cache set failed", zap.Error(err))
		}
		// Also cache by ID
		idKey := fmt.Sprintf("obj:%s:%s", tenantID, rec.ID.String())
		if err := r.cache.Set(ctx, idKey, rec, r.ttl); err != nil {
			r.log.Error("cache set failed", zap.Error(err))
		}
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
	if err := r.cache.Set(ctx, cacheKey, &rec, r.ttl); err != nil {
		r.log.Error("cache set failed", zap.Error(err))
	}

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
			if err := r.cache.Set(ctx, cacheKey, rec, r.ttl); err != nil {
				r.log.Error("cache set failed", zap.Error(err))
			}
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
	if err := r.cache.Delete(ctx, cacheKey); err != nil {
		r.log.Error("cache delete failed", zap.Error(err))
	}

	return updated, nil
}

func (r *CachedObjectsRepo) MarkHardDeleted(ctx context.Context, tenantID string, id uuid.UUID) (bool, error) {
	updated, err := r.repo.MarkHardDeleted(ctx, tenantID, id)
	if err != nil {
		return false, err
	}

	// Invalidate cache
	cacheKey := fmt.Sprintf("obj:%s:%s", tenantID, id.String())
	if err := r.cache.Delete(ctx, cacheKey); err != nil {
		r.log.Error("cache delete failed", zap.Error(err))
	}

	return updated, nil
}

func (r *CachedObjectsRepo) Restore(ctx context.Context, tenantID string, id uuid.UUID) (bool, error) {
	updated, err := r.repo.Restore(ctx, tenantID, id)
	if err != nil {
		return false, err
	}

	// Invalidate cache
	cacheKey := fmt.Sprintf("obj:%s:%s", tenantID, id.String())
	if err := r.cache.Delete(ctx, cacheKey); err != nil {
		r.log.Error("cache delete failed", zap.Error(err))
	}

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
	if err := r.cache.Delete(ctx, cacheKey); err != nil {
		r.log.Error("cache delete failed", zap.Error(err))
	}

	return updated, nil
}

func (r *CachedObjectsRepo) UpdateStatus(ctx context.Context, tenantID string, id uuid.UUID, status string) (bool, error) {
	updated, err := r.repo.UpdateStatus(ctx, tenantID, id, status)
	if err != nil {
		return false, err
	}

	// Invalidate cache
	cacheKey := fmt.Sprintf("obj:%s:%s", tenantID, id.String())
	if err := r.cache.Delete(ctx, cacheKey); err != nil {
		r.log.Error("cache delete failed", zap.Error(err))
	}

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
	if err := r.cache.Set(ctx, cacheKey, rec, r.ttl); err != nil {
		r.log.Error("cache set failed", zap.Error(err))
	}

	// If external_ref changed, update external_ref cache too
	if externalRef != nil {
		extKey := fmt.Sprintf("obj:ext:%s:%s", tenantID, *externalRef)
		if err := r.cache.Set(ctx, extKey, rec, r.ttl); err != nil {
			r.log.Error("cache set failed", zap.Error(err))
		}
	}

	return rec, nil
}

// List delegates to underlying repo (no caching for list operations)
func (r *CachedObjectsRepo) List(ctx context.Context, tenantID string, filter domain.ListObjectsFilter) ([]domain.Object, string, int64, error) {
	return r.repo.List(ctx, tenantID, filter)
}

// ListExpiredPending delegates to underlying repo
func (r *CachedObjectsRepo) ListExpiredPending(ctx context.Context, cutoff time.Time, limit int) ([]domain.Object, error) {
	return r.repo.ListExpiredPending(ctx, cutoff, limit)
}

// Delete removes an object and invalidates cache
func (r *CachedObjectsRepo) Delete(ctx context.Context, tenantID string, id uuid.UUID) (bool, error) {
	updated, err := r.repo.Delete(ctx, tenantID, id)
	if err != nil {
		return false, err
	}

	// Invalidate cache
	cacheKey := fmt.Sprintf("obj:%s:%s", tenantID, id.String())
	if err := r.cache.Delete(ctx, cacheKey); err != nil {
		r.log.Error("cache delete failed", zap.Error(err))
	}

	return updated, nil
}

// CacheStats returns cache statistics
func (r *CachedObjectsRepo) CacheStats() cache.CacheStats {
	return r.cache.Stats()
}
func (r *CachedObjectsRepo) BulkCreate(ctx context.Context, objects []domain.Object) error {
	err := r.repo.BulkCreate(ctx, objects)
	if err != nil {
		return err
	}

	// Cache the newly created objects
	for _, rec := range objects {
		cacheKey := fmt.Sprintf("obj:%s:%s", rec.TenantID, rec.ID.String())
		if err := r.cache.Set(ctx, cacheKey, &rec, r.ttl); err != nil {
			r.log.Error("cache set failed", zap.Error(err))
		}
	}

	return nil
}

func (r *CachedObjectsRepo) BulkMarkSoftDeleted(ctx context.Context, tenantID string, ids []uuid.UUID) (int64, error) {
	rows, err := r.repo.BulkMarkSoftDeleted(ctx, tenantID, ids)
	if err != nil {
		return 0, err
	}

	// Invalidate cache for all affected IDs
	for _, id := range ids {
		cacheKey := fmt.Sprintf("obj:%s:%s", tenantID, id.String())
		if err := r.cache.Delete(ctx, cacheKey); err != nil {
			r.log.Error("cache delete failed", zap.Error(err))
		}
	}

	return rows, nil
}

func (r *CachedObjectsRepo) BulkRestore(ctx context.Context, tenantID string, ids []uuid.UUID) (int64, error) {
	rows, err := r.repo.BulkRestore(ctx, tenantID, ids)
	if err != nil {
		return 0, err
	}

	// Invalidate cache for all affected IDs
	for _, id := range ids {
		cacheKey := fmt.Sprintf("obj:%s:%s", tenantID, id.String())
		if err := r.cache.Delete(ctx, cacheKey); err != nil {
			r.log.Error("cache delete failed", zap.Error(err))
		}
	}

	return rows, nil
}

func (r *CachedObjectsRepo) BulkDelete(ctx context.Context, tenantID string, ids []uuid.UUID) (int64, error) {
	rows, err := r.repo.BulkDelete(ctx, tenantID, ids)
	if err != nil {
		return 0, err
	}

	// Invalidate cache for all affected IDs
	for _, id := range ids {
		cacheKey := fmt.Sprintf("obj:%s:%s", tenantID, id.String())
		if err := r.cache.Delete(ctx, cacheKey); err != nil {
			r.log.Error("cache delete failed", zap.Error(err))
		}
	}

	return rows, nil
}

func (r *CachedObjectsRepo) GetStats(ctx context.Context, tenantID string) (*domain.ObjectStats, error) {
	return r.repo.GetStats(ctx, tenantID)
}

func (r *CachedObjectsRepo) GetBucketStats(ctx context.Context, tenantID, bucket string) (int64, int64, error) {
	return r.repo.GetBucketStats(ctx, tenantID, bucket)
}

func (r *CachedObjectsRepo) BulkPatch(ctx context.Context, tenantID string, items []domain.BulkPatchItem) (int64, error) {
	rows, err := r.repo.BulkPatch(ctx, tenantID, items)
	if err != nil {
		return 0, err
	}

	// Invalidate cache for all affected IDs
	for _, item := range items {
		cacheKey := fmt.Sprintf("obj:%s:%s", tenantID, item.ID.String())
		if err := r.cache.Delete(ctx, cacheKey); err != nil {
			r.log.Error("cache delete failed", zap.Error(err))
		}

		// Also invalidate by external_ref if we know it, or just let it expire.
		// Since we don't know the OLD external_ref here without fetching,
		// and the NEW one is in 'item.ExternalRef', we can at least invalidate the new one's cache key
		// in case it was pointing to something else (unlikely but safe).
		if item.ExternalRef != nil {
			extKey := fmt.Sprintf("obj:ext:%s:%s", tenantID, *item.ExternalRef)
			if err := r.cache.Delete(ctx, extKey); err != nil {
				r.log.Error("cache delete failed", zap.Error(err))
			}
		}
	}

	return rows, nil
}
