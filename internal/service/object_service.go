package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	apperrors "paladin/internal/errors"
	"paladin/internal/logger"
	"paladin/internal/metrics"
	"paladin/internal/storage/s3"
	"paladin/internal/store/postgres"
	"paladin/internal/utils"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.uber.org/zap"
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

type ObjectsService interface {
	CreateSingle(ctx context.Context, tenantID string, contentType string, sizeBytes int64, labels map[string]string, externalRef *string, uploadTTL int, idempotencyKey *string) (CreateObjectResponse, error)
	Get(ctx context.Context, tenantID string, id uuid.UUID) (*postgres.ObjectRecord, error)
	GetMeta(ctx context.Context, tenantID string, id uuid.UUID) (*postgres.ObjectRecord, error)
	CompleteObject(ctx context.Context, tenantID string, id uuid.UUID, etag *string, sizeBytes *int64) (*postgres.ObjectRecord, error)
	Delete(ctx context.Context, tenantID string, id uuid.UUID) error
	List(ctx context.Context, tenantID string, filter postgres.ListObjectsFilter, limit int, cursor string) ([]postgres.ObjectRecord, string, error)
	PatchMeta(ctx context.Context, tenantID string, id uuid.UUID, labels map[string]string, externalRef *string) (*postgres.ObjectRecord, error)
	SignUpload(ctx context.Context, tenantID string, id uuid.UUID, uploadTTL int) (s3.Presigned, error)
	SignDownload(ctx context.Context, tenantID string, id uuid.UUID, downloadTTL int) (s3.Presigned, error)

	InitiateMultipart(ctx context.Context, tenantID string, contentType string, sizeBytes int64, labels map[string]string, externalRef *string, uploadTTL int, idempotencyKey *string) (MultipartInitResponse, error)
	GetMultipart(ctx context.Context, tenantID string, uploadID string) (*postgres.MultipartRecord, error)
	SignPart(ctx context.Context, tenantID string, uploadID string, partNumber int32) (s3.Presigned, error)
	SignPartsBatch(ctx context.Context, tenantID string, uploadID string, partNumbers []int32) ([]SignPartResponse, error)
	CompleteMultipart(ctx context.Context, tenantID string, uploadID string, parts []CompletePart) (*postgres.ObjectRecord, error)
	AbortMultipart(ctx context.Context, tenantID string, uploadID string) error
}

