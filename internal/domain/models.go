package domain

import (
	"time"

	"github.com/google/uuid"
)

type AppMetadata struct {
	Version   string
	Commit    string
	BuildTime string
}

type ObjectStatus string

const (
	ObjectPending     ObjectStatus = "pending"
	ObjectUploading   ObjectStatus = "uploading"
	ObjectUploaded    ObjectStatus = "uploaded"
	ObjectComplete    ObjectStatus = "complete"
	ObjectAborted     ObjectStatus = "aborted"
	ObjectError       ObjectStatus = "error"
	ObjectDeleted     ObjectStatus = "deleted"
	ObjectSoftDeleted ObjectStatus = "soft_deleted"
	ObjectHardDeleted ObjectStatus = "hard_deleted"
)

const (
	// CategorySeparator is the character used to separate segments in category slugs.
	CategorySeparator = "/"
)

// Category represents a tenant-scoped object category.
// Category slugs are user-defined and managed via the /categories API.
//
// If slug contains slashes, check if parent categories exist
type Category struct {
	ID       uuid.UUID
	TenantID string
	// Slug is unique per tenant. Max 63 characters.
	Slug string
	// Name is unique per tenant. Max 64 characters.
	Name string
	// Description details. Max 128 characters.
	Description *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Tenant is a registered tenant in the system.
// Tenants are first-class entities that must be provisioned before they can
// store objects. The tenant_id is the canonical identifier used throughout
// all other tables, headers, and RLS policies.
type Tenant struct {
	ID uuid.UUID
	// TenantID is the unique identifier for the tenant.
	TenantID string
	// DisplayName is a human-readable name. Max 64 characters.
	DisplayName *string
	// Labels are arbitrary key-value metadata. Max 10 keys, max 4 KiB total.
	Labels map[string]string
	// Tags are an unordered set of strings for categorical filtering.
	Tags      []string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ListTenantsFilter holds optional filter criteria for listing tenants.
type ListTenantsFilter struct {
	// LabelSelector matches tenants whose labels contain all provided key-value pairs.
	LabelSelector map[string]string
	// TagSelector matches tenants that have at least one of the provided tags.
	TagSelector []string
	// Search matches tenants by ID or DisplayName.
	Search *string
	// Sorting
	SortBy    string
	SortOrder string

	Limit  int
	Cursor string
}

type Object struct {
	ID              uuid.UUID
	TenantID        string
	ObjectKey       string
	Bucket          string
	ContentType     string
	SizeBytes       int64
	ChecksumSHA256  *string
	Status          ObjectStatus
	Labels          map[string]string
	ExternalRef     *string
	StoredETag      *string
	StoredSizeBytes *int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ExpiresAt       *time.Time
	CompletedAt     *time.Time
	DeletedAt       *time.Time
	// Category is the slug of the tenant-scoped category this object belongs to.
	// The value is validated at create time against the object_categories table.
	// Default: None (must be validated against object_categories table)
	Category string
	// Subpath is an optional forward-slash-separated path within the category
	// (e.g. "2024/01"). Reserved for future use; not exposed in v1 API.
	Subpath *string
}

type MultipartStatus string

const (
	MultipartInitiated MultipartStatus = "initiated"
	MultipartCompleted MultipartStatus = "completed"
	MultipartAborted   MultipartStatus = "aborted"
	MultipartExpired   MultipartStatus = "expired"
	MultipartUploaded  MultipartStatus = "uploaded"
)

type Multipart struct {
	ID          uuid.UUID
	TenantID    string
	ObjectID    uuid.UUID
	UploadID    string
	Bucket      string
	ObjectKey   string
	ContentType string
	PartSize    int64
	Status      MultipartStatus
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ExpiresAt   time.Time
	CompletedAt *time.Time
	DeletedAt   *time.Time
}

type MultipartPart struct {
	MultipartID uuid.UUID
	PartNumber  int
	ETag        *string
	SizeBytes   *int64
	CreatedAt   time.Time
}

type IdempotencyRecord struct {
	TenantID     string
	Key          string
	RequestPath  string
	RequestHash  string
	ResponseCode int
	ResponseBody []byte
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

// ListObjectsFilter holds optional filters for listing objects.
type ListObjectsFilter struct {
	Status        *ObjectStatus
	ExternalRef   *string
	Search        *string
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
	// Category filters objects to a specific category slug. If nil, all categories are returned.
	Category *string
	// Recursive enables prefix matching for category slugs (e.g. 'docs' matches 'docs/invoices').
	Recursive bool
	// KeyPrefix is an optional prefix filter within tenant/category scope.
	// The server validates that it cannot escape the tenant+category boundary.
	KeyPrefix *string

	// Sorting
	SortBy    string // e.g. "created_at"
	SortOrder string // "asc" or "desc"

	Limit  int
	Cursor string
}

// ListCategoriesFilter holds pagination state for listing categories.
type ListCategoriesFilter struct {
	Limit  int
	Cursor string
	// Search matches categories by slug or name.
	Search *string
	// Sorting
	SortBy    string
	SortOrder string
}

type CategoryStats struct {
	TotalCount       int64
	TotalSize        int64
	SoftDeletedCount int64
}

// BulkPatchItem carries partial updates for a single object.
type BulkPatchItem struct {
	ID          uuid.UUID
	Labels      map[string]string
	ExternalRef *string
}

type CreateObjectResponse struct {
	ID       uuid.UUID
	Key      string
	Bucket   string
	Upload   Presigned
	Category string
}

type MultipartInitResponse struct {
	ObjectID  uuid.UUID
	ObjectKey string
	UploadID  string
	Bucket    string
	PartSize  int64
	ExpiresAt time.Time
	Category  string
}

type MultipartInit struct {
	UploadID  string
	Key       string
	Bucket    string
	ExpiresAt time.Time
}

type HeadRecord struct {
	Key          string
	ETag         string
	SizeBytes    int64
	ContentType  string
	LastModified time.Time
	Metadata     map[string]string
}

type S3PingResult struct {
	Status     string
	HttpStatus int
	Message    string
	Bucket     string
	Region     string
}

type CompletePart struct {
	PartNumber int32
	ETag       string
}

type SignPartResponse struct {
	PartNumber int32
	Upload     Presigned
}

type Presigned struct {
	URL       string            `json:"url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers,omitempty"`
	ExpiresAt time.Time         `json:"expires_at"`
}

// CreateObjectRequest carries validated input for ObjectsService.CreateSingle and BulkCreate.
type CreateObjectRequest struct {
	TenantID    string
	Category    string
	ContentType string
	SizeBytes   int64
	Labels      map[string]string
	ExternalRef *string
}

// SignUploadItem carries input for ObjectsService.BulkSignUploads.
type SignUploadItem struct {
	ObjectID  uuid.UUID
	UploadTTL int
}
