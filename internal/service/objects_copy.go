package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/metrics"
)

// copyObject performs a server-side S3 copy and creates a new DB record for the destination.
func (s *objectsService) copyObject(ctx context.Context, tenantID, srcBucket, srcKey, dstBucket, dstKey string, metadata map[string]string) (*domain.Object, error) {
	ctx, span := otel.Tracer(TracerName).Start(ctx, "CopyObject")
	defer span.End()
	span.SetAttributes(
		attribute.String("tenant_id", tenantID),
		attribute.String("src_bucket", srcBucket),
		attribute.String("src_key", srcKey),
		attribute.String("dst_bucket", dstBucket),
		attribute.String("dst_key", dstKey),
	)

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOp(ctx, "copy", status, start) }()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionRead); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return nil, err
	}

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionCreate); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return nil, err
	}

	// Resolve source object
	src, err := s.objRepo.GetByKey(ctx, tenantID, srcBucket, srcKey)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return nil, err
	}

	// FSM: validate source is in a copyable state
	sm := domain.NewObjectFSM(src.Status)
	if err := sm.Fire(domain.EventObjectCopy); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return nil, fmt.Errorf("invalid source state for copy: %w", err)
	}

	// Build the full S3 key for the destination
	dstObjectKey := fmt.Sprintf("%s/%s/%s", tenantID, dstBucket, dstKey)

	// Server-side S3 copy
	if err := s.s3.CopyObject(ctx, src.ObjectKey, dstObjectKey); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return nil, fmt.Errorf("s3 copy: %w", err)
	}

	// Merge metadata: start with source labels, overlay with provided metadata
	labels := make(map[string]string)
	for k, v := range src.Labels {
		labels[k] = v
	}
	for k, v := range metadata {
		labels[k] = v
	}

	// Create destination DB record
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
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return nil, fmt.Errorf("create destination record: %w", err)
	}

	status = "success"
	span.SetStatus(codes.Ok, "")

	return &dstObj, nil
}

// moveObject performs a copy followed by a soft delete of the source.
func (s *objectsService) moveObject(ctx context.Context, tenantID, srcBucket, srcKey, dstBucket, dstKey string) (*domain.Object, error) {
	ctx, span := otel.Tracer(TracerName).Start(ctx, "MoveObject")
	defer span.End()
	span.SetAttributes(
		attribute.String("tenant_id", tenantID),
		attribute.String("src_bucket", srcBucket),
		attribute.String("src_key", srcKey),
		attribute.String("dst_bucket", dstBucket),
		attribute.String("dst_key", dstKey),
	)

	start := time.Now()
	var status string
	defer func() { metrics.RecordObjectOp(ctx, "move", status, start) }()

	// Copy first
	dst, err := s.copyObject(ctx, tenantID, srcBucket, srcKey, dstBucket, dstKey, nil)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return nil, err
	}

	// Resolve source to delete
	src, err := s.objRepo.GetByKey(ctx, tenantID, srcBucket, srcKey)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return nil, fmt.Errorf("resolve source for delete: %w", err)
	}

	// FSM: validate source can be soft-deleted
	srcSM := domain.NewObjectFSM(src.Status)
	if err := srcSM.Fire(domain.EventObjectSoftDelete); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return nil, fmt.Errorf("invalid source state for move: %w", err)
	}

	// Soft-delete source
	if _, err := s.objRepo.MarkSoftDeleted(ctx, tenantID, src.ID); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		status = "error"

		return nil, fmt.Errorf("soft-delete source: %w", err)
	}

	status = "success"
	span.SetStatus(codes.Ok, "")

	return dst, nil
}

// listParts retrieves uploaded parts for a multipart upload.
func (s *objectsService) listParts(ctx context.Context, tenantID string, uploadID string) ([]domain.MultipartPart, error) {
	ctx, span := otel.Tracer(TracerName).Start(ctx, "ListParts")
	defer span.End()
	span.SetAttributes(
		attribute.String("tenant_id", tenantID),
		attribute.String("upload_id", uploadID),
	)

	ctx, cancel := context.WithTimeout(ctx, s.fastOperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionRead); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())

		return nil, err
	}

	mp, err := s.multiRepo.GetByUploadID(ctx, tenantID, uploadID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())

		return nil, err
	}

	parts, err := s.multiRepo.ListParts(ctx, mp.ID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())

		return nil, err
	}

	span.SetStatus(codes.Ok, "")

	return parts, nil
}
