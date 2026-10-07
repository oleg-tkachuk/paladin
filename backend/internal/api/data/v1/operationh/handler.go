// Package operation implements the OperationService business logic.
//
// Long-running operations follow AIP-151: a client submits an async task
// (BatchDelete, BatchCopy, …) and polls GetOperation until done. The
// metadata/response fields carry proto-Any bytes — decoded at the Connect
// adapter layer, never parsed here.
package operationh

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	celpkg "github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
	"github.com/oleg-tkachuk/paladin/capability"
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
	// filter is the caller's CEL expression. The repo pushes its
	// SQL-expressible conjuncts into the query; ListOperations still
	// evaluates the whole expression over the returned page, so pushdown may
	// only narrow the candidate set.
	// newestFirst flips the sort and the cursor comparison together. A caller
	// showing current activity wants the newest rows; an ascending page of 50
	// is the 50 oldest, which is how a three-day-old failure came to be the
	// console's idea of what is running.
	List(ctx context.Context, tenantID uuid.UUID, state *State, afterID uuid.UUID, pageSize int32, filter string, newestFirst bool) ([]Operation, string, error)

	// ClaimNext is the worker-side atomic dequeue: pick the oldest
	// PENDING row, flip it to RUNNING, return it. Returns
	// ErrNoOperationToClaim when nothing's pending — workers loop on
	// a ticker and treat it as "nothing to do".
	ClaimNext(ctx context.Context) (Operation, error)
}

type Handler struct {
	repo   Repository
	policy cedar.Authorizer
	// cel compiles and caches List filters (program cache only).
	cel *celpkg.Evaluator
}

func NewHandler(repo Repository, policy cedar.Authorizer) *Handler {
	if policy == nil {
		panic("operation: policy authorizer is required")
	}
	return &Handler{cel: celpkg.NewEvaluator(), repo: repo, policy: policy}
}

// Each RPC also asserts a capability op, because a capability alone can
// authenticate a data-plane call: reading an operation is get, listing them
// is list, and cancelling one is manage — it stops work some other caller may
// have started. None can name a resource URI: an operation spans the
// collections its batch touched, and its metadata names them. So a
// resource-restricted capability is refused, as it is for the batch RPCs that
// create operations.

// authorize gates operation RPCs against Cedar with the operation's
// tenant_id (an operation is owned by the tenant whose principal spawned
// it; cross-tenant Get/Cancel is denied at the repo layer too).
func (h *Handler) authorize(ctx context.Context, action cedar.Action, tenantID uuid.UUID) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipal(p),
		action,
		&cedar.Resource{TenantID: tenantID},
		cedar.RequestContext{Now: time.Now()},
	)
	if err != nil {
		return apiutil.MapError(fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied, "denied by policy")
	}
	return nil
}

func (h *Handler) GetOperation(ctx context.Context, opID uuid.UUID) (*Operation, error) {
	tenantID, err := auth.TenantFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	if err := auth.AssertCapabilityOp(ctx, capability.OpGet, ""); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, cedar.ActionReadOperation, tenantID); err != nil {
		return nil, err
	}
	op, err := h.repo.Get(ctx, opID, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err.Error()).WithCause(err)
	}
	return &op, nil
}

func (h *Handler) CancelOperation(ctx context.Context, opID uuid.UUID) error {
	tenantID, err := auth.TenantFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	if err := auth.AssertCapabilityOp(ctx, capability.OpManage, ""); err != nil {
		return err
	}
	if err := h.authorize(ctx, cedar.ActionCancelOperation, tenantID); err != nil {
		return err
	}
	if err := h.repo.Cancel(ctx, opID, tenantID); err != nil {
		return rpcerr.New(connect.CodeFailedPrecondition, fmt.Errorf("operation not cancellable: %w", err))
	}
	return nil
}

// ListOperations returns one page of the caller tenant's operations. filter is
// an optional CEL expression over OperationSchema, applied after the fetch;
// the repo cursor is returned unchanged so paging survives a page whose rows
// all fail the predicate.
func (h *Handler) ListOperations(ctx context.Context, state *State, pageSize int32, pageToken, filter string, newestFirst bool) ([]Operation, string, error) {
	tenantID, err := auth.TenantFromContext(ctx)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	if err := auth.AssertCapabilityOp(ctx, capability.OpList, ""); err != nil {
		return nil, "", err
	}
	if err := h.authorize(ctx, cedar.ActionReadOperation, tenantID); err != nil {
		return nil, "", err
	}
	var afterID uuid.UUID
	if pageToken != "" {
		id, err := uuid.Parse(pageToken)
		if err != nil {
			return nil, "", rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid page_token: %w", err))
		}
		afterID = id
	}
	page, next, err := h.repo.List(ctx, tenantID, state, afterID, pageSize, filter, newestFirst)
	if err != nil {
		return nil, "", err
	}
	page, err = celpkg.FilterPage(h.cel, celpkg.OperationSchema, filter, page, operationRow)
	if err != nil {
		return nil, "", rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("filter: %w", err))
	}
	return page, next, nil
}

// operationRow projects an Operation onto the variables OperationSchema
// declares. `done` is derived from DoneAt rather than stored, matching what
// the proto reports.
func operationRow(o Operation) map[string]any {
	return map[string]any{
		"operation_id":  o.OperationID.String(),
		"type":          o.Type,
		"state":         string(o.State),
		"done":          o.DoneAt != nil,
		"tenant_id":     o.TenantID.String(),
		"error_code":    o.ErrorCode,
		"error_message": o.ErrorMessage,
		"created_at":    o.CreatedAt,
		"updated_at":    o.UpdatedAt,
	}
}

// Submit records a new operation row. Called by Batch handlers before they
// enqueue the actual work. Not exposed as an RPC directly.
func (h *Handler) Submit(ctx context.Context, opType string, metadata []byte) (uuid.UUID, error) {
	tenantID, err := auth.TenantFromContext(ctx)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	op := Operation{
		OperationID: uuid.Must(uuid.NewV7()),
		TenantID:    tenantID,
		Type:        opType,
		State:       StatePending,
		Metadata:    metadata,
	}
	if err := h.repo.Create(ctx, op); err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return op.OperationID, nil
}

// ErrNotCancellable is surfaced by repositories when the operation is in a
// terminal state and cannot be cancelled.
var ErrNotCancellable = errors.New("operation not in cancellable state")

// ErrOperationFinished is surfaced by UpdateState when the operation is not
// found or already in a terminal state — cancelled, or reclaimed as lost —
// so the write was not applied.
var ErrOperationFinished = errors.New("operation not found or already finished")

// ErrNoOperationToClaim is the typed sentinel returned by ClaimNext on
// the worker side when nothing is PENDING. Worker loops gate on it as
// the cheap "nothing to do; sleep until next tick" signal — distinct
// from a real error.
var ErrNoOperationToClaim = errors.New("no PENDING operation to claim")
