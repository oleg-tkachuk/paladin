package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"paladin/internal/breaker"
	"paladin/internal/fault"
	"paladin/internal/storage/s3"
	"paladin/internal/store/postgres"

	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
)

type ObjectsRepository interface {
	Create(ctx context.Context, rec postgres.ObjectRecord) error
	Get(ctx context.Context, tenantID string, id uuid.UUID) (*postgres.ObjectRecord, error)
	MarkActive(ctx context.Context, tenantID string, id uuid.UUID) error
	MarkDeleted(ctx context.Context, tenantID string, id uuid.UUID) error
}

type MultipartRepository interface {
	Create(ctx context.Context, rec postgres.MultipartRecord) error
	GetByUploadID(ctx context.Context, tenantID string, uploadID string) (*postgres.MultipartRecord, error)
	UpsertPartETag(ctx context.Context, multipartID uuid.UUID, partNumber int, etag string, sizeBytes *int64) error
	MarkCompleted(ctx context.Context, tenantID string, uploadID string) error
	MarkAborted(ctx context.Context, tenantID string, uploadID string) error
}

type S3Client interface {
	BucketName() string
	PresignTTLDuration() time.Duration
	PresignPutObject(ctx context.Context, key string, contentType string, sizeBytes int64) (s3.Presigned, error)
	PresignGetObject(ctx context.Context, key string) (s3.Presigned, error)
	PresignUploadPart(ctx context.Context, key, uploadID string, partNumber int32) (s3.Presigned, error)
	CreateMultipartUpload(ctx context.Context, key string, contentType string) (s3.MultipartInit, error)
	CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []types.CompletedPart) error
	AbortMultipartUpload(ctx context.Context, key, uploadID string) error
}

type ObjectsService interface {
	CreateSingle(ctx context.Context, tenantID string, contentType string, sizeBytes int64, checksum []byte) (uuid.UUID, string, s3.Presigned, error)
	Get(ctx context.Context, tenantID string, id uuid.UUID) (*postgres.ObjectRecord, s3.Presigned, error)
	MarkComplete(ctx context.Context, tenantID string, id uuid.UUID) error
	Delete(ctx context.Context, tenantID string, id uuid.UUID) error
	InitiateMultipart(ctx context.Context, tenantID string, contentType string, sizeBytes int64) (MultipartInitResponse, error)
	SignPart(ctx context.Context, tenantID string, uploadID string, partNumber int32) (s3.Presigned, error)
	CompleteMultipart(ctx context.Context, tenantID string, uploadID string, parts []CompletePart) (uuid.UUID, error)
	AbortMultipart(ctx context.Context, tenantID string, uploadID string) error
}

type objectsService struct {
	policy   Policy
	s3       S3Client
	objRepo  ObjectsRepository
	mpRepo   MultipartRepository
	brk      breaker.Factory
	partSize int64
}

func NewObjectsService(policy Policy, s3c S3Client, objRepo ObjectsRepository, mpRepo MultipartRepository, brk breaker.Factory, partSize int64) ObjectsService {
	return &objectsService{policy: policy, s3: s3c, objRepo: objRepo, mpRepo: mpRepo, brk: brk, partSize: partSize}
}

func (s *objectsService) CreateSingle(ctx context.Context, tenantID string, contentType string, sizeBytes int64, checksum []byte) (uuid.UUID, string, s3.Presigned, error) {
	if err := s.policy.Validate(contentType, sizeBytes); err != nil {
		return uuid.UUID{}, "", s3.Presigned{}, err
	}

	id, _ := uuid.NewV7()
	key := fmt.Sprintf("%s/%s", tenantID, id.String())

	var checksumStr *string

	if len(checksum) > 0 {
		sum := sha256.Sum256(checksum)
		hexed := hex.EncodeToString(sum[:])
		checksumStr = &hexed
	}

	rec := postgres.ObjectRecord{
		ID: id, TenantID: tenantID, ObjectKey: key, Bucket: s.s3.BucketName(),
		ContentType: contentType, SizeBytes: sizeBytes, ChecksumSHA256: checksumStr,
		Status: postgres.ObjectPending,
	}

	if err := s.objRepo.Create(ctx, rec); err != nil {
		return uuid.UUID{}, "", s3.Presigned{}, fmt.Errorf("repo create: %w", err)
	}

	p, err := s.s3.PresignPutObject(ctx, key, contentType, sizeBytes)
	if err != nil {
		return uuid.UUID{}, "", s3.Presigned{}, fmt.Errorf("s3 presign put: %w", err)
	}

	return id, key, p, nil
}

func (s *objectsService) Get(ctx context.Context, tenantID string, id uuid.UUID) (*postgres.ObjectRecord, s3.Presigned, error) {
	rec, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		return nil, s3.Presigned{}, fmt.Errorf("repo get: %w", err)
	}

	p, err := s.s3.PresignGetObject(ctx, rec.ObjectKey)
	if err != nil {
		return nil, s3.Presigned{}, fmt.Errorf("s3 presign get: %w", err)
	}

	return rec, p, nil
}

func (s *objectsService) MarkComplete(ctx context.Context, tenantID string, id uuid.UUID) error {
	if err := s.objRepo.MarkActive(ctx, tenantID, id); err != nil {
		return fmt.Errorf("repo mark active: %w", err)
	}

	return nil
}

