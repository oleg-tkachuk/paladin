package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"github.com/oleg-tkachuk/paladin/internal/domain"
)

func (s *objectsService) CopyObject(ctx context.Context, tenantID, srcBucket, srcKey, dstBucket, dstKey string, metadata map[string]string) (*domain.Object, error) {
	ctx, op := beginOp(ctx, "CopyObject", "copy",
		attribute.String("tenant_id", tenantID),
		attribute.String("src_bucket", srcBucket),
		attribute.String("src_key", srcKey),
		attribute.String("dst_bucket", dstBucket),
		attribute.String("dst_key", dstKey),
	)
	defer op.end()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionRead); err != nil {
		op.fail(err)
		return nil, err
	}
	if err := s.policy.Authorize(ctx, tenantID, domain.ActionCreate); err != nil {
		op.fail(err)
		return nil, err
	}

	src, err := s.objRepo.GetByKey(ctx, tenantID, srcBucket, srcKey)
	if err != nil {
		op.fail(err)
		return nil, err
	}

	sm := domain.NewObjectFSM(src.Status)
	if err := sm.Fire(domain.EventObjectCopy); err != nil {
		op.fail(err)
		return nil, fmt.Errorf("invalid source state for copy: %w", err)
	}

	dstObjectKey := fmt.Sprintf("%s/%s/%s", tenantID, dstBucket, dstKey)

	if err := s.s3.CopyObject(ctx, src.ObjectKey, dstObjectKey); err != nil {
		op.fail(err)
		return nil, fmt.Errorf("s3 copy: %w", err)
	}

	labels := make(map[string]string)
	for k, v := range src.Labels {
		labels[k] = v
	}
	for k, v := range metadata {
		labels[k] = v
	}

	now := time.Now()
	dstObj := domain.Object{
		ID:          uuid.New(),
		TenantID:    tenantID,
		ObjectKey:   dstObjectKey,
		Bucket:      dstBucket,
		ContentType: src.ContentType,
		SizeBytes:   src.SizeBytes,
		Status:      src.Status,
		Labels:      labels,
		StoredETag:  src.StoredETag,
		Category:    dstBucket,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := s.objRepo.Create(ctx, dstObj); err != nil {
		op.fail(err)
		return nil, fmt.Errorf("create destination record: %w", err)
	}

	op.succeed()
	return &dstObj, nil
}

func (s *objectsService) MoveObject(ctx context.Context, tenantID, srcBucket, srcKey, dstBucket, dstKey string) (*domain.Object, error) {
	ctx, op := beginOp(ctx, "MoveObject", "move",
		attribute.String("tenant_id", tenantID),
		attribute.String("src_bucket", srcBucket),
		attribute.String("src_key", srcKey),
		attribute.String("dst_bucket", dstBucket),
		attribute.String("dst_key", dstKey),
	)
	defer op.end()

	dst, err := s.CopyObject(ctx, tenantID, srcBucket, srcKey, dstBucket, dstKey, nil)
	if err != nil {
		op.fail(err)
		return nil, err
	}

	src, err := s.objRepo.GetByKey(ctx, tenantID, srcBucket, srcKey)
	if err != nil {
		op.fail(err)
		return nil, fmt.Errorf("resolve source for delete: %w", err)
	}

	srcSM := domain.NewObjectFSM(src.Status)
	if err := srcSM.Fire(domain.EventObjectSoftDelete); err != nil {
		op.fail(err)
		return nil, fmt.Errorf("invalid source state for move: %w", err)
	}

	if _, err := s.objRepo.MarkSoftDeleted(ctx, tenantID, src.ID); err != nil {
		op.fail(err)
		return nil, fmt.Errorf("soft-delete source: %w", err)
	}

	op.succeed()
	return dst, nil
}

func (s *objectsService) ListParts(ctx context.Context, tenantID string, uploadID string) ([]domain.MultipartPart, error) {
	ctx, op := beginOp(ctx, "ListParts", "list_parts",
		attribute.String("tenant_id", tenantID),
		attribute.String("upload_id", uploadID),
	)
	defer op.end()

	ctx, cancel := context.WithTimeout(ctx, s.fastOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionRead); err != nil {
		op.fail(err)
		return nil, err
	}

	mp, err := s.multiRepo.GetByUploadID(ctx, tenantID, uploadID)
	if err != nil {
		op.fail(err)
		return nil, err
	}

	parts, err := s.multiRepo.ListParts(ctx, mp.ID)
	if err != nil {
		op.fail(err)
		return nil, err
	}

	op.succeed()
	return parts, nil
}
