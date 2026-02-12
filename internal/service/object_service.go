package service

import (
	"context"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/domain"

	openapi_types "github.com/oapi-codegen/runtime/types"
)

type objectsService struct {
	objRepo   domain.ObjectsRepository
	multiRepo domain.MultipartRepository
	s3        domain.StorageClient
	policy    domain.Policy
	idemRepo  domain.IdempotencyRepository
	partSize  int64

	// Configurable timeouts
	fastOperationTimeout    time.Duration
	defaultOperationTimeout time.Duration
	s3OperationTimeout      time.Duration
	longOperationTimeout    time.Duration
	idempotencyTTL          time.Duration
}

func NewObjectsService(
	objRepo domain.ObjectsRepository,
	multiRepo domain.MultipartRepository,
	s3Client domain.StorageClient,
	policy domain.Policy,
	idemRepo domain.IdempotencyRepository,
	partSize int64,
	fastTimeout, defaultTimeout, s3Timeout, longTimeout time.Duration,
	idempotencyTTL time.Duration,
) domain.ObjectsService {
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

func (s *objectsService) CreateSingle(ctx context.Context, tenantID string, contentType string, sizeBytes int64, labels map[string]string, externalRef *string, uploadTTL int, idempotencyKey *string) (domain.CreateObjectResponse, error) {
	return s.createSingle(ctx, tenantID, contentType, sizeBytes, labels, externalRef, uploadTTL, idempotencyKey)
}

func (s *objectsService) Get(ctx context.Context, tenantID string, id openapi_types.UUID) (*domain.Object, error) {
	return s.get(ctx, tenantID, id)
}

func (s *objectsService) GetMeta(ctx context.Context, tenantID string, id openapi_types.UUID) (*domain.Object, error) {
	return s.getMeta(ctx, tenantID, id)
}

func (s *objectsService) CompleteObject(ctx context.Context, tenantID string, id openapi_types.UUID, etag *string, sizeBytes *int64) (*domain.Object, error) {
	return s.completeObject(ctx, tenantID, id, etag, sizeBytes)
}

func (s *objectsService) HardDelete(ctx context.Context, tenantID string, id openapi_types.UUID, idempotencyKey *string) error {
	return s.hardDeleteObject(ctx, tenantID, id, idempotencyKey)
}

func (s *objectsService) UpdateStatus(ctx context.Context, tenantID string, id openapi_types.UUID, status string, idempotencyKey *string) error {
	return s.updateObjectStatus(ctx, tenantID, id, status, idempotencyKey)
}

func (s *objectsService) List(ctx context.Context, tenantID string, filter domain.ListObjectsFilter, limit int, cursor string) ([]domain.Object, string, error) {
	return s.listObjects(ctx, tenantID, filter, limit, cursor)
}

func (s *objectsService) PatchMeta(ctx context.Context, tenantID string, id openapi_types.UUID, labels map[string]string, externalRef *string) (*domain.Object, error) {
	return s.patchMeta(ctx, tenantID, id, labels, externalRef)
}

func (s *objectsService) SignUpload(ctx context.Context, tenantID string, id openapi_types.UUID, uploadTTL int) (domain.Presigned, error) {
	return s.signUpload(ctx, tenantID, id, uploadTTL)
}

func (s *objectsService) SignDownload(ctx context.Context, tenantID string, id openapi_types.UUID, downloadTTL int) (domain.Presigned, error) {
	return s.signDownload(ctx, tenantID, id, downloadTTL)
}

func (s *objectsService) InitiateMultipart(ctx context.Context, tenantID string, contentType string, sizeBytes int64, labels map[string]string, externalRef *string, uploadTTL int, idempotencyKey *string) (domain.MultipartInitResponse, error) {
	return s.initiateMultipart(ctx, tenantID, contentType, sizeBytes, labels, externalRef, uploadTTL, idempotencyKey)
}

func (s *objectsService) GetMultipart(ctx context.Context, tenantID string, uploadID string) (*domain.Multipart, error) {
	return s.getMultipart(ctx, tenantID, uploadID)
}

func (s *objectsService) SignPart(ctx context.Context, tenantID string, uploadID string, partNumber int32) (domain.Presigned, error) {
	return s.signPart(ctx, tenantID, uploadID, partNumber)
}

func (s *objectsService) SignPartsBatch(ctx context.Context, tenantID string, uploadID string, partNumbers []int32) ([]domain.SignPartResponse, error) {
	return s.signPartsBatch(ctx, tenantID, uploadID, partNumbers)
}

func (s *objectsService) CompleteMultipart(ctx context.Context, tenantID string, uploadID string, parts []domain.CompletePart) (*domain.Object, error) {
	return s.completeMultipart(ctx, tenantID, uploadID, parts)
}

func (s *objectsService) AbortMultipart(ctx context.Context, tenantID string, uploadID string) error {
	return s.abortMultipart(ctx, tenantID, uploadID)
}