func (s *objectsService) Delete(ctx context.Context, tenantID string, id uuid.UUID) error {
	if err := s.objRepo.MarkDeleted(ctx, tenantID, id); err != nil {
		return fmt.Errorf("repo mark deleted: %w", err)
	}

	return nil
}

type MultipartInitResponse struct {
	ObjectID  uuid.UUID
	ObjectKey string
	UploadID  string
	PartSize  int64
	ExpiresAt time.Time
}

func (s *objectsService) InitiateMultipart(ctx context.Context, tenantID string, contentType string, sizeBytes int64) (MultipartInitResponse, error) {
	if err := s.policy.Validate(contentType, sizeBytes); err != nil {
		return MultipartInitResponse{}, err
	}

	objectID, _ := uuid.NewV7()
	objectKey := fmt.Sprintf("%s/%s", tenantID, objectID.String())

	if err := s.objRepo.Create(ctx, postgres.ObjectRecord{
		ID: objectID, TenantID: tenantID, ObjectKey: objectKey, Bucket: s.s3.BucketName(),
		ContentType: contentType, SizeBytes: sizeBytes, Status: postgres.ObjectPending,
	}); err != nil {
		return MultipartInitResponse{}, fmt.Errorf("repo create object: %w", err)
	}

	init, err := s.s3.CreateMultipartUpload(ctx, objectKey, contentType)
	if err != nil {
		return MultipartInitResponse{}, fmt.Errorf("s3 create multipart: %w", err)
	}

	mpuID, _ := uuid.NewV7()
	expires := time.Now().Add(s.s3.PresignTTLDuration())

	if err := s.mpRepo.Create(ctx, postgres.MultipartRecord{
		ID: mpuID, TenantID: tenantID, ObjectID: objectID,
		UploadID: init.UploadID, Bucket: init.Bucket, ObjectKey: init.Key, ContentType: contentType,
		PartSize: s.partSize, Status: postgres.MultipartInitiated, ExpiresAt: expires,
	}); err != nil {
		return MultipartInitResponse{}, fmt.Errorf("repo create multipart: %w", err)
	}

	return MultipartInitResponse{
		ObjectID:  objectID,
		ObjectKey: objectKey,
		UploadID:  init.UploadID,
		PartSize:  s.partSize,
		ExpiresAt: expires,
	}, nil
}

func (s *objectsService) SignPart(ctx context.Context, tenantID string, uploadID string, partNumber int32) (s3.Presigned, error) {
	mpu, err := s.mpRepo.GetByUploadID(ctx, tenantID, uploadID)
	if err != nil {
		return s3.Presigned{}, fmt.Errorf("repo get multipart: %w", err)
	}

	p, err := s.s3.PresignUploadPart(ctx, mpu.ObjectKey, mpu.UploadID, partNumber)
	if err != nil {
		return s3.Presigned{}, fmt.Errorf("s3 presign upload part: %w", err)
	}

	return p, nil
}

type CompletePart struct {
	PartNumber int32
	ETag       string
}

func (s *objectsService) CompleteMultipart(ctx context.Context, tenantID string, uploadID string, parts []CompletePart) (uuid.UUID, error) {
	mpu, err := s.mpRepo.GetByUploadID(ctx, tenantID, uploadID)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("repo get multipart: %w", err)
	}

	completed := make([]types.CompletedPart, 0, len(parts))

	for _, p := range parts {
		et := p.ETag
		completed = append(completed, types.CompletedPart{
			PartNumber: &p.PartNumber,
			ETag:       &et,
		})
		_ = s.mpRepo.UpsertPartETag(ctx, mpu.ID, int(p.PartNumber), et, nil)
	}

	w := s.brk.Get("s3.complete_multipart")
	if _, err := fault.Execute(w, func() (interface{}, error) {
		return nil, s.s3.CompleteMultipartUpload(ctx, mpu.ObjectKey, mpu.UploadID, completed)
	}); err != nil {
		return uuid.UUID{}, fmt.Errorf("s3 complete multipart: %w", err)
	}

	if err := s.mpRepo.MarkCompleted(ctx, tenantID, uploadID); err != nil {
		return uuid.UUID{}, fmt.Errorf("repo mark multipart completed: %w", err)
	}

	if err := s.objRepo.MarkActive(ctx, tenantID, mpu.ObjectID); err != nil {
		return uuid.UUID{}, fmt.Errorf("repo mark object active: %w", err)
	}

	return mpu.ObjectID, nil
}

func (s *objectsService) AbortMultipart(ctx context.Context, tenantID string, uploadID string) error {
	mpu, err := s.mpRepo.GetByUploadID(ctx, tenantID, uploadID)
	if err != nil {
		return fmt.Errorf("repo get multipart: %w", err)
	}

	w := s.brk.Get("s3.abort_multipart")
	if _, err := fault.Execute(w, func() (interface{}, error) {
		return nil, s.s3.AbortMultipartUpload(ctx, mpu.ObjectKey, mpu.UploadID)
	}); err != nil {
		return fmt.Errorf("s3 abort multipart: %w", err)
	}

	if err := s.mpRepo.MarkAborted(ctx, tenantID, uploadID); err != nil {
		return fmt.Errorf("repo mark multipart aborted: %w", err)
	}

	return nil
}
