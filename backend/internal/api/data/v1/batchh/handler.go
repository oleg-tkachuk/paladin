// Package batch implements the BatchService business logic.
//
// Batch RPCs return a long-running Operation (AIP-151). The actual work
// happens in a background worker consuming the operations table. Handlers
// validate, authorize, and enqueue — they never block on backend calls.
package batchh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/capability"
)

type BatchDeleteArgs struct {
	TenantID   uuid.UUID
	Collection string
	ObjectIDs  []uuid.UUID
	// Permanent selects hard deletion — bytes and row — over the default
	// soft delete, honouring each object's lock exactly as DeleteObject
	// does. There is deliberately no batch equivalent of
	// bypass_governance_retention: one call that overrode compliance locks
	// on up to 10k objects is a different kind of authority from one that
	// overrides a single object's, and a locked object simply lands in the
	// per-object failure list instead.
	Permanent bool
}

type BatchCopyArgs struct {
	TenantID      uuid.UUID
	SrcCollection string
	DstCollection string
	ObjectIDs     []uuid.UUID
	KeyPrefix     string // optional destination prefix
}

type BatchUpdateTagsArgs struct {
	TenantID   uuid.UUID
	Collection string
	ObjectIDs  []uuid.UUID
	Tags       map[string]string
	// Replace selects wholesale replacement over per-key merge. The request
	// has carried this flag all along and nothing read it: the executor
	// always replaced, so the DEFAULT call — replace=false, meaning merge —
	// silently dropped every tag the caller did not restate.
	Replace bool
}

type BatchRestoreObjectsArgs struct {
	TenantID   uuid.UUID
	Collection string
	ObjectIDs  []uuid.UUID
}

// Submitter records LROs. Provided by the operation package.
type Submitter interface {
	Submit(ctx context.Context, opType string, metadata []byte) (uuid.UUID, error)
}

// BucketResolver resolves an object-key to its physical (backend, bucket) so
// the SUBMIT-time Cedar check can enforce bucket:/collection: PAT scopes.
//
// This matters because the batch WORKER does NOT re-check Cedar per object
// (see internal/worker/operations/batch_copy.go — "Per-object Cedar would be
// defence-in-depth; not free in latency"): the submit-time object-key check is
// the sole Cedar gate for a batch, so its Resource must carry the bucket or a
// bucket:/collection:-scoped PAT is fail-closed on its own object-keys. The
// object repository (object.Repository) satisfies this.
type BucketResolver interface {
	LookupBucket(ctx context.Context, tenantID uuid.UUID, collection string, write bool) (backendID, bucket string, err error)
}

type Handler struct {
	submitter Submitter
	policy    cedar.Authorizer
	buckets   BucketResolver
}

func NewHandler(submitter Submitter, policy cedar.Authorizer, buckets BucketResolver) *Handler {
	return &Handler{submitter: submitter, policy: policy, buckets: buckets}
}

// BatchDelete validates and enqueues an async delete across up to 10k objects.
// Returns the operation_id to poll via GetOperation.
func (h *Handler) BatchDelete(ctx context.Context, args BatchDeleteArgs) (uuid.UUID, error) {
	tenantID, p, err := apiutil.CallerContext(ctx)
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
	// Capability gate: BatchDelete names its objects by ID, not URI, so it
	// asserts OpDelete with no resource. A resource-restricted capability is
	// therefore refused (it cannot be shown to stay in scope); an
	// unrestricted one needs only the op.
	if err := auth.AssertCapabilityOp(ctx, capability.OpDelete, ""); err != nil {
		return uuid.Nil, err
	}
	// Collection-level authorization, and the ONLY authorization this batch
	// gets. The comment here used to promise a per-object Cedar check inside
	// the worker; the worker has never done one, and batch_copy.go documents
	// why it deliberately does not — the caveat scope is identical for every
	// row, so per-row Cedar buys defence in depth at a latency cost across up
	// to 10k objects. Saying it happens when it does not is worse than either
	// choice, because a reader counts on a second line of defence.
	if err := h.authorize(ctx, p, tenantID, args.Collection, cedar.ActionDeleteObject); err != nil {
		return uuid.Nil, err
	}
	md, err := json.Marshal(args)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInternal, fmt.Errorf("marshal BatchDelete args: %w", err))
	}
	return h.chargeAndSubmit(ctx, "BatchDelete", md)
}