type SignPartResponse struct {
	PartNumber int32
	Upload     s3.Presigned
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
	// Start tracing span
	ctx, span := otel.Tracer("object-service").Start(ctx, "CreateSingle")
	defer span.End()

	span.SetAttributes(
		attribute.String("tenant_id", tenantID),
		attribute.String("content_type", contentType),
		attribute.Int64("size_bytes", sizeBytes),
	)

	start := time.Now()
	var status string
	defer func() {
		duration := time.Since(start).Seconds()
		metrics.RecordObjectOperation("create_single", status, duration)
	}()

	// Enforce operation timeout
	ctx, cancel := context.WithTimeout(ctx, s.defaultOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, ActionCreate); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return CreateObjectResponse{}, err
	}

	if err := s.policy.Validate(contentType, sizeBytes); err != nil {
		return CreateObjectResponse{}, apperrors.ValidationFailed("validation failed", err)
	}

	// Validate external_ref for security
	if externalRef != nil {
		if err := utils.ValidateExternalRef(*externalRef); err != nil {
			return CreateObjectResponse{}, apperrors.ValidationFailed("invalid external_ref", err)
		}
	}

	// Validate labels
	if err := utils.ValidateLabels(labels); err != nil {
		return CreateObjectResponse{}, apperrors.ValidationFailed("invalid labels", err)
	}

	// 1. Check Idempotency-Key header for cached response
	if idempotencyKey != nil && s.idemRepo != nil {
		if cached, err := s.idemRepo.Get(ctx, tenantID, *idempotencyKey); err == nil && cached != nil {
			var res CreateObjectResponse
			if err := json.Unmarshal(cached.ResponseBody, &res); err == nil {
				return res, nil
			}
		}
	}

	// 2. Check external_ref for idempotency
	if externalRef != nil {
		existing, err := s.objRepo.GetByExternalRef(ctx, tenantID, *externalRef)
		if err != nil {
			return CreateObjectResponse{}, err
		}
		if existing != nil {
			if existing.ContentType == contentType && existing.SizeBytes == sizeBytes {
				// Idempotent retry: re-generate upload URL
				ttl := time.Duration(uploadTTL) * time.Second
				if ttl == 0 {
					ttl = s.s3.PresignTTLDuration()
				}
				signed, err := s.s3.PresignPutObject(ctx, existing.ObjectKey, existing.ContentType, existing.SizeBytes, ttl)
				if err != nil {
					return CreateObjectResponse{}, err
				}
				return CreateObjectResponse{
					ID:     existing.ID,
					Key:    existing.ObjectKey,
					Bucket: existing.Bucket,
					Upload: signed,
				}, nil
			}
			return CreateObjectResponse{}, apperrors.Conflict("object with this external_ref already exists with different parameters", nil)
		}
	}

	id := uuid.New()
	key := fmt.Sprintf("%s/%s", tenantID, id.String())

	ttl := time.Duration(uploadTTL) * time.Second
	if ttl == 0 {
		ttl = s.s3.PresignTTLDuration()
	}

	signed, err := s.s3.PresignPutObject(ctx, key, contentType, sizeBytes, ttl)
	if err != nil {
		return CreateObjectResponse{}, err
	}

	bucket := s.s3.BucketName()
	expiresAt := time.Now().Add(ttl)
	rec := postgres.ObjectRecord{
		ID: id, TenantID: tenantID, ObjectKey: key, Bucket: bucket,
		ContentType: contentType, SizeBytes: sizeBytes,
		Status: postgres.ObjectPending, Labels: labels, ExternalRef: externalRef,
		ExpiresAt: &expiresAt,
	}

	if err := s.objRepo.Create(ctx, rec); err != nil {
		return CreateObjectResponse{}, err
	}

	res := CreateObjectResponse{
		ID:     id,
		Key:    key,
		Bucket: bucket,
		Upload: signed,
	}

	// 4. Save response for Idempotency-Key
	if idempotencyKey != nil && s.idemRepo != nil {
		body, _ := json.Marshal(res)
		_ = s.idemRepo.Save(ctx, postgres.IdempotencyRecord{
			TenantID:     tenantID,
			Key:          *idempotencyKey,
			RequestPath:  "/v1/objects",
			ResponseBody: body,
			ResponseCode: 200,
			ExpiresAt:    time.Now().Add(s.idempotencyTTL),
		})
	}

	// Mark operation as successful
	status = "success"
	span.SetStatus(codes.Ok, "")
	span.SetAttributes(attribute.String("object_id", id.String()))

	return res, nil
}

func (s *objectsService) Get(ctx context.Context, tenantID string, id uuid.UUID) (*postgres.ObjectRecord, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "Get")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("object_id", id.String()))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("get", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.fastOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, ActionRead); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	rec, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	status = "success"
	span.SetStatus(codes.Ok, "")
	return rec, nil
}

func (s *objectsService) GetMeta(ctx context.Context, tenantID string, id uuid.UUID) (*postgres.ObjectRecord, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "GetMeta")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("object_id", id.String()))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("get_meta", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.fastOperationTimeout)
	defer cancel()

	rec, err := s.Get(ctx, tenantID, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	status = "success"
	span.SetStatus(codes.Ok, "")
	return rec, nil
}

