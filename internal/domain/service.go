package domain

import (
	"context"

	"github.com/google/uuid"
)

// ObjectsService defines business operations on objects.
type ObjectsService interface {
	// CreateSingle creates a single-PUT object and returns a presigned upload URL.
	// category must be a slug of an existing tenant category.
	CreateSingle(ctx context.Context, tenantID string, category string, contentType string, sizeBytes int64, labels map[string]string, externalRef *string, uploadTTL int, idempotencyKey *string) (CreateObjectResponse, error)
	Get(ctx context.Context, tenantID string, id uuid.UUID) (*Object, error)
	GetMeta(ctx context.Context, tenantID string, id uuid.UUID) (*Object, error)
	CompleteObject(ctx context.Context, tenantID string, id uuid.UUID, etag *string, sizeBytes *int64) (*Object, error)
	// Delete performs a soft delete
	Delete(ctx context.Context, tenantID string, id uuid.UUID) error
	// Purge performs a hard delete (removes from storage)
	Purge(ctx context.Context, tenantID string, id uuid.UUID, idempotencyKey *string) error
	// UpdateStatus updates the status of an object (non-delete transitions only)
	UpdateStatus(ctx context.Context, tenantID string, id uuid.UUID, status string, idempotencyKey *string) error
	List(ctx context.Context, tenantID string, filter ListObjectsFilter, limit int, cursor string) ([]Object, string, error)
	PatchMeta(ctx context.Context, tenantID string, id uuid.UUID, labels map[string]string, externalRef *string) (*Object, error)
	SignUpload(ctx context.Context, tenantID string, id uuid.UUID, uploadTTL int) (Presigned, error)
	SignDownload(ctx context.Context, tenantID string, id uuid.UUID, downloadTTL int) (Presigned, error)

	// InitiateMultipart starts a multipart upload.
	// category must be a slug of an existing tenant category.
	InitiateMultipart(ctx context.Context, tenantID string, category string, contentType string, sizeBytes int64, labels map[string]string, externalRef *string, uploadTTL int, idempotencyKey *string) (MultipartInitResponse, error)
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
