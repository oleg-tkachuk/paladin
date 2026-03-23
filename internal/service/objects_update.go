package service

import (
	"context"
	"fmt"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/logger"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

func (s *objectsService) UpdateStatus(ctx context.Context, tenantID string, id openapi_types.UUID, status string, idempotencyKey *string) error {
	ctx, op := beginOp(ctx, OpUpdateStatus, "update_status",
		attribute.String("tenant_id", tenantID),
		attribute.String("object_id", id.String()),
		attribute.String("status", status),
	)
	defer op.end()

	// Idempotency check
	if idempotencyKey != nil && *idempotencyKey != "" {
		op.addAttrs(attribute.String("idempotency_key", *idempotencyKey))
		cached, err := s.idemRepo.Get(ctx, tenantID, *idempotencyKey)
		if err != nil {
			logger.FromContext(ctx).Warn("Failed to check idempotency key", zap.Error(err))
		} else if cached != nil {
			if cached.ResponseCode >= 200 && cached.ResponseCode < 300 {
				return nil
			}
		}
	}

	var opErr error

	// Store idempotency result on exit
	defer func() {
		if idempotencyKey != nil && *idempotencyKey != "" {
			respCode := 200
			if opErr != nil {
				respCode = 500
			}
			if err := s.idemRepo.Save(ctx, domain.IdempotencyRecord{
				TenantID:     tenantID,
				Key:          *idempotencyKey,
				RequestPath:  "PATCH",
				ResponseCode: respCode,
				ExpiresAt:    time.Now().Add(s.idempotencyTTL),
			}); err != nil {
				s.log.Warn("save idempotency record failed", zap.Error(err))
			}
		}
	}()

	ctx, cancel := context.WithTimeout(ctx, s.fastOperationTimeout)
	defer cancel()

	action := domain.ActionUpdate
	if status == string(domain.ObjectSoftDeleted) {
		action = domain.ActionDelete
	}

	if opErr = s.policy.Authorize(ctx, tenantID, action); opErr != nil {
		op.fail(opErr)
		return opErr
	}

	obj, err := s.objRepo.Get(ctx, tenantID, id)
	if err != nil {
		opErr = err
		op.fail(err)
		return err
	}

	// FSM State Transition
	sm := domain.NewObjectFSM(obj.Status)
	var event domain.ObjectEvent

	switch status {
	case "aborted":
		event = domain.EventObjectAbort
	case "error":
		event = domain.EventObjectFail
	default:
		opErr = fmt.Errorf("unsupported status update: %s", status)
		op.fail(opErr)
		return opErr
	}

	if err = sm.Fire(event); err != nil {
		opErr = fmt.Errorf("%s: %w", ErrInvalidTransition, err)
		op.failStatus(err, domain.StatusConflict)
		return opErr
	}

	state, _ := sm.State(ctx)
	if state == obj.Status {
		op.succeed()
		return nil
	}

	if event == domain.EventObjectAbort || event == domain.EventObjectFail {
		if _, err = s.objRepo.UpdateStatus(ctx, tenantID, id, status); err != nil {
			opErr = err
			op.fail(err)
			return err
		}
	}

	logger.FromContext(ctx).Info(LogObjectStatusUpdated,
		zap.String("tenant_id", tenantID),
		zap.String("object_id", id.String()),
		zap.String("status", status))

	op.succeed()
	return nil
}
