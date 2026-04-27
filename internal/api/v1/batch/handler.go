// Package batch implements the BatchService business logic.
//
// Batch RPCs return a long-running Operation (AIP-151). The actual work
// happens in a background worker consuming the operations table. Handlers
// validate, authorize, and enqueue — they never block on backend calls.
package batch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/operation"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

type BatchDeleteArgs struct {
	TenantID  uuid.UUID
	ObjectKey string
	ObjectIDs []uuid.UUID
}

type BatchCopyArgs struct {
	TenantID  uuid.UUID
	SrcBucket string
	DstBucket string
	ObjectIDs []uuid.UUID
	KeyPrefix string // optional destination prefix
}

type BatchUpdateTagsArgs struct {
	TenantID  uuid.UUID
	ObjectKey string
	ObjectIDs []uuid.UUID
	Tags      map[string]string
}

// Submitter records LROs. Provided by the operation package.
type Submitter interface {
	Submit(ctx context.Context, opType string, metadata []byte) (uuid.UUID, error)
}

type Handler struct {
	submitter Submitter
	policy    *cedar.Engine
}

func NewHandler(submitter Submitter, policy *cedar.Engine) *Handler {
	return &Handler{submitter: submitter, policy: policy}
}

// BatchDelete validates and enqueues an async delete across up to 10k objects.
// Returns the operation_id to poll via GetOperation.
func (h *Handler) BatchDelete(ctx context.Context, args BatchDeleteArgs) (uuid.UUID, error) {
	tenantID, p, err := callerContext(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	args.TenantID = tenantID
	if len(args.ObjectIDs) == 0 {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("object_ids must be non-empty"))
	}
	if len(args.ObjectIDs) > maxBatchSize {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("batch too large: %d > %d", len(args.ObjectIDs), maxBatchSize))
	}
	// ObjectKey-level authorization. Per-object authorization happens inside
	// the worker on each row (slower but safer).
	if err := h.authorize(ctx, p, tenantID, args.ObjectKey, cedar.ActionDeleteObject); err != nil {
		return uuid.Nil, err
	}
	md, _ := json.Marshal(args)
	return h.submitter.Submit(ctx, "BatchDelete", md)
}

func (h *Handler) BatchCopy(ctx context.Context, args BatchCopyArgs) (uuid.UUID, error) {
	tenantID, p, err := callerContext(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	args.TenantID = tenantID
	if len(args.ObjectIDs) == 0 {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("object_ids must be non-empty"))
	}
	if len(args.ObjectIDs) > maxBatchSize {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("batch too large: %d > %d", len(args.ObjectIDs), maxBatchSize))
	}
	// Require copy on source and put on destination — objectKey-level check.
	if err := h.authorize(ctx, p, tenantID, args.SrcBucket, cedar.ActionCopyObject); err != nil {
		return uuid.Nil, err
	}
	if err := h.authorize(ctx, p, tenantID, args.DstBucket, cedar.ActionPutObject); err != nil {
		return uuid.Nil, err
	}
	md, _ := json.Marshal(args)
	return h.submitter.Submit(ctx, "BatchCopy", md)
}

func (h *Handler) BatchUpdateTags(ctx context.Context, args BatchUpdateTagsArgs) (uuid.UUID, error) {
	tenantID, p, err := callerContext(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	args.TenantID = tenantID
	if len(args.ObjectIDs) == 0 {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("object_ids must be non-empty"))
	}
	if err := h.authorize(ctx, p, tenantID, args.ObjectKey, cedar.ActionUpdateObject); err != nil {
		return uuid.Nil, err
	}
	md, _ := json.Marshal(args)
	return h.submitter.Submit(ctx, "BatchUpdateTags", md)
}

func (h *Handler) authorize(ctx context.Context, p *auth.Principal, tenantID uuid.UUID, objectKey, action string) error {
	decision, err := h.policy.IsAuthorized(ctx,
		&cedar.Principal{Subject: p.Subject, TenantID: tenantID, Roles: p.Roles},
		action,
		&cedar.Resource{TenantID: tenantID, ObjectKey: objectKey},
		cedar.RequestContext{Now: time.Now()},
	)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied, errors.New("denied by policy"))
	}
	return nil
}

func callerContext(ctx context.Context) (uuid.UUID, *auth.Principal, error) {
	t, err := auth.TenantFromContext(ctx)
	if err != nil {
		return uuid.Nil, nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return uuid.Nil, nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	return t, p, nil
}

// maxBatchSize caps the number of objects per BatchXxx call.
const maxBatchSize = 10_000

// Compile-time check: the operation package provides a compatible Submitter.
var _ Submitter = (*operation.Handler)(nil)
