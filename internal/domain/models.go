package domain

import (
	"time"

	"github.com/google/uuid"
)

type ObjectStatus string

const (
	ObjectPending     ObjectStatus = "pending"
	ObjectUploading   ObjectStatus = "uploading"
	ObjectUploaded    ObjectStatus = "uploaded"
	ObjectComplete    ObjectStatus = "complete"
	ObjectAborted     ObjectStatus = "aborted"
	ObjectDeleted     ObjectStatus = "deleted"
	ObjectSoftDeleted ObjectStatus = "soft_deleted"
	ObjectHardDeleted ObjectStatus = "hard_deleted"
)

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

type ListObjectsFilter struct {
	Status        *ObjectStatus
	ExternalRef   *string
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
}

type CreateObjectResponse struct {
	ID     uuid.UUID
	Key    string
	Bucket string
	Upload Presigned
}

type MultipartInitResponse struct {
	ObjectID  uuid.UUID
	ObjectKey string
	UploadID  string
	Bucket    string
	PartSize  int64
	ExpiresAt time.Time
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
