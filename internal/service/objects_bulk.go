package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"go.opentelemetry.io/otel/attribute"
)

func (s *objectsService) BulkCreate(ctx context.Context, tenantID string, items []domain.CreateObjectRequest, idempotencyKey *string) ([]domain.CreateObjectResponse, error) {
	ctx, op := beginOp(ctx, "BulkCreate", "bulk_create",
		attribute.String("tenant_id", tenantID),
		attribute.Int("items_count", len(items)),
	)
	defer op.end()

	res := make([]domain.CreateObjectResponse, 0, len(items))
	for _, item := range items {
		out, err := s.CreateSingle(ctx, tenantID, item.Category, item.ContentType, item.SizeBytes, item.Labels, item.Tags, item.ExternalRef, 0, nil)
		if err != nil {
			op.fail(err)
			return nil, fmt.Errorf("bulk create failed at item category %s: %w", item.Category, err)
		}
		res = append(res, out)
	}

	op.succeed()
	return res, nil
}

func (s *objectsService) BulkSignUploads(ctx context.Context, tenantID string, items []domain.SignUploadItem) ([]domain.CreateObjectResponse, error) {
	ctx, op := beginOp(ctx, "BulkSignUploads", "bulk_sign_uploads",
		attribute.String("tenant_id", tenantID),
		attribute.Int("items_count", len(items)),
	)
	defer op.end()

	res := make([]domain.CreateObjectResponse, 0, len(items))
	for _, item := range items {
		obj, err := s.objRepo.Get(ctx, tenantID, item.ObjectID)
		if err != nil {
			return nil, fmt.Errorf("bulk sign upload failed getting object %s: %w", item.ObjectID, err)
		}

		signed, err := s.SignUpload(ctx, tenantID, item.ObjectID, item.UploadTTL)
		if err != nil {
			return nil, fmt.Errorf("bulk sign upload failed signing %s: %w", item.ObjectID, err)
		}

		res = append(res, domain.CreateObjectResponse{
			ID:       obj.ID,
			Key:      obj.ObjectKey,
			Bucket:   obj.Bucket,
			Category: obj.Category,
			Upload:   signed,
		})
	}

	op.succeed()
	return res, nil
}

func (s *objectsService) BulkComplete(ctx context.Context, tenantID string, ids []uuid.UUID) ([]*domain.Object, error) {
	ctx, op := beginOp(ctx, "BulkComplete", "bulk_complete",
		attribute.String("tenant_id", tenantID),
		attribute.Int("ids_count", len(ids)),
	)
	defer op.end()

	res := make([]*domain.Object, 0, len(ids))
	for _, id := range ids {
		obj, err := s.CompleteObject(ctx, tenantID, id, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("bulk complete failed for object %s: %w", id.String(), err)
		}
		res = append(res, obj)
	}

	op.succeed()
	return res, nil
}

func (s *objectsService) BulkPatch(ctx context.Context, tenantID string, items []domain.BulkPatchItem, idempotencyKey *string) (int64, error) {
	ctx, op := beginOp(ctx, "BulkPatch", "bulk_patch",
		attribute.String("tenant_id", tenantID),
		attribute.Int("items_count", len(items)),
	)
	defer op.end()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionPatch); err != nil {
		op.fail(err)
		return 0, err
	}

	count, err := s.objRepo.BulkPatch(ctx, tenantID, items)
	if err != nil {
		op.fail(err)
		return 0, err
	}

	op.succeed()
	return count, nil
}
