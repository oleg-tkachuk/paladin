package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

func (s *objectsService) BulkCreate(ctx context.Context, tenantID string, items []domain.CreateObjectRequest, idempotencyKey *string) ([]domain.CreateObjectResponse, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "BulkCreate")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.Int("items_count", len(items)))

	// Simple sequential implementation for now, can be optimized later
	res := make([]domain.CreateObjectResponse, 0, len(items))
	for _, item := range items {
		out, err := s.createSingle(ctx, tenantID, item.Category, item.ContentType, item.SizeBytes, item.Labels, item.Tags, item.ExternalRef, 0, nil)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())

			return nil, fmt.Errorf("bulk create failed at item: %w", err)
		}
		res = append(res, out)
	}

	span.SetStatus(codes.Ok, "")

	return res, nil
}

func (s *objectsService) BulkSignUploads(ctx context.Context, tenantID string, items []domain.SignUploadItem) ([]domain.CreateObjectResponse, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "BulkSignUploads")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.Int("items_count", len(items)))

	res := make([]domain.CreateObjectResponse, 0, len(items))
	for _, item := range items {
		obj, err := s.objRepo.Get(ctx, tenantID, item.ObjectID)
		if err != nil {
			return nil, fmt.Errorf("get object %s: %w", item.ObjectID, err)
		}

		signed, err := s.signUpload(ctx, tenantID, item.ObjectID, item.UploadTTL)
		if err != nil {
			return nil, fmt.Errorf("sign upload %s: %w", item.ObjectID, err)
		}

		res = append(res, domain.CreateObjectResponse{
			ID:       obj.ID,
			Key:      obj.ObjectKey,
			Bucket:   obj.Bucket,
			Category: obj.Category,
			Upload:   signed,
		})
	}

	span.SetStatus(codes.Ok, "")

	return res, nil
}

func (s *objectsService) BulkComplete(ctx context.Context, tenantID string, ids []uuid.UUID) ([]*domain.Object, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "BulkComplete")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.Int("ids_count", len(ids)))

	res := make([]*domain.Object, 0, len(ids))
	for _, id := range ids {
		obj, err := s.completeObject(ctx, tenantID, id, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("complete object %s: %w", id, err)
		}
		res = append(res, obj)
	}

	span.SetStatus(codes.Ok, "")

	return res, nil
}

func (s *objectsService) BulkPatch(ctx context.Context, tenantID string, items []domain.BulkPatchItem, idempotencyKey *string) (int64, error) {
	ctx, span := otel.Tracer("object-service").Start(ctx, "BulkPatch")
	defer span.End()
	span.SetAttributes(attribute.String("tenant_id", tenantID), attribute.Int("items_count", len(items)))

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionPatch); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())

		return 0, err
	}

	count, err := s.objRepo.BulkPatch(ctx, tenantID, items)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())

		return 0, err
	}

	span.SetStatus(codes.Ok, "")

	return count, nil
}