func (s *objectsService) CompleteObject(ctx context.Context, tenantID string, id uuid.UUID, etag *string, sizeBytes *int64) (*postgres.ObjectRecord, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "CompleteObject")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("object_id", id.String()))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("complete_object", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, ActionUpdate); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	rec, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	if rec.Status == postgres.ObjectComplete {
		status = "success"
		span.SetStatus(codes.Ok, "already_complete")
		return rec, nil
	}

	// Double check S3
	head, err := s.s3.HeadObject(ctx, rec.ObjectKey)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, fmt.Errorf("s3 head check: %w", err)
	}

	// Validate ETag if provided
	if etag != nil && *etag != head.ETag {
		err := fmt.Errorf("etag mismatch: expected %s, got %s", *etag, head.ETag)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}
	// Validate Size if provided
	if sizeBytes != nil && *sizeBytes != head.SizeBytes {
		err := fmt.Errorf("size mismatch: expected %d, got %d", *sizeBytes, head.SizeBytes)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	updated, err := s.objRepo.MarkComplete(ctx, tenantID, id, head.ETag, head.SizeBytes)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	if updated {
		logger.FromContext(ctx).Info("Object Upload Completed", zap.String("tenant_id", tenantID), zap.String("object_id", id.String()))
	}

	finalRec, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	status = "success"
	span.SetStatus(codes.Ok, "")
	return finalRec, nil
}

func (s *objectsService) Delete(ctx context.Context, tenantID string, id uuid.UUID) error {
	ctx, span := otel.Tracer("object-service").Start(ctx, "Delete")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("object_id", id.String()))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("delete", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.fastOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, ActionDelete); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return err
	}

	updated, err := s.objRepo.MarkDeleted(ctx, tenantID, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return err
	}

	if updated {
		logger.FromContext(ctx).Info("Object Deleted", zap.String("tenant_id", tenantID), zap.String("object_id", id.String()))
	}

	status = "success"
	span.SetStatus(codes.Ok, "")
	return nil
}

func (s *objectsService) List(ctx context.Context, tenantID string, filter postgres.ListObjectsFilter, limit int, cursor string) ([]postgres.ObjectRecord, string, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "List")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.Int("limit", limit))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("list", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.defaultOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, ActionRead); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, "", err
	}

	recs, nextCursor, err := s.objRepo.List(ctx, tenantID, filter, limit, cursor)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, "", err
	}

	status = "success"
	span.SetStatus(codes.Ok, "")
	span.SetAttributes(attribute.Int("result_count", len(recs)))
	return recs, nextCursor, nil
}

func (s *objectsService) PatchMeta(ctx context.Context, tenantID string, id uuid.UUID, labels map[string]string, externalRef *string) (*postgres.ObjectRecord, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "PatchMeta")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("object_id", id.String()))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("patch_meta", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.fastOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, ActionUpdate); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	rec, err := s.objRepo.Patch(ctx, tenantID, id, labels, externalRef)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	status = "success"
	span.SetStatus(codes.Ok, "")
	return rec, nil
}

func (s *objectsService) SignUpload(ctx context.Context, tenantID string, id uuid.UUID, uploadTTL int) (s3.Presigned, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "SignUpload")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("object_id", id.String()))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("sign_upload", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, ActionUpdate); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return s3.Presigned{}, err
	}

	rec, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return s3.Presigned{}, err
	}

	ttl := time.Duration(uploadTTL) * time.Second
	if ttl == 0 {
		ttl = s.s3.PresignTTLDuration()
	}

	presigned, err := s.s3.PresignPutObject(ctx, rec.ObjectKey, rec.ContentType, rec.SizeBytes, ttl)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return s3.Presigned{}, err
	}

	status = "success"
	span.SetStatus(codes.Ok, "")
	return presigned, nil
}

func (s *objectsService) SignDownload(ctx context.Context, tenantID string, id uuid.UUID, downloadTTL int) (s3.Presigned, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "SignDownload")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("object_id", id.String()))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("sign_download", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, ActionRead); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return s3.Presigned{}, err
	}

	rec, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return s3.Presigned{}, err
	}

	if rec.Status != postgres.ObjectComplete {
		err := fmt.Errorf("object not complete")
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return s3.Presigned{}, err
	}

	ttl := time.Duration(downloadTTL) * time.Second
	if ttl == 0 {
		ttl = s.s3.PresignTTLDuration()
	}

	presigned, err := s.s3.PresignGetObject(ctx, rec.ObjectKey, ttl)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return s3.Presigned{}, err
	}

	status = "success"
	span.SetStatus(codes.Ok, "")
	return presigned, nil
}

