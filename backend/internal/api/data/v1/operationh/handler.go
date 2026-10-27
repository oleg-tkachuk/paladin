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
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	celpkg "github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
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
	OperationID uuid.UUID
	TenantID    uuid.UUID
	// InitiatorSubject is the subject of the token that submitted it; "" for
	// an operation from before initiators were recorded.
	InitiatorSubject string
	// DismissedAt is when the initiator cleared it from their own list.
	DismissedAt *time.Time
	// ExpireAt is when the housekeeping reaper removes it (expiresAt); nil
	// while it runs and while finished operations are kept indefinitely.
	ExpireAt *time.Time
	Type     string
	State    State
	// Metadata is the executor's arguments, JSON, written once at creation.
	Metadata []byte
	// Progress is the latest {processed, total} snapshot the worker reported,
	// JSON; nil until the first report. It is what the API calls metadata
	// (AIP-151: an operation's metadata is its progress).
	Progress []byte
	Response []byte
	// FailedCount is how many objects a batch could not act on: one that
	// runs to its end succeeds whatever it could not do, so this is what
	// tells one that left objects behind from a clean one.
	FailedCount  int64
	ErrorCode    string
	ErrorMessage string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DoneAt       *time.Time
}

type Repository interface {
	Create(ctx context.Context, op Operation) error
	Get(ctx context.Context, opID, tenantID uuid.UUID) (Operation, error)
	// UpdateState moves a PENDING or RUNNING operation to newState. It never
	// touches the arguments (Metadata) or Progress. failedCount is how many
	// objects a batch's result says it could not act on; 0 for any other.
	UpdateState(ctx context.Context, opID uuid.UUID, newState State, response []byte, failedCount int64, errCode, errMsg string) error
	// ReportProgress records a RUNNING operation's latest progress snapshot;
	// ErrOperationFinished once it is no longer RUNNING.
	ReportProgress(ctx context.Context, opID uuid.UUID, progress []byte) error
	Cancel(ctx context.Context, opID, tenantID uuid.UUID) error
	// filter is the caller's CEL expression. The repo pushes its
	// SQL-expressible conjuncts into the query; ListOperations still
	// evaluates the whole expression over the returned page, so pushdown may
	// only narrow the candidate set.
	// newestFirst flips the sort and the cursor comparison together. A caller
	// showing current activity wants the newest rows; an ascending page of 50
	// is the 50 oldest, which is how a three-day-old failure came to be the
	// console's idea of what is running.
	List(ctx context.Context, args ListArgs) ([]Operation, string, error)
	// CountFacets counts the operations List would list for args, without
	// its cursor or its page size, by each facet's values: each facet
	// counted without its own choices in args.Facets.
	CountFacets(ctx context.Context, args ListArgs) ([]FacetCount, error)
	// Dismiss clears the finished operations of initiator in tenantID from
	// their own list — those of ids, or every one with ids empty — and
	// reports how many it cleared.
	Dismiss(ctx context.Context, tenantID uuid.UUID, initiator string, ids []uuid.UUID) (int64, error)

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
	// finishedRetention is worker.jobs.housekeeping.operations_ttl: how long
	// the reaper keeps a finished operation. Zero keeps them indefinitely.
	finishedRetention time.Duration
}

// SetFinishedRetention sets how long a finished operation is kept before the
// housekeeping reaper removes it, which each operation reports as ExpireAt.
func (h *Handler) SetFinishedRetention(d time.Duration) { h.finishedRetention = d }

// expiresAt is when the reaper removes an operation that finished at doneAt:
// nil while it runs, and when retention keeps finished ones indefinitely.
func expiresAt(doneAt *time.Time, retention time.Duration) *time.Time {
	if doneAt == nil || retention <= 0 {
		return nil
	}
	t := doneAt.Add(retention)
	return &t
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
	if err := auth.AssertCapabilityOp(ctx, limes.OpGet, ""); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, cedar.ActionReadOperation, tenantID); err != nil {
		return nil, err
	}
	op, err := h.repo.Get(ctx, opID, tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err.Error()).WithCause(err)
	}
	op.ExpireAt = expiresAt(op.DoneAt, h.finishedRetention)
	return &op, nil
}

