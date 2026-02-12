package domain

import (
	"context"

	"github.com/google/uuid"
)

type ObjectsService interface {
	CreateSingle(ctx context.Context, tenantID string, contentType string, sizeBytes int64, labels map[string]string, externalRef *string, uploadTTL int, idempotencyKey *string) (CreateObjectResponse, error)
	Get(ctx context.Context, tenantID string, id uuid.UUID) (*Object, error)
	GetMeta(ctx context.Context, tenantID string, id uuid.UUID) (*Object, error)
	CompleteObject(ctx context.Context, tenantID string, id uuid.UUID, etag *string, sizeBytes *int64) (*Object, error)
	HardDelete(ctx context.Context, tenantID string, id uuid.UUID, idempotencyKey *string) error
	// UpdateStatus updates the status of an object (e.g. for soft deletion)
	UpdateStatus(ctx context.Context, tenantID string, id uuid.UUID, status string, idempotencyKey *string) error
	List(ctx context.Context, tenantID string, filter ListObjectsFilter, limit int, cursor string) ([]Object, string, error)
	PatchMeta(ctx context.Context, tenantID string, id uuid.UUID, labels map[string]string, externalRef *string) (*Object, error)
	SignUpload(ctx context.Context, tenantID string, id uuid.UUID, uploadTTL int) (Presigned, error)
	SignDownload(ctx context.Context, tenantID string, id uuid.UUID, downloadTTL int) (Presigned, error)

	InitiateMultipart(ctx context.Context, tenantID string, contentType string, sizeBytes int64, labels map[string]string, externalRef *string, uploadTTL int, idempotencyKey *string) (MultipartInitResponse, error)
	GetMultipart(ctx context.Context, tenantID string, uploadID string) (*Multipart, error)
	SignPart(ctx context.Context, tenantID string, uploadID string, partNumber int32) (Presigned, error)
	SignPartsBatch(ctx context.Context, tenantID string, uploadID string, partNumbers []int32) ([]SignPartResponse, error)
	CompleteMultipart(ctx context.Context, tenantID string, uploadID string, parts []CompletePart) (*Object, error)
	AbortMultipart(ctx context.Context, tenantID string, uploadID string) error
}

// Operation timeouts
type Action string

const (
	ActionCreate Action = "create"
	ActionRead   Action = "read"
	ActionUpdate Action = "update"
	ActionDelete Action = "delete"
)

type Policy interface {
	Authorize(ctx context.Context, tenantID string, action Action) error
	Validate(contentType string, sizeBytes int64) error
}