func (s *objectsService) InitiateMultipart(ctx context.Context, tenantID string, contentType string, sizeBytes int64, labels map[string]string, externalRef *string, uploadTTL int, idempotencyKey *string) (MultipartInitResponse, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "InitiateMultipart")
	defer span.End()
	span.SetAttributes(
		attribute.String("tenant_id", tenantID),
		attribute.String("content_type", contentType),
		attribute.Int64("size_bytes", sizeBytes),
	)

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("initiate_multipart", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, ActionCreate); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return MultipartInitResponse{}, err
	}

	if err := s.policy.Validate(contentType, sizeBytes); err != nil {
		return MultipartInitResponse{}, apperrors.ValidationFailed("validation failed", err)
	}

	// Validate external_ref for security
	if externalRef != nil {
		if err := utils.ValidateExternalRef(*externalRef); err != nil {
			return MultipartInitResponse{}, apperrors.ValidationFailed("invalid external_ref", err)
		}
	}

	// Validate labels
	if err := utils.ValidateLabels(labels); err != nil {
		return MultipartInitResponse{}, apperrors.ValidationFailed("invalid labels", err)
	}

	// 1. Check Idempotency-Key header
	if idempotencyKey != nil && s.idemRepo != nil {
		if cached, err := s.idemRepo.Get(ctx, tenantID, *idempotencyKey); err == nil && cached != nil {
			var res MultipartInitResponse
			if err := json.Unmarshal(cached.ResponseBody, &res); err == nil {
				return res, nil
			}
		}
	}

	// 2. Check external_ref
	if externalRef != nil {
		existing, err := s.objRepo.GetByExternalRef(ctx, tenantID, *externalRef)
		if err == nil && existing != nil {
			return MultipartInitResponse{}, apperrors.Conflict("object with this external_ref already exists", nil)
		}
	}

	id := uuid.New()
	key := fmt.Sprintf("%s/%s", tenantID, id.String())

	init, err := s.s3.CreateMultipartUpload(ctx, key, contentType)
	if err != nil {
		return MultipartInitResponse{}, err
	}

	ttl := time.Duration(uploadTTL) * time.Second
	if ttl == 0 {
		ttl = s.s3.PresignTTLDuration()
	}
	expiresAt := time.Now().Add(ttl)

	objRec := postgres.ObjectRecord{
		ID: id, TenantID: tenantID, ObjectKey: key, Bucket: s.s3.BucketName(),
		ContentType: contentType, SizeBytes: sizeBytes,
		Status: postgres.ObjectUploading, Labels: labels, ExternalRef: externalRef,
		ExpiresAt: &expiresAt,
	}
	if err := s.objRepo.Create(ctx, objRec); err != nil {
		return MultipartInitResponse{}, err
	}

	multiRec := postgres.MultipartRecord{
		ID: id, TenantID: tenantID, ObjectID: id, ObjectKey: key, UploadID: init.UploadID,
		Status: postgres.MultipartInitiated, ExpiresAt: expiresAt,
		Bucket: init.Bucket, ContentType: contentType, PartSize: s.partSize,
	}
	if err := s.multiRepo.Create(ctx, multiRec); err != nil {
		return MultipartInitResponse{}, err
	}

	res := MultipartInitResponse{
		ObjectID:  id,
		ObjectKey: key,
		UploadID:  init.UploadID,
		Bucket:    s.s3.BucketName(),
		PartSize:  s.partSize,
		ExpiresAt: expiresAt,
	}

	// 4. Save response for Idempotency-Key
	if idempotencyKey != nil && s.idemRepo != nil {
		body, _ := json.Marshal(res)
		_ = s.idemRepo.Save(ctx, postgres.IdempotencyRecord{
			TenantID:     tenantID,
			Key:          *idempotencyKey,
			RequestPath:  "/v1/multipart",
			ResponseBody: body,
			ResponseCode: 200,
			ExpiresAt:    time.Now().Add(s.idempotencyTTL),
		})
	}

	return res, nil
}

