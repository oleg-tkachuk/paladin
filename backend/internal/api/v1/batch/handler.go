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

	"github.com/oleg-tkachuk/paladin/capability"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
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
	TenantID     uuid.UUID
	SrcObjectKey string
	DstObjectKey string
	ObjectIDs    []uuid.UUID
	KeyPrefix    string // optional destination prefix
}

type BatchUpdateTagsArgs struct {
	TenantID  uuid.UUID
	ObjectKey string
	ObjectIDs []uuid.UUID
	Tags      map[string]string
}

type BatchRestoreObjectsArgs struct {
	TenantID  uuid.UUID
	ObjectKey string
	ObjectIDs []uuid.UUID
}

// Submitter records LROs. Provided by the operation package.
type Submitter interface {
	Submit(ctx context.Context, opType string, metadata []byte) (uuid.UUID, error)
}

// BucketResolver resolves an object-key to its physical (backend, bucket) so
// the SUBMIT-time Cedar check can enforce bucket:/object_key: PAT scopes.
//
// This matters because the batch WORKER does NOT re-check Cedar per object
// (see internal/worker/operations/batch_copy.go — "Per-object Cedar would be
// defence-in-depth; not free in latency"): the submit-time object-key check is
// the sole Cedar gate for a batch, so its Resource must carry the bucket or a
// bucket:/object_key:-scoped PAT is fail-closed on its own object-keys. The
// object repository (object.Repository) satisfies this.
type BucketResolver interface {
	LookupBucket(ctx context.Context, tenantID uuid.UUID, objectKey string, write bool) (backendID, bucket string, err error)
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
	// Capability gate: BatchDelete spans many objects under one
	// objectKey. We assert OpDelete with an empty URI (the prefix-
	// scope check happens per-row in the worker against
	// cap.Caveats.ResourcePrefixes — this surface only enforces the
	// op caveat). Per-row resource gating runs inside the worker.
	if err := auth.AssertCapabilityOp(ctx, capability.OpDelete, ""); err != nil {
		return uuid.Nil, err
	}
	// ObjectKey-level authorization. Per-object authorization happens inside
	// the worker on each row (slower but safer).
	if err := h.authorize(ctx, p, tenantID, args.ObjectKey, cedar.ActionDeleteObject); err != nil {
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
	// Require copy on source and put on destination — objectKey-level check.
	if err := h.authorize(ctx, p, tenantID, args.SrcObjectKey, cedar.ActionCopyObject); err != nil {
		return uuid.Nil, err
	}
	if err := h.authorize(ctx, p, tenantID, args.DstObjectKey, cedar.ActionPutObject); err != nil {
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
	if err := h.authorize(ctx, p, tenantID, args.ObjectKey, cedar.ActionUpdateObject); err != nil {
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
	if err := h.authorize(ctx, p, tenantID, args.ObjectKey, cedar.ActionRestoreObject); err != nil {
		return uuid.Nil, err
	}
	md, _ := json.Marshal(args)
	return h.chargeAndSubmit(ctx, "BatchRestoreObjects", md)
}

// authorize runs the submit-time Cedar check for ONE target object-key. It
// resolves that object-key's bucket and injects it so bucket:/object_key: PAT
// scopes enforce — a scoped principal is admitted on its own object-key(s) and
// DENIED when a target is off-scope. Each Batch RPC calls this per object-key
// (BatchCopy: src + dst); any denial short-circuits the whole submit.
//
// Best-effort + read-only + nil-safe: an unresolvable binding (or no resolver
// wired) emits no bucket scope key — unscoped principals are unaffected (the
// scope-enforcement forbid never fires for them), scoped principals stay
// fail-closed. write=false: the scope key is independent of the drain gate,
// and this is a submit-time authz probe, not the mutation itself.
func (h *Handler) authorize(ctx context.Context, p *auth.Principal, tenantID uuid.UUID, objectKey, action string) error {
	var backendID, bucket string
	if h.buckets != nil {
		backendID, bucket, _ = h.buckets.LookupBucket(ctx, tenantID, objectKey, false)
	}
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipalFor(p, tenantID),
		action,
		&cedar.Resource{TenantID: tenantID, ObjectKey: objectKey, BackendID: backendID, BucketName: bucket},
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
var _ Submitter = (*operation.Handler)(nil)
