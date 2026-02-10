package service

import (
	"context"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/storage/s3"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres"

	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

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

type ObjectsRepository interface {
	Create(ctx context.Context, rec postgres.ObjectRecord) error
	Get(ctx context.Context, tenantID string, id uuid.UUID) (*postgres.ObjectRecord, error)
	GetByExternalRef(ctx context.Context, tenantID string, externalRef string) (*postgres.ObjectRecord, error)
	MarkComplete(ctx context.Context, tenantID string, id uuid.UUID, etag string, sizeBytes int64) (bool, error)
	MarkSoftDeleted(ctx context.Context, tenantID string, id uuid.UUID) (bool, error)
	MarkHardDeleted(ctx context.Context, tenantID string, id uuid.UUID) (bool, error)
	MarkDeleted(ctx context.Context, tenantID string, id uuid.UUID) (bool, error)
	List(ctx context.Context, tenantID string, filter postgres.ListObjectsFilter, limit int, cursor string) ([]postgres.ObjectRecord, string, error)
	Patch(ctx context.Context, tenantID string, id uuid.UUID, labels map[string]string, externalRef *string) (*postgres.ObjectRecord, error)
	ListExpiredPending(ctx context.Context, cutoff time.Time, limit int) ([]postgres.ObjectRecord, error)
}

type MultipartRepository interface {
	Create(ctx context.Context, rec postgres.MultipartRecord) error
	GetByUploadID(ctx context.Context, tenantID string, uploadID string) (*postgres.MultipartRecord, error)
	UpsertPartETag(ctx context.Context, multipartID uuid.UUID, partNumber int, etag string, sizeBytes *int64) error
	MarkCompleted(ctx context.Context, tenantID string, uploadID string) error
	MarkAborted(ctx context.Context, tenantID string, uploadID string) error
	CompleteUpload(ctx context.Context, tenantID string, uploadID string, objectID uuid.UUID) error
	ListExpired(ctx context.Context, limit int) ([]postgres.MultipartRecord, error)
}

type S3Client interface {
	BucketName() string
	PresignTTLDuration() time.Duration
	PresignPutObject(ctx context.Context, key string, contentType string, sizeBytes int64, ttl time.Duration) (s3.Presigned, error)
	PresignGetObject(ctx context.Context, key string, ttl time.Duration) (s3.Presigned, error)
	PresignUploadPart(ctx context.Context, key, uploadID string, partNumber int32, ttl time.Duration) (s3.Presigned, error)
	CreateMultipartUpload(ctx context.Context, key string, contentType string) (s3.MultipartInit, error)
	CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []types.CompletedPart) error
	AbortMultipartUpload(ctx context.Context, key, uploadID string) error
	HeadObject(ctx context.Context, key string) (*s3.HeadRecord, error)
	DeleteObject(ctx context.Context, key string) error
}

type CreateObjectResponse struct {
	ID     uuid.UUID
	Key    string
	Bucket string
	Upload s3.Presigned
}

type MultipartInitResponse struct {
	ObjectID  uuid.UUID
	ObjectKey string
	UploadID  string
	Bucket    string
	PartSize  int64
	ExpiresAt time.Time
}

type CompletePart struct {
	PartNumber int32
	ETag       string
}

type SignPartResponse struct {
	PartNumber int32
	Upload     s3.Presigned
}

type ObjectsService interface {
	CreateSingle(ctx context.Context, tenantID string, contentType string, sizeBytes int64, labels map[string]string, externalRef *string, uploadTTL int, idempotencyKey *string) (CreateObjectResponse, error)
	Get(ctx context.Context, tenantID string, id openapi_types.UUID) (*postgres.ObjectRecord, error)
	GetMeta(ctx context.Context, tenantID string, id openapi_types.UUID) (*postgres.ObjectRecord, error)
	CompleteObject(ctx context.Context, tenantID string, id openapi_types.UUID, etag *string, sizeBytes *int64) (*postgres.ObjectRecord, error)
	HardDelete(ctx context.Context, tenantID string, id openapi_types.UUID, idempotencyKey *string) error
	// UpdateStatus updates the status of an object (e.g. for soft deletion)
	UpdateStatus(ctx context.Context, tenantID string, id openapi_types.UUID, status string, idempotencyKey *string) error
	List(ctx context.Context, tenantID string, filter postgres.ListObjectsFilter, limit int, cursor string) ([]postgres.ObjectRecord, string, error)
	PatchMeta(ctx context.Context, tenantID string, id openapi_types.UUID, labels map[string]string, externalRef *string) (*postgres.ObjectRecord, error)
	SignUpload(ctx context.Context, tenantID string, id openapi_types.UUID, uploadTTL int) (s3.Presigned, error)
	SignDownload(ctx context.Context, tenantID string, id openapi_types.UUID, downloadTTL int) (s3.Presigned, error)

	InitiateMultipart(ctx context.Context, tenantID string, contentType string, sizeBytes int64, labels map[string]string, externalRef *string, uploadTTL int, idempotencyKey *string) (MultipartInitResponse, error)
	GetMultipart(ctx context.Context, tenantID string, uploadID string) (*postgres.MultipartRecord, error)
	SignPart(ctx context.Context, tenantID string, uploadID string, partNumber int32) (s3.Presigned, error)
	SignPartsBatch(ctx context.Context, tenantID string, uploadID string, partNumbers []int32) ([]SignPartResponse, error)
	CompleteMultipart(ctx context.Context, tenantID string, uploadID string, parts []CompletePart) (*postgres.ObjectRecord, error)
	AbortMultipart(ctx context.Context, tenantID string, uploadID string) error
}

