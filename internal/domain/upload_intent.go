package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// UploadIntent represents a server-generated upload intent — i.e. the state
// that exists after UploadObject has handed out a presigned URL but before
// the client has reported success via CompleteObject.
//
// Intents are ephemeral: on CompleteObject (S3 HEAD succeeded) the intent is
// consumed and a corresponding row is created in the `objects` table with
// status=ObjectComplete, transactionally. If the client never completes, the
// intent expires and the Reaper removes it. The `objects` table therefore
// never contains pending/failed uploads.
type UploadIntent struct {
	ID             uuid.UUID
	TenantID       string
	Bucket         string
	ObjectKey      string
	Category       string
	Subpath        *string
	ContentType    string
	SizeBytes      int64
	Labels         map[string]string
	Tags           map[string]string
	ExternalRef    *string
	IdempotencyKey *string
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

// UploadIntentsRepository manages the lifecycle of pending upload intents.
type UploadIntentsRepository interface {
	// Create persists a new upload intent. Returns a conflict error if the
	// (tenant_id, bucket, object_key) tuple already exists.
	Create(ctx context.Context, rec UploadIntent) error
	// Get retrieves an intent by its server-generated ID. Returns ErrNotFound
	// if the intent has already been consumed or expired.
	Get(ctx context.Context, tenantID string, id uuid.UUID) (*UploadIntent, error)
	// GetByKey retrieves an intent by its object key. Used by CompleteObject
	// handlers which receive (bucket, key) rather than the intent ID.
	GetByKey(ctx context.Context, tenantID, bucket, objectKey string) (*UploadIntent, error)
	// GetByIdempotencyKey returns the intent associated with a client-supplied
	// idempotency key, if any. Returns ErrNotFound when no such intent exists.
	GetByIdempotencyKey(ctx context.Context, tenantID, idempotencyKey string) (*UploadIntent, error)
	// Delete removes an intent, typically after the object has been promoted
	// into the `objects` table inside the same transaction.
	Delete(ctx context.Context, tenantID string, id uuid.UUID) (bool, error)
	// DeleteExpired removes intents whose expires_at < cutoff globally (Reaper).
	// Returns the number of rows removed.
	DeleteExpired(ctx context.Context, cutoff time.Time, limit int) (int64, error)
}