func (s *objectsService) GetMultipart(ctx context.Context, tenantID string, uploadID string) (*postgres.MultipartRecord, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "GetMultipart")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("upload_id", uploadID))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("get_multipart", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.fastOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, ActionRead); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	rec, err := s.multiRepo.GetByUploadID(ctx, tenantID, uploadID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	status = "success"
	span.SetStatus(codes.Ok, "")
	return rec, nil
}

func (s *objectsService) SignPart(ctx context.Context, tenantID string, uploadID string, partNumber int32) (s3.Presigned, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "SignPart")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("upload_id", uploadID), attribute.Int("part_number", int(partNumber)))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("sign_part", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, ActionUpdate); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return s3.Presigned{}, err
	}

	multi, err := s.multiRepo.GetByUploadID(ctx, tenantID, uploadID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return s3.Presigned{}, err
	}

	presigned, err := s.s3.PresignUploadPart(ctx, multi.ObjectKey, uploadID, partNumber, s.s3.PresignTTLDuration())
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return s3.Presigned{}, err
	}

	status = "success"
	span.SetStatus(codes.Ok, "")
	return presigned, nil
}

func (s *objectsService) SignPartsBatch(ctx context.Context, tenantID string, uploadID string, partNumbers []int32) ([]SignPartResponse, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "SignPartsBatch")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("upload_id", uploadID), attribute.Int("part_count", len(partNumbers)))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("sign_parts_batch", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, ActionUpdate); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	multi, err := s.multiRepo.GetByUploadID(ctx, tenantID, uploadID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	out := make([]SignPartResponse, len(partNumbers))
	for i, pn := range partNumbers {
		signed, err := s.s3.PresignUploadPart(ctx, multi.ObjectKey, uploadID, pn, s.s3.PresignTTLDuration())
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			status = "error"
			return nil, err
		}
		out[i] = SignPartResponse{PartNumber: pn, Upload: signed}
	}

	status = "success"
	span.SetStatus(codes.Ok, "")
	return out, nil
}

func (s *objectsService) CompleteMultipart(ctx context.Context, tenantID string, uploadID string, parts []CompletePart) (*postgres.ObjectRecord, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "CompleteMultipart")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("upload_id", uploadID), attribute.Int("parts_count", len(parts)))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("complete_multipart", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.longOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, ActionUpdate); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	multi, err := s.multiRepo.GetByUploadID(ctx, tenantID, uploadID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	s3Parts := make([]types.CompletedPart, len(parts))
	for i, p := range parts {
		s3Parts[i] = types.CompletedPart{
			ETag:       aws.String(p.ETag),
			PartNumber: aws.Int32(p.PartNumber),
		}
	}

	if err := s.s3.CompleteMultipartUpload(ctx, multi.ObjectKey, uploadID, s3Parts); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	// Double check S3 for final ETag/Size
	head, err := s.s3.HeadObject(ctx, multi.ObjectKey)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, fmt.Errorf("s3 head after complete: %w", err)
	}

	if _, err := s.objRepo.MarkComplete(ctx, tenantID, multi.ObjectID, head.ETag, head.SizeBytes); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	if err := s.multiRepo.MarkCompleted(ctx, tenantID, uploadID); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	rec, err := s.objRepo.Get(ctx, tenantID, multi.ObjectID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return nil, err
	}

	status = "success"
	span.SetStatus(codes.Ok, "")
	span.SetAttributes(attribute.String("object_id", multi.ObjectID.String()))
	return rec, nil
}

func (s *objectsService) AbortMultipart(ctx context.Context, tenantID string, uploadID string) error {
	ctx, span := otel.Tracer("object-service").Start(ctx, "AbortMultipart")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.String("upload_id", uploadID))

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOperation("abort_multipart", status, time.Since(start).Seconds()) }()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, ActionUpdate); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return err
	}

	multi, err := s.multiRepo.GetByUploadID(ctx, tenantID, uploadID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return err
	}

	if err := s.s3.AbortMultipartUpload(ctx, multi.ObjectKey, uploadID); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return err
	}

	err = s.multiRepo.MarkAborted(ctx, tenantID, uploadID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"
		return err
	}

	status = "success"
	span.SetStatus(codes.Ok, "")
	return nil
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
