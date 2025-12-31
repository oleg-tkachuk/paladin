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

type ObjectsService struct {
	policy   Policy
	s3       *s3.Client
	objRepo  *postgres.ObjectsRepo
	mpRepo   *postgres.MultipartRepo
	brk      breaker.Factory
	partSize int64
}

func NewObjectsService(policy Policy, s3c *s3.Client, objRepo *postgres.ObjectsRepo, mpRepo *postgres.MultipartRepo, brk breaker.Factory, partSize int64) *ObjectsService {
	return &ObjectsService{policy: policy, s3: s3c, objRepo: objRepo, mpRepo: mpRepo, brk: brk, partSize: partSize}
}

func (s *ObjectsService) CreateSingle(ctx context.Context, tenantID string, contentType string, sizeBytes int64, checksum []byte) (uuid.UUID, string, s3.Presigned, error) {
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
		ID: id, TenantID: tenantID, ObjectKey: key, Bucket: s.s3.Bucket,
		ContentType: contentType, SizeBytes: sizeBytes, ChecksumSHA256: checksumStr,
		Status: postgres.ObjectPending,
	}

	if err := s.objRepo.Create(ctx, rec); err != nil {
		return uuid.UUID{}, "", s3.Presigned{}, err
	}

	p, err := s.s3.PresignPutObject(ctx, key, contentType, sizeBytes)
	if err != nil {
		return uuid.UUID{}, "", s3.Presigned{}, err
	}

	return id, key, p, nil
}

func (s *ObjectsService) Get(ctx context.Context, tenantID string, id uuid.UUID) (*postgres.ObjectRecord, s3.Presigned, error) {
	rec, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		return nil, s3.Presigned{}, err
	}

	p, err := s.s3.PresignGetObject(ctx, rec.ObjectKey)
	if err != nil {
		return nil, s3.Presigned{}, err
	}

	return rec, p, nil
}

func (s *ObjectsService) MarkComplete(ctx context.Context, tenantID string, id uuid.UUID) error {
	return s.objRepo.MarkActive(ctx, tenantID, id)
}

type MultipartInitResponse struct {
	ObjectID  uuid.UUID
	ObjectKey string
	UploadID  string
	PartSize  int64
	ExpiresAt time.Time
}

func (s *ObjectsService) InitiateMultipart(ctx context.Context, tenantID string, contentType string, sizeBytes int64) (MultipartInitResponse, error) {
	if err := s.policy.Validate(contentType, sizeBytes); err != nil {
		return MultipartInitResponse{}, err
	}

	objectID, _ := uuid.NewV7()
	objectKey := fmt.Sprintf("%s/%s", tenantID, objectID.String())

	if err := s.objRepo.Create(ctx, postgres.ObjectRecord{
		ID: objectID, TenantID: tenantID, ObjectKey: objectKey, Bucket: s.s3.Bucket,
		ContentType: contentType, SizeBytes: sizeBytes, Status: postgres.ObjectPending,
	}); err != nil {
		return MultipartInitResponse{}, err
	}

	init, err := s.s3.CreateMultipartUpload(ctx, objectKey, contentType)
	if err != nil {
		return MultipartInitResponse{}, err
	}

	mpuID, _ := uuid.NewV7()
	expires := time.Now().Add(s.s3.PresignTTL)

	if err := s.mpRepo.Create(ctx, postgres.MultipartRecord{
		ID: mpuID, TenantID: tenantID, ObjectID: objectID,
		UploadID: init.UploadID, Bucket: init.Bucket, ObjectKey: init.Key, ContentType: contentType,
		PartSize: s.partSize, Status: postgres.MultipartInitiated, ExpiresAt: expires,
	}); err != nil {
		return MultipartInitResponse{}, err
	}

	return MultipartInitResponse{
		ObjectID:  objectID,
		ObjectKey: objectKey,
		UploadID:  init.UploadID,
		PartSize:  s.partSize,
		ExpiresAt: expires,
	}, nil
}

func (s *ObjectsService) SignPart(ctx context.Context, tenantID string, uploadID string, partNumber int32) (s3.Presigned, error) {
	mpu, err := s.mpRepo.GetByUploadID(ctx, tenantID, uploadID)
	if err != nil {
		return s3.Presigned{}, err
	}

	return s.s3.PresignUploadPart(ctx, mpu.ObjectKey, mpu.UploadID, partNumber)
}

type CompletePart struct {
	PartNumber int32
	ETag       string
}

func (s *ObjectsService) CompleteMultipart(ctx context.Context, tenantID string, uploadID string, parts []CompletePart) (uuid.UUID, error) {
	mpu, err := s.mpRepo.GetByUploadID(ctx, tenantID, uploadID)
	if err != nil {
		return uuid.UUID{}, err
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
		return uuid.UUID{}, err
	}

	if err := s.mpRepo.MarkCompleted(ctx, tenantID, uploadID); err != nil {
		return uuid.UUID{}, err
	}

	if err := s.objRepo.MarkActive(ctx, tenantID, mpu.ObjectID); err != nil {
		return uuid.UUID{}, err
	}

	return mpu.ObjectID, nil
}

func (s *ObjectsService) AbortMultipart(ctx context.Context, tenantID string, uploadID string) error {
	mpu, err := s.mpRepo.GetByUploadID(ctx, tenantID, uploadID)
	if err != nil {
		return err
	}

	w := s.brk.Get("s3.abort_multipart")
	if _, err := fault.Execute(w, func() (interface{}, error) {
		return nil, s.s3.AbortMultipartUpload(ctx, mpu.ObjectKey, mpu.UploadID)
	}); err != nil {
		return err
	}

	return s.mpRepo.MarkAborted(ctx, tenantID, uploadID)
}
