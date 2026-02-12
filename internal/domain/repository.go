package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type ObjectsRepository interface {
	Create(ctx context.Context, rec Object) error
	Get(ctx context.Context, tenantID string, id uuid.UUID) (*Object, error)
	GetByExternalRef(ctx context.Context, tenantID string, externalRef string) (*Object, error)
	MarkComplete(ctx context.Context, tenantID string, id uuid.UUID, etag string, sizeBytes int64) (bool, error)
	MarkSoftDeleted(ctx context.Context, tenantID string, id uuid.UUID) (bool, error)
	MarkHardDeleted(ctx context.Context, tenantID string, id uuid.UUID) (bool, error)
	MarkDeleted(ctx context.Context, tenantID string, id uuid.UUID) (bool, error)
	List(ctx context.Context, tenantID string, filter ListObjectsFilter, limit int, cursor string) ([]Object, string, error)
	Patch(ctx context.Context, tenantID string, id uuid.UUID, labels map[string]string, externalRef *string) (*Object, error)
	ListExpiredPending(ctx context.Context, cutoff time.Time, limit int) ([]Object, error)
}

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

type IdempotencyRepository interface {
	Get(ctx context.Context, tenantID string, key string) (*IdempotencyRecord, error)
	Save(ctx context.Context, rec IdempotencyRecord) error
	Delete(ctx context.Context, tenantID string, key string) error
}
