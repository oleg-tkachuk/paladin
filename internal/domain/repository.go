package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// UnitOfWork groups repositories for cross-entity transactional consistency.
type UnitOfWork interface {
	Objects() ObjectsRepository
	Multipart() MultipartRepository
	Idempotency() IdempotencyRepository
	AuditLogs() AuditLogRepository
	Categories() CategoryRepository
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// UoWFactory describes how to start a new UnitOfWork.
type UoWFactory interface {
	Begin(ctx context.Context) (UnitOfWork, error)
}

// CategoryRepository manages the lifecycle of tenant-scoped object categories.
type CategoryRepository interface {
	// Create persists a new category. Returns error on duplicate slug.
	Create(ctx context.Context, rec Category) error
	// Get retrieves a category by tenant + slug. Returns ErrNotFound if absent.
	Get(ctx context.Context, tenantID, slug string) (*Category, error)
	// List returns categories for a tenant, paginated by cursor (created_at).
	List(ctx context.Context, tenantID string, limit int, cursor string) ([]Category, string, int64, error)
	// Delete removes a category. Returns (false, nil) if it did not exist.
	Delete(ctx context.Context, tenantID, slug string) (bool, error)
	// Exists returns true if the slug is registered for the tenant.
	Exists(ctx context.Context, tenantID, slug string) (bool, error)
	// CategoryID returns the count of objects in a category.
	ObjectCount(ctx context.Context, tenantID, slug string) (int64, error)
	// GetStats returns detailed statistics for a category.
	GetStats(ctx context.Context, tenantID, slug string) (*CategoryStats, error)
	// ListTenants returns a list of all distinct tenant IDs, paginated.
	ListTenants(ctx context.Context, limit int, cursor string) ([]string, string, int64, error)
}

// TenantRepository manages the lifecycle of registered tenants.
type TenantRepository interface {
	// Create upserts the tenant record. Returns the persisted tenant.
	// Safe to call multiple times with the same tenant_id (idempotent).
	Create(ctx context.Context, rec Tenant) (*Tenant, error)
	// Get retrieves a tenant by its tenant_id. Returns ErrNotFound if absent.
	Get(ctx context.Context, tenantID string) (*Tenant, error)
	// Delete removes the tenant record. Returns (false, nil) if it did not exist.
	Delete(ctx context.Context, tenantID string) (bool, error)
	// HasActiveObjects returns true when the tenant has any objects that
	// have not reached the hard_deleted status.
	HasActiveObjects(ctx context.Context, tenantID string) (bool, error)
	// UpdateMetadata performs a partial-update of a tenant's labels and tags.
	// Labels are merged (provided keys overwrite; null values remove the key).
	// Tags are replaced in full.
	UpdateMetadata(ctx context.Context, tenantID string, labelsPatch map[string]interface{}, tags []string) (*Tenant, error)
	// List returns tenants matching filter, cursor-paginated (created_at DESC).
	List(ctx context.Context, filter TenantFilter, limit int, cursor string) ([]Tenant, string, int64, error)
}

// CategoryService defines the business logic for managing categories.
type CategoryService interface {
	Create(ctx context.Context, tenantID, slug, name string, description *string) (*Category, error)
	Get(ctx context.Context, tenantID, slug string) (*Category, error)
	List(ctx context.Context, tenantID string, limit int, cursor string) ([]Category, string, int64, error)
	// Delete returns ErrConflict if any active objects still reference the category.
	Delete(ctx context.Context, tenantID, slug string) error
	// GetStats returns detailed statistics for a category.
	GetStats(ctx context.Context, tenantID, slug string) (*CategoryStats, error)
	// ListTenants returns a list of available tenants in the system.
	ListTenants(ctx context.Context, limit int, cursor string) ([]string, string, int64, error)
}

// TenantService defines business operations for managing tenants.
type TenantService interface {
	// Create provisions a new tenant. Re-creating the same tenant_id is
	// idempotent and returns the existing record.
	Create(ctx context.Context, tenantID string, displayName *string, labels map[string]string, tags []string) (*Tenant, error)
	// Get retrieves a tenant. Returns ErrNotFound when absent.
	Get(ctx context.Context, tenantID string) (*Tenant, error)
	// Delete removes a tenant. Returns ErrConflict when the tenant still has
	// active (non-hard-deleted) objects; callers must purge all data first.
	Delete(ctx context.Context, tenantID string) error
	// PatchMetadata performs a partial-update of labels (merge) and a full
	// replacement of tags. Returns ErrNotFound if the tenant does not exist.
	PatchMetadata(ctx context.Context, tenantID string, labelsPatch map[string]interface{}, tags []string) (*Tenant, error)
	// List returns tenants matching filter, cursor-paginated.
	List(ctx context.Context, filter TenantFilter, limit int, cursor string) ([]Tenant, string, int64, error)
}

// CreateCategoryRequest carries validated input for CategoryService.Create.
type CreateCategoryRequest struct {
	TenantID    string
	Slug        string
	Name        string
	Description *string
}

// ListCategoriesFilter holds pagination state for listing categories.
type ListCategoriesFilter struct {
	Limit  int
	Cursor string
}

// ObjectsRepository defines persistent object operations.
type ObjectsRepository interface {
	Create(ctx context.Context, rec Object) error
	Get(ctx context.Context, tenantID string, id uuid.UUID) (*Object, error)
	GetByExternalRef(ctx context.Context, tenantID string, externalRef string) (*Object, error)
	MarkComplete(ctx context.Context, tenantID string, id uuid.UUID, etag string, sizeBytes int64) (bool, error)
	MarkSoftDeleted(ctx context.Context, tenantID string, id uuid.UUID) (bool, error)
	MarkHardDeleted(ctx context.Context, tenantID string, id uuid.UUID) (bool, error)
	UpdateStatus(ctx context.Context, tenantID string, id uuid.UUID, status string) (bool, error)
	Restore(ctx context.Context, tenantID string, id uuid.UUID) (bool, error)
	MarkDeleted(ctx context.Context, tenantID string, id uuid.UUID) (bool, error)
	Delete(ctx context.Context, tenantID string, id uuid.UUID) (bool, error)
	BulkMarkSoftDeleted(ctx context.Context, tenantID string, ids []uuid.UUID) (int64, error)
	BulkRestore(ctx context.Context, tenantID string, ids []uuid.UUID) (int64, error)
	BulkDelete(ctx context.Context, tenantID string, ids []uuid.UUID) (int64, error)
	BulkCreate(ctx context.Context, objects []Object) error
	BulkPatch(ctx context.Context, tenantID string, items []BulkPatchItem) (int64, error)
	List(ctx context.Context, tenantID string, filter ListObjectsFilter, limit int, cursor string) ([]Object, string, int64, error)

	Patch(ctx context.Context, tenantID string, id uuid.UUID, labels map[string]string, externalRef *string) (*Object, error)
	ListExpiredPending(ctx context.Context, cutoff time.Time, limit int) ([]Object, error)
	GetStats(ctx context.Context, tenantID string) (*ObjectStats, error)
}

type ObjectStats struct {
	TotalCount       int64
	TotalSize        int64
	PendingCount     int64
	UploadingCount   int64
	UploadedCount    int64
	CompleteCount    int64
	SoftDeletedCount int64
}

// MultipartRepository manages multipart upload state.
type MultipartRepository interface {
	Create(ctx context.Context, rec Multipart) error
	GetByUploadID(ctx context.Context, tenantID string, uploadID string) (*Multipart, error)
	UpsertPartETag(ctx context.Context, multipartID uuid.UUID, partNumber int, etag string, sizeBytes *int64) error
	MarkCompleted(ctx context.Context, tenantID string, uploadID string) error
	MarkAborted(ctx context.Context, tenantID string, uploadID string) error
	CompleteUpload(ctx context.Context, tenantID string, uploadID string, objectID uuid.UUID) error
	ListExpired(ctx context.Context, limit int) ([]Multipart, error)
	ListParts(ctx context.Context, multipartID uuid.UUID) ([]MultipartPart, error)
}

// IdempotencyRepository provides safe retry semantics for mutating operations.
type IdempotencyRepository interface {
	Get(ctx context.Context, tenantID string, key string) (*IdempotencyRecord, error)
	Save(ctx context.Context, rec IdempotencyRecord) error
	Delete(ctx context.Context, tenantID string, key string) error
}
