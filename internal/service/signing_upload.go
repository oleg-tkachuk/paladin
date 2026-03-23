package service

import (
	"context"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/domain"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"go.opentelemetry.io/otel/attribute"
)

func (s *objectsService) SignUpload(ctx context.Context, tenantID string, id openapi_types.UUID, uploadTTL int) (domain.Presigned, error) {
	ctx, op := beginOp(ctx, "SignUpload", "sign_upload",
		attribute.String("tenant_id", tenantID),
		attribute.String("object_id", id.String()),
	)
	defer op.end()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionUpdate); err != nil {
		op.fail(err)
		return domain.Presigned{}, err
	}

	rec, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		op.fail(err)
		return domain.Presigned{}, err
	}

	ttl := time.Duration(uploadTTL) * time.Second
	if ttl == 0 {
		ttl = s.s3.PresignTTLDuration()
	}

	presigned, err := executeWithBreakerRet(s.brk, "s3_presign", func() (domain.Presigned, error) {
		return s.s3.PresignPutObject(ctx, rec.ObjectKey, rec.ContentType, rec.SizeBytes, ttl)
	})
	if err != nil {
		op.fail(err)
		return domain.Presigned{}, err
	}

	op.succeed()
	return presigned, nil
}
