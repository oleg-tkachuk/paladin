// Package operation implements the OperationService business logic.
//
// Long-running operations follow AIP-151: a client submits an async task
// (BatchDelete, BatchCopy, …) and polls GetOperation until done. The
// metadata/response fields carry proto-Any bytes — decoded at the Connect
// adapter layer, never parsed here.
package operation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

// State mirrors the operation_state SQL enum.
type State string

const (
	StatePending   State = "PENDING"
	StateRunning   State = "RUNNING"
	StateSucceeded State = "SUCCEEDED"
	StateFailed    State = "FAILED"
	StateCancelled State = "CANCELLED"
)

type Operation struct {
	OperationID  uuid.UUID
	TenantID     uuid.UUID
	Type         string
	State        State
	Metadata     []byte
	Response     []byte
	ErrorCode    string
	ErrorMessage string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DoneAt       *time.Time
}

type Repository interface {
	Create(ctx context.Context, op Operation) error
	Get(ctx context.Context, opID, tenantID uuid.UUID) (Operation, error)
	UpdateState(ctx context.Context, opID uuid.UUID, newState State, metadata, response []byte, errCode, errMsg string) error
	Cancel(ctx context.Context, opID, tenantID uuid.UUID) error
	List(ctx context.Context, tenantID uuid.UUID, state *State, afterID uuid.UUID, pageSize int32) ([]Operation, string, error)
}

type Handler struct {
	repo   Repository
	policy cedar.Authorizer
}

func NewHandler(repo Repository, policy cedar.Authorizer) *Handler {
	if policy == nil {
		panic("operation: policy authorizer is required")
	}
	return &Handler{repo: repo, policy: policy}
}

// authorize gates operation RPCs against Cedar with the operation's
// tenant_id (an operation is owned by the tenant whose principal spawned
// it; cross-tenant Get/Cancel is denied at the repo layer too).
func (h *Handler) authorize(ctx context.Context, action string, tenantID uuid.UUID) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	decision, err := h.policy.IsAuthorized(ctx,
		&cedar.Principal{Subject: p.Subject, TenantID: p.TenantID, Roles: p.Roles},
		action,
		&cedar.Resource{TenantID: tenantID},
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

func (h *Handler) GetOperation(ctx context.Context, opID uuid.UUID) (*Operation, error) {
	tenantID, err := auth.TenantFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	if err := h.authorize(ctx, cedar.ActionReadOperation, tenantID); err != nil {
		return nil, err
	}
	op, err := h.repo.Get(ctx, opID, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return &op, nil
}

func (h *Handler) CancelOperation(ctx context.Context, opID uuid.UUID) error {
	tenantID, err := auth.TenantFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	if err := h.authorize(ctx, cedar.ActionCancelOperation, tenantID); err != nil {
		return err
	}
	if err := h.repo.Cancel(ctx, opID, tenantID); err != nil {
		return connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("operation not cancellable: %w", err))
	}
	return nil
}

func (h *Handler) ListOperations(ctx context.Context, state *State, pageSize int32, pageToken string) ([]Operation, string, error) {
	tenantID, err := auth.TenantFromContext(ctx)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeUnauthenticated, err)
	}
	if err := h.authorize(ctx, cedar.ActionReadOperation, tenantID); err != nil {
		return nil, "", err
	}
	var afterID uuid.UUID
	if pageToken != "" {
		id, err := uuid.Parse(pageToken)
		if err != nil {
			return nil, "", connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("invalid page_token: %w", err))
		}
		afterID = id
	}
	return h.repo.List(ctx, tenantID, state, afterID, pageSize)
}

// Submit records a new operation row. Called by Batch handlers before they
// enqueue the actual work. Not exposed as an RPC directly.
func (h *Handler) Submit(ctx context.Context, opType string, metadata []byte) (uuid.UUID, error) {
	tenantID, err := auth.TenantFromContext(ctx)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	op := Operation{
		OperationID: uuid.Must(uuid.NewV7()),
		TenantID:    tenantID,
		Type:        opType,
		State:       StatePending,
		Metadata:    metadata,
	}
	if err := h.repo.Create(ctx, op); err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInternal, err)
	}
	return op.OperationID, nil
}

// ErrNotCancellable is surfaced by repositories when the operation is in a
// terminal state and cannot be cancelled.
var ErrNotCancellable = errors.New("operation not in cancellable state")