type objectsService struct {
	objRepo   ObjectsRepository
	multiRepo MultipartRepository
	s3        S3Client
	policy    Policy
	idemRepo  *postgres.IdempotencyRepo
	partSize  int64

	// Configurable timeouts
	fastOperationTimeout    time.Duration
	defaultOperationTimeout time.Duration
	s3OperationTimeout      time.Duration
	longOperationTimeout    time.Duration
	idempotencyTTL          time.Duration
}

func NewObjectsService(
	objRepo ObjectsRepository,
	multiRepo MultipartRepository,
	s3Client S3Client,
	policy Policy,
	idemRepo *postgres.IdempotencyRepo,
	partSize int64,
	fastTimeout, defaultTimeout, s3Timeout, longTimeout time.Duration,
	idempotencyTTL time.Duration,
) ObjectsService {
	return &objectsService{
		objRepo:                 objRepo,
		multiRepo:               multiRepo,
		s3:                      s3Client,
		policy:                  policy,
		idemRepo:                idemRepo,
		partSize:                partSize,
		fastOperationTimeout:    fastTimeout,
		defaultOperationTimeout: defaultTimeout,
		s3OperationTimeout:      s3Timeout,
		longOperationTimeout:    longTimeout,
		idempotencyTTL:          idempotencyTTL,
	}
}

func (s *objectsService) CreateSingle(ctx context.Context, tenantID string, contentType string, sizeBytes int64, labels map[string]string, externalRef *string, uploadTTL int, idempotencyKey *string) (CreateObjectResponse, error) {
	return s.createSingle(ctx, tenantID, contentType, sizeBytes, labels, externalRef, uploadTTL, idempotencyKey)
}

func (s *objectsService) Get(ctx context.Context, tenantID string, id openapi_types.UUID) (*postgres.ObjectRecord, error) {
	return s.get(ctx, tenantID, id)
}

func (s *objectsService) GetMeta(ctx context.Context, tenantID string, id openapi_types.UUID) (*postgres.ObjectRecord, error) {
	return s.getMeta(ctx, tenantID, id)
}

func (s *objectsService) CompleteObject(ctx context.Context, tenantID string, id openapi_types.UUID, etag *string, sizeBytes *int64) (*postgres.ObjectRecord, error) {
	return s.completeObject(ctx, tenantID, id, etag, sizeBytes)
}

func (s *objectsService) HardDelete(ctx context.Context, tenantID string, id openapi_types.UUID, idempotencyKey *string) error {
	return s.hardDeleteObject(ctx, tenantID, id, idempotencyKey)
}

func (s *objectsService) UpdateStatus(ctx context.Context, tenantID string, id openapi_types.UUID, status string, idempotencyKey *string) error {
	return s.updateObjectStatus(ctx, tenantID, id, status, idempotencyKey)
}

func (s *objectsService) List(ctx context.Context, tenantID string, filter postgres.ListObjectsFilter, limit int, cursor string) ([]postgres.ObjectRecord, string, error) {
	return s.listObjects(ctx, tenantID, filter, limit, cursor)
}

func (s *objectsService) PatchMeta(ctx context.Context, tenantID string, id openapi_types.UUID, labels map[string]string, externalRef *string) (*postgres.ObjectRecord, error) {
	return s.patchMeta(ctx, tenantID, id, labels, externalRef)
}

func (s *objectsService) SignUpload(ctx context.Context, tenantID string, id openapi_types.UUID, uploadTTL int) (s3.Presigned, error) {
	return s.signUpload(ctx, tenantID, id, uploadTTL)
}

func (s *objectsService) SignDownload(ctx context.Context, tenantID string, id openapi_types.UUID, downloadTTL int) (s3.Presigned, error) {
	return s.signDownload(ctx, tenantID, id, downloadTTL)
}

func (s *objectsService) InitiateMultipart(ctx context.Context, tenantID string, contentType string, sizeBytes int64, labels map[string]string, externalRef *string, uploadTTL int, idempotencyKey *string) (MultipartInitResponse, error) {
	return s.initiateMultipart(ctx, tenantID, contentType, sizeBytes, labels, externalRef, uploadTTL, idempotencyKey)
}

func (s *objectsService) GetMultipart(ctx context.Context, tenantID string, uploadID string) (*postgres.MultipartRecord, error) {
	return s.getMultipart(ctx, tenantID, uploadID)
}

func (s *objectsService) SignPart(ctx context.Context, tenantID string, uploadID string, partNumber int32) (s3.Presigned, error) {
	return s.signPart(ctx, tenantID, uploadID, partNumber)
}

func (s *objectsService) SignPartsBatch(ctx context.Context, tenantID string, uploadID string, partNumbers []int32) ([]SignPartResponse, error) {
	return s.signPartsBatch(ctx, tenantID, uploadID, partNumbers)
}

func (s *objectsService) CompleteMultipart(ctx context.Context, tenantID string, uploadID string, parts []CompletePart) (*postgres.ObjectRecord, error) {
	return s.completeMultipart(ctx, tenantID, uploadID, parts)
}

func (s *objectsService) AbortMultipart(ctx context.Context, tenantID string, uploadID string) error {
	return s.abortMultipart(ctx, tenantID, uploadID)
}
