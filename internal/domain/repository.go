package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// CategoryRepository manages the lifecycle of tenant-scoped object categories.
type CategoryRepository interface {
	// Create persists a new category. Returns error on duplicate slug.
	Create(ctx context.Context, rec Category) error
	// Get retrieves a category by tenant + slug. Returns ErrNotFound if absent.
	Get(ctx context.Context, tenantID, slug string) (*Category, error)
	// List returns categories for a tenant, paginated by cursor (created_at).
	List(ctx context.Context, tenantID string, limit int, cursor string) ([]Category, string, error)
	// Delete removes a category. Returns (false, nil) if it did not exist.
	Delete(ctx context.Context, tenantID, slug string) (bool, error)
	// Exists returns true if the slug is registered for the tenant.
	Exists(ctx context.Context, tenantID, slug string) (bool, error)
	// ObjectCount returns the number of non-hard-deleted objects in the category.
	// Used to guard deletion (returns 409 if count > 0).
	ObjectCount(ctx context.Context, tenantID, slug string) (int64, error)
	// ListTenants returns a list of all distinct tenant IDs that have created categories.
	ListTenants(ctx context.Context) ([]string, error)
}

// CategoryService defines the business logic for managing categories.
type CategoryService interface {
	Create(ctx context.Context, tenantID, slug, name string, description *string) (*Category, error)
	Get(ctx context.Context, tenantID, slug string) (*Category, error)
	List(ctx context.Context, tenantID string, limit int, cursor string) ([]Category, string, error)
	// Delete returns ErrConflict if any active objects still reference the category.
	Delete(ctx context.Context, tenantID, slug string) error
	// ListTenants returns a list of available tenants in the system.
	ListTenants(ctx context.Context) ([]string, error)
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
	List(ctx context.Context, tenantID string, filter ListObjectsFilter, limit int, cursor string) ([]Object, string, error)

	Patch(ctx context.Context, tenantID string, id uuid.UUID, labels map[string]string, externalRef *string) (*Object, error)
	ListExpiredPending(ctx context.Context, cutoff time.Time, limit int) ([]Object, error)
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
