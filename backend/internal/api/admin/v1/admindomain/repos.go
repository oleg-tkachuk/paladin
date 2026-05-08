package admindomain

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound / ErrVersionMismatch are the canonical not-found / OCC errors
// for admin repositories. Handlers map these to connect.CodeNotFound and
// connect.CodeAborted respectively.
var (
	ErrNotFound        = errors.New("admin: resource not found")
	ErrVersionMismatch = errors.New("admin: resource_version mismatch")
	ErrConflict        = errors.New("admin: conflict")
)

// ─── Storage backend repository ─────────────────────────────────────────────

type BackendRepository interface {
	Upsert(ctx context.Context, b StorageBackend) error
	Get(ctx context.Context, backendID string) (StorageBackend, error)
	List(ctx context.Context, pageSize int32, afterID string) ([]StorageBackend, string, error)
	Update(ctx context.Context, b StorageBackend, expectedVersion int64, mask []string) error
	RotateCredentials(ctx context.Context, backendID, secretRef string) error
	Delete(ctx context.Context, backendID string, expectedVersion int64, force bool) error
}

// ─── Bucket repository ──────────────────────────────────────────────────────

type BucketRepository interface {
	Create(ctx context.Context, b Bucket) error
	Get(ctx context.Context, backendID, bucketName string) (Bucket, error)
	List(ctx context.Context, args ListBucketsArgs) ([]Bucket, string, error)
	ListAccessible(ctx context.Context, tenantID uuid.UUID, pageSize int32, afterBackend, afterName string) ([]Bucket, string, error)
	UpdateBasic(ctx context.Context, b Bucket, expectedVersion int64, mask []string) error
	SetPolicy(ctx context.Context, backendID, bucketName, policy string, expectedVersion int64) error
	SetLifecycle(ctx context.Context, backendID, bucketName string, rules []LifecycleRule, expectedVersion int64) error
	SetObjectLock(ctx context.Context, backendID, bucketName string, lock ObjectLockConfig, expectedVersion int64) error
	SetVersioning(ctx context.Context, backendID, bucketName string, v BucketVersioning, expectedVersion int64) error
	SetReplication(ctx context.Context, backendID, bucketName string, r BucketReplication, expectedVersion int64) error
	SetConstraints(ctx context.Context, backendID, bucketName string, c BucketConstraints, expectedVersion int64) error
	Delete(ctx context.Context, backendID, bucketName string, expectedVersion int64) error

	// ─── outbox / reconciler ────────────────────────────────────────────
	// ListPendingProvisions returns up to `limit` rows that need the
	// backend's CreateBucket call. Includes 'pending' rows and 'failed'
	// rows that have not yet exhausted their retry budget (`maxAttempts`).
	ListPendingProvisions(ctx context.Context, maxAttempts, limit int32) ([]BucketProvisionRow, error)
	MarkProvisionReady(ctx context.Context, backendID, bucketName string) error
	// MarkProvisionFailed records an attempt error. terminal=true marks
	// the row 'failed' and stops the worker from retrying; terminal=false
	// (transient) keeps it 'pending' for the next tick.
	MarkProvisionFailed(ctx context.Context, backendID, bucketName string, terminal bool, errMsg string) error

	// ─── outbox / delete path ───────────────────────────────────────────
	// MarkDeleting flips a row into the deletion outbox. The handler calls
	// this; the actual physical delete (both S3 + DB row) is the worker's
	// job. expectedVersion=0 disables the OCC check.
	MarkDeleting(ctx context.Context, backendID, bucketName string, expectedVersion int64) error
	// ListPendingDeletions is the deletion-side companion of ListPending
	// Provisions: 'deleting' rows + 'deletion_failed' rows still within
	// the retry budget.
	ListPendingDeletions(ctx context.Context, maxAttempts, limit int32) ([]BucketProvisionRow, error)
	// MarkDeletionFailed records a failed delete attempt. terminal=true
	// freezes the row in 'deletion_failed'; terminal=false keeps it
	// 'deleting' for retry.
	MarkDeletionFailed(ctx context.Context, backendID, bucketName string, terminal bool, errMsg string) error
}

type ListBucketsArgs struct {
	BackendID    string // "" → all
	PageSize     int32
	AfterBackend string
	AfterName    string
}

// ─── Audit repository ───────────────────────────────────────────────────────

type AuditRepository interface {
	Insert(ctx context.Context, e AuditEntry) error
	Get(ctx context.Context, entryID uuid.UUID) (AuditEntry, error)
	List(ctx context.Context, args ListAuditArgs) ([]AuditEntry, string, error)
}

type ListAuditArgs struct {
	ActorSubject  string
	ActorTenantID uuid.UUID
	AfterAt       time.Time
	AfterID       uuid.UUID
	PageSize      int32
}

// ─── Quota repository ───────────────────────────────────────────────────────

type QuotaRepository interface {
	UpsertTenant(ctx context.Context, q Quota) error
	UpsertBucket(ctx context.Context, q Quota) error
	GetTenant(ctx context.Context, tenantID uuid.UUID) (Quota, error)
	GetBucket(ctx context.Context, backendID, bucketName string) (Quota, error)
	IncrementUsage(ctx context.Context, quotaID uuid.UUID, deltaBytes, deltaCount int64) error
	ResetDaily(ctx context.Context, quotaID uuid.UUID, at time.Time) error
}

// ─── Event subscription repository ──────────────────────────────────────────

type EventSubscriptionRepository interface {
	Create(ctx context.Context, s EventSubscription) error
	Get(ctx context.Context, id uuid.UUID) (EventSubscription, error)
	List(ctx context.Context, args ListEventSubscriptionsArgs) ([]EventSubscription, string, error)
	Update(ctx context.Context, s EventSubscription, expectedVersion int64, mask []string) error
	Delete(ctx context.Context, id uuid.UUID, expectedVersion int64) error
}

type ListEventSubscriptionsArgs struct {
	TenantID uuid.UUID
	PageSize int32
	AfterID  uuid.UUID
}
