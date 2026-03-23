package service

import (
	"context"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/errors"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"go.opentelemetry.io/otel/attribute"
)

func (s *objectsService) SignDownload(ctx context.Context, tenantID string, id openapi_types.UUID, downloadTTL int) (domain.Presigned, error) {
	ctx, op := beginOp(ctx, "SignDownload", "sign_download",
		attribute.String("tenant_id", tenantID),
		attribute.String("object_id", id.String()),
	)
	defer op.end()

	ctx, cancel := context.WithTimeout(ctx, s.s3OperationTimeout)
	defer cancel()

	if err := s.policy.Authorize(ctx, tenantID, domain.ActionRead); err != nil {
		op.fail(err)
		return domain.Presigned{}, err
	}

	rec, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		op.fail(err)
		return domain.Presigned{}, err
	}

	if rec.Status != domain.ObjectComplete {
		err := errors.Conflict("object not complete", nil)
		op.fail(err)
		return domain.Presigned{}, err
	}

	ttl := time.Duration(downloadTTL) * time.Second
	if ttl == 0 {
		ttl = s.s3.PresignTTLDuration()
	}

	presigned, err := executeWithBreakerRet(s.brk, "s3_presign", func() (domain.Presigned, error) {
		return s.s3.PresignGetObject(ctx, rec.ObjectKey, ttl)
	})
	if err != nil {
		op.fail(err)
		return domain.Presigned{}, err
	}

	op.succeed()
	return presigned, nil
}
