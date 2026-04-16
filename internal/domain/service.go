package domain

import (
	"context"

	"github.com/google/uuid"
)

// ObjectsService defines business operations on objects.
//
//nolint:interfacebloat
type ObjectsService interface {
	// CreateSingle creates a single-PUT object and returns a presigned upload URL.
	// category must be a slug of an existing tenant category.
	CreateSingle(ctx context.Context, tenantID string, category string, contentType string, sizeBytes int64, labels map[string]string, tags map[string]string, externalRef *string, uploadTTL int, idempotencyKey *string) (CreateObjectResponse, error)
	Get(ctx context.Context, tenantID string, id uuid.UUID) (*Object, error)
	GetMeta(ctx context.Context, tenantID string, id uuid.UUID) (*Object, error)
	GetByKey(ctx context.Context, tenantID, bucket, key string) (*Object, error)
	CompleteObject(ctx context.Context, tenantID string, id uuid.UUID, etag *string, sizeBytes *int64) (*Object, error)
	// CompleteObjectByKey resolves an upload intent by (bucket, key) and
	// promotes it into a completed object. Used by the handler layer which
	// receives the key from the client.
	CompleteObjectByKey(ctx context.Context, tenantID, bucket, key string, etag *string, sizeBytes *int64) (*Object, error)
	// Delete performs a soft delete
	Delete(ctx context.Context, tenantID string, id uuid.UUID) error
	// Restore brings back a soft-deleted object
	Restore(ctx context.Context, tenantID string, id uuid.UUID) error
	// Purge performs a hard delete (removes from storage)
	Purge(ctx context.Context, tenantID string, id uuid.UUID, idempotencyKey *string) error
	// Bulk operations
	BulkDelete(ctx context.Context, tenantID string, ids []uuid.UUID) (int64, error)
	BulkRestore(ctx context.Context, tenantID string, ids []uuid.UUID) (int64, error)
	BulkPurge(ctx context.Context, tenantID string, ids []uuid.UUID, idempotencyKey *string) (int64, error)
	BulkCreate(ctx context.Context, tenantID string, items []CreateObjectRequest, idempotencyKey *string) ([]CreateObjectResponse, error)
	BulkPatch(ctx context.Context, tenantID string, items []BulkPatchItem, idempotencyKey *string) (int64, error)
	BulkSignUploads(ctx context.Context, tenantID string, items []SignUploadItem) ([]CreateObjectResponse, error)
	BulkComplete(ctx context.Context, tenantID string, ids []uuid.UUID) ([]*Object, error)
	// UpdateStatus updates the status of an object (non-delete transitions only)
	UpdateStatus(ctx context.Context, tenantID string, id uuid.UUID, status string, idempotencyKey *string) error
	List(ctx context.Context, tenantID string, filter ListObjectsFilter) ([]Object, string, int64, error)
	PatchMeta(ctx context.Context, tenantID string, id uuid.UUID, labels map[string]string, tags map[string]string, externalRef *string) (*Object, error)
	SignUpload(ctx context.Context, tenantID string, id uuid.UUID, uploadTTL int) (Presigned, error)
	SignDownload(ctx context.Context, tenantID string, id uuid.UUID, downloadTTL int) (Presigned, error)
	CopyObject(ctx context.Context, tenantID, srcBucket, srcKey, dstBucket, dstKey string, metadata map[string]string) (*Object, error)
	MoveObject(ctx context.Context, tenantID, srcBucket, srcKey, dstBucket, dstKey string) (*Object, error)

	// InitiateMultipart starts a multipart upload.
	// category must be a slug of an existing tenant category.
	InitiateMultipart(ctx context.Context, tenantID string, category string, contentType string, sizeBytes int64, labels map[string]string, tags map[string]string, externalRef *string, uploadTTL int, idempotencyKey *string) (MultipartInitResponse, error)
	GetMultipart(ctx context.Context, tenantID string, uploadID string) (*Multipart, error)
	SignPart(ctx context.Context, tenantID string, uploadID string, partNumber int32) (Presigned, error)
	SignPartsBatch(ctx context.Context, tenantID string, uploadID string, partNumbers []int32) ([]SignPartResponse, error)
	CompleteMultipart(ctx context.Context, tenantID string, uploadID string, parts []CompletePart) (*Object, error)
	AbortMultipart(ctx context.Context, tenantID string, uploadID string) error
	ListParts(ctx context.Context, tenantID string, uploadID string) ([]MultipartPart, error)
	GetStats(ctx context.Context, tenantID string) (*ObjectStats, error)
	GetBucketStats(ctx context.Context, tenantID, bucket string) (int64, int64, error)
}

// SystemService defines operations for system-level information and management.
type SystemService interface {
	GetConfig(ctx context.Context) (SystemConfig, error)
}

// Operation timeouts
// (removed Action type and constants - moved to constants.go)

// SystemConfig represents the sanitized system configuration for administrative display.
type SystemConfig struct {
	App struct {
		Name string
		Env  string
	}
	Server struct {
		Name string
		Mode string
		HTTP struct {
			Addr               string
			CORSAllowedOrigins []string
			ReadTimeout        string
			WriteTimeout       string
			RequestIDHeader    string
		}
	}
	Datastores struct {
		Postgres struct {
			Host    string
			Port    string
			User    string
			Dbname  string
			SslMode string
		}
		S3 struct {
			Bucket         string
			Endpoint       string
			PublicEndpoint string
			ForcePathStyle bool
			PresignTTL     string
			PartSize       string
			SSEType        string
		}
	}
	Policy struct {
		MaxObjectSize       string
		MaxMultipartSize    string
		MinPartSize         string
		MaxPartSize         string
		PresignPutTTL       string
		PresignGetTTL       string
		AllowedContentTypes []string
	}
	Auth struct {
		Enabled bool
	}
	Security struct {
		TrustTenantIDFromRequest bool
		RejectTenantMismatch     bool
		EnableRLS                bool
	}
	Housekeeping struct {
		EnableReaper bool
		PendingTTL   string
		MultipartTTL string
		GCInterval   string
	}
	RateLimit struct {
		RequestsPerSecond float32
		Burst             int
		MaxTenants        int
	}
	Cache struct {
		Enabled bool
		MaxSize int
		TTL     string
	}
	Timeouts struct {
		FastOperation    string
		DefaultOperation string
		S3Operation      string
		LongOperation    string
	}
	Idempotency struct {
		Enabled bool
		TTL     string
	}
	OTel struct {
		Enabled  bool
		Endpoint string
		Protocol string
		Insecure bool
	}
}

type Policy interface {
	Authorize(ctx context.Context, tenantID string, action Action) error
	Validate(contentType string, sizeBytes int64) error
}