func (h *Handler) CancelOperation(ctx context.Context, opID uuid.UUID) error {
	tenantID, err := auth.TenantFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	if err := auth.AssertCapabilityOp(ctx, limes.OpManage, ""); err != nil {
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

// listScope is what a listing of in may read: the tenant, and the initiator
// when it lists only the caller's own — refused when the caller may not
// list them.
func (h *Handler) listScope(ctx context.Context, in ListInput) (uuid.UUID, string, error) {
	tenantID, err := auth.TenantFromContext(ctx)
	if err != nil {
		return uuid.Nil(), "", connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	if in.TenantID != uuid.Nil() && in.TenantID != tenantID {
		if !apiutil.HasRole(ctx, apiutil.RolePlatformAdmin) {
			return uuid.Nil(), "", connect.NewError(connect.CodePermissionDenied, msgCrossTenant)
		}
		tenantID = in.TenantID
	}
	var initiator string
	if in.Mine {
		if initiator, err = callerSubject(ctx); err != nil {
			return uuid.Nil(), "", err
		}
	}
	if err := auth.AssertCapabilityOp(ctx, limes.OpList, ""); err != nil {
		return uuid.Nil(), "", err
	}
	if err := h.authorize(ctx, cedar.ActionReadOperation, tenantID); err != nil {
		return uuid.Nil(), "", err
	}
	return tenantID, initiator, nil
}

// CountOperationFacets counts the operations ListOperations would list for
// in by each facet's values (FacetState, FacetType), each facet without its
// own choices. Counted over what the filter pushes down: the conjuncts it
// cannot are applied to each page after, so a count can exceed the rows the
// pages then hold.
func (h *Handler) CountOperationFacets(ctx context.Context, in ListInput) ([]FacetCount, error) {
	tenantID, initiator, err := h.listScope(ctx, in)
	if err != nil {
		return nil, err
	}
	return h.repo.CountFacets(ctx, ListArgs{
		TenantID: tenantID, State: in.State, Filter: in.Filter, Facets: in.Facets,
		Initiator: initiator, HideDismissed: in.Mine && !in.IncludeDismissed,
	})
}

// ListOperations returns one page of the caller tenant's operations, or with
// mine only those the caller started. filter is an optional CEL expression
// over OperationSchema, applied after the fetch; the repo cursor is returned
// unchanged so paging survives a page whose rows all fail the predicate.
func (h *Handler) ListOperations(ctx context.Context, in ListInput) ([]Operation, string, error) {
	state, pageSize, pageToken, filter, newestFirst := in.State, in.PageSize, in.PageToken, in.Filter, in.NewestFirst
	tenantID, initiator, err := h.listScope(ctx, in)
	if err != nil {
		return nil, "", err
	}
	mine := in.Mine
	var afterID uuid.UUID
	if pageToken != "" {
		id, err := uuid.Parse(pageToken)
		if err != nil {
			return nil, "", rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid page_token: %w", err))
		}
		afterID = id
	}
	page, next, err := h.repo.List(ctx, ListArgs{
		TenantID: tenantID, State: state, AfterID: afterID, PageSize: pageSize,
		Filter: filter, NewestFirst: newestFirst, Facets: in.Facets,
		Initiator: initiator, HideDismissed: mine && !in.IncludeDismissed,
	})
	if err != nil {
		return nil, "", err
	}
	page, err = celpkg.FilterPage(h.cel, celpkg.OperationSchema, filter, page, operationRow)
	if err != nil {
		return nil, "", rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("filter: %w", err))
	}
	for i := range page {
		page[i].ExpireAt = expiresAt(page[i].DoneAt, h.finishedRetention)
	}
	return page, next, nil
}

// HasFailures reports whether o failed, or is a batch that could not act on
// some of its objects: what the filter's has_failures and the list's
// pushdown of it both mean.
func (o Operation) HasFailures() bool {
	return o.State == StateFailed || o.FailedCount > 0
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
		"failed_count":  o.FailedCount,
		"has_failures":  o.HasFailures(),
		"created_at":    o.CreatedAt,
		"updated_at":    o.UpdatedAt,
	}
}

// Submit records a new operation row. Called by Batch handlers before they
// enqueue the actual work. Not exposed as an RPC directly.
func (h *Handler) Submit(ctx context.Context, opType string, metadata []byte) (uuid.UUID, error) {
	tenantID, err := auth.TenantFromContext(ctx)
	if err != nil {
		return uuid.Nil(), connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	op := Operation{
		OperationID: uuid.NewV7(),
		TenantID:    tenantID,
		Type:        opType,
		State:       StatePending,
		Metadata:    metadata,
	}
	if p, err := auth.PrincipalFromContext(ctx); err == nil {
		op.InitiatorSubject = p.Subject
	}
	if err := h.repo.Create(ctx, op); err != nil {
		return uuid.Nil(), connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return op.OperationID, nil
}

// ListArgs is one page of a tenant's operations, as the store reads it.
type ListArgs struct {
	TenantID uuid.UUID
	State    *State
	AfterID  uuid.UUID
	PageSize int32
	// Filter is the caller's CEL expression, pushed down where it can be.
	Filter      string
	NewestFirst bool
	// Initiator, when set, keeps only the operations that subject started;
	// HideDismissed then leaves out those it cleared from its own list.
	Initiator     string
	HideDismissed bool
	// Facets narrows the listing apart from Filter.
	Facets Facets
}

// The facets an operation listing narrows and counts by (FacetCount.Facet).
const (
	FacetState = "state"
	FacetType  = "type"
)

// Facets narrows an operation listing: an operation passes when its state is
// one of States and its type one of Types, either empty for any.
type Facets struct {
	States []State
	Types  []string
}

// FacetCount is how many operations of a listing give Value for Facet.
type FacetCount struct {
	Facet, Value string
	Count        int64
}

// ListInput is a ListOperations request.
type ListInput struct {
	State       *State
	PageSize    int32
	PageToken   string
	Filter      string
	NewestFirst bool
	// Mine lists only the caller's own operations, and leaves out those the
	// caller dismissed unless IncludeDismissed.
	Mine             bool
	IncludeDismissed bool
	// TenantID lists another tenant's operations, which only a platform
	// admin may; uuid.Nil is the caller's own tenant.
	TenantID uuid.UUID
	// Facets narrows the listing apart from Filter.
	Facets Facets
}

// msgCrossTenant is the refusal of a caller who names another tenant's
// operations without the platform admin role.
const msgCrossTenant = "cross-tenant operations denied"

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

// callerSubject is the subject of the caller's token, which owns the
// operations it started. No subject is no one's operations, never everyone's.
func callerSubject(ctx context.Context) (string, error) {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil || p.Subject == "" {
		return "", connect.NewError(connect.CodeUnauthenticated, "needs a caller with a subject")
	}
	return p.Subject, nil
}

// DismissOperations clears the caller's own finished operations from their
// list: those of ids, or every one with ids empty. It changes only the
// caller's view — the operations stay, listed to everyone else — so reading
// operations is the permission it asks for.
func (h *Handler) DismissOperations(ctx context.Context, ids []uuid.UUID) (int64, error) {
	tenantID, err := auth.TenantFromContext(ctx)
	if err != nil {
		return 0, connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	subject, err := callerSubject(ctx)
	if err != nil {
		return 0, err
	}
	// Clearing changes only what the caller's own list shows, never the
	// operation: whoever may list them may tidy that list.
	if err := auth.AssertCapabilityOp(ctx, limes.OpList, ""); err != nil {
		return 0, err
	}
	if err := h.authorize(ctx, cedar.ActionReadOperation, tenantID); err != nil {
		return 0, err
	}
	n, err := h.repo.Dismiss(ctx, tenantID, subject, ids)
	if err != nil {
		return 0, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return n, nil
}