func (h *Handler) BatchCopy(ctx context.Context, args BatchCopyArgs) (uuid.UUID, error) {
	tenantID, p, err := apiutil.CallerContext(ctx)
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
	if err := auth.AssertCapabilityOp(ctx, capability.OpPut, ""); err != nil {
		return uuid.Nil, err
	}
	// Require copy on source and put on destination — collection-level check.
	if err := h.authorize(ctx, p, tenantID, args.SrcCollection, cedar.ActionCopyObject); err != nil {
		return uuid.Nil, err
	}
	if err := h.authorize(ctx, p, tenantID, args.DstCollection, cedar.ActionPutObject); err != nil {
		return uuid.Nil, err
	}
	md, err := json.Marshal(args)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInternal, fmt.Errorf("marshal BatchCopy args: %w", err))
	}
	return h.chargeAndSubmit(ctx, "BatchCopy", md)
}

func (h *Handler) BatchUpdateTags(ctx context.Context, args BatchUpdateTagsArgs) (uuid.UUID, error) {
	tenantID, p, err := apiutil.CallerContext(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	args.TenantID = tenantID
	if len(args.ObjectIDs) == 0 {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("object_ids must be non-empty"))
	}
	if err := auth.AssertCapabilityOp(ctx, capability.OpTag, ""); err != nil {
		return uuid.Nil, err
	}
	if err := h.authorize(ctx, p, tenantID, args.Collection, cedar.ActionUpdateObject); err != nil {
		return uuid.Nil, err
	}
	md, err := json.Marshal(args)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInternal, fmt.Errorf("marshal BatchUpdateTags args: %w", err))
	}
	return h.chargeAndSubmit(ctx, "BatchUpdateTags", md)
}

func (h *Handler) BatchRestoreObjects(ctx context.Context, args BatchRestoreObjectsArgs) (uuid.UUID, error) {
	tenantID, p, err := apiutil.CallerContext(ctx)
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
	// Restoring a soft-deleted object is a write to the lifecycle —
	// the operator authority required mirrors the put path.
	if err := auth.AssertCapabilityOp(ctx, capability.OpPut, ""); err != nil {
		return uuid.Nil, err
	}
	if err := h.authorize(ctx, p, tenantID, args.Collection, cedar.ActionRestoreObject); err != nil {
		return uuid.Nil, err
	}
	md, _ := json.Marshal(args)
	return h.chargeAndSubmit(ctx, "BatchRestoreObjects", md)
}

// authorize runs the submit-time Cedar check for ONE target object-key. It
// resolves that object-key's bucket and injects it so bucket:/collection: PAT
// scopes enforce — a scoped principal is admitted on its own object-key(s) and
// DENIED when a target is off-scope. Each Batch RPC calls this per object-key
// (BatchCopy: src + dst); any denial short-circuits the whole submit.
//
// Best-effort + read-only + nil-safe: an unresolvable binding (or no resolver
// wired) emits no bucket scope key — unscoped principals are unaffected (the
// scope-enforcement forbid never fires for them), scoped principals stay
// fail-closed. write=false: the scope key is independent of the drain gate,
// and this is a submit-time authz probe, not the mutation itself.
func (h *Handler) authorize(ctx context.Context, p *auth.Principal, tenantID uuid.UUID, collection, action string) error {
	var backendID, bucket string
	if h.buckets != nil {
		backendID, bucket, _ = h.buckets.LookupBucket(ctx, tenantID, collection, false)
	}
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipalFor(p, tenantID),
		action,
		&cedar.Resource{TenantID: tenantID, Collection: collection, BackendID: backendID, BucketName: bucket},
		cedar.RequestContext{Now: time.Now()},
	)
	if err != nil {
		return apiutil.MapError(fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied, errors.New("denied by policy"))
	}
	return nil
}

// chargeAndSubmit charges the per-request capability budget BEFORE
// enqueuing the operation. Order matters: an over-budget caller
// must NOT leave a dangling PENDING row that the runner will then
// pick up and execute. ChargeRequest is a no-op when no capability
// is on context (JWT path) or when cfg.Capability.ChargePerRequest
// is 0 (default), so the JWT/unmetered call shape is unchanged.
func (h *Handler) chargeAndSubmit(ctx context.Context, opType string, md []byte) (uuid.UUID, error) {
	if err := auth.ChargeRequest(ctx); err != nil {
		return uuid.Nil, err
	}
	return h.submitter.Submit(ctx, opType, md)
}

// maxBatchSize caps the number of objects per BatchXxx call.
const maxBatchSize = 10_000

// Compile-time check: the operation package provides a compatible Submitter.
var _ Submitter = (*operationh.Handler)(nil)
