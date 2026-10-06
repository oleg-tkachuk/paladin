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
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
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

// ObjectFinder reads the objects a batch names by id, as the worker does.
type ObjectFinder interface {
	FindByIDs(ctx context.Context, tenantID uuid.UUID, ids []uuid.UUID) ([]objecth.Object, error)
}

type Handler struct {
	submitter Submitter
	policy    cedar.Authorizer
	buckets   BucketResolver
	objects   ObjectFinder
}

func NewHandler(submitter Submitter, policy cedar.Authorizer, buckets BucketResolver, objects ObjectFinder) *Handler {
	return &Handler{submitter: submitter, policy: policy, buckets: buckets, objects: objects}
}

// BatchDelete validates and enqueues an async delete across up to 10k objects.
// Returns the operation_id to poll via GetOperation.
func (h *Handler) BatchDelete(ctx context.Context, args BatchDeleteArgs) (uuid.UUID, error) {
	tenantID, p, err := apiutil.ActingContext(ctx)
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
	if err := h.assertOnObjects(ctx, tenantID, args.Collection, args.ObjectIDs, capability.OpDelete); err != nil {
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
	tenantID, p, err := apiutil.ActingContext(ctx)
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
	if err := h.assertCopy(ctx, tenantID, args); err != nil {
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
	tenantID, p, err := apiutil.ActingContext(ctx)
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
	if err := h.assertOnObjects(ctx, tenantID, args.Collection, args.ObjectIDs, capability.OpTag); err != nil {
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
	tenantID, p, err := apiutil.ActingContext(ctx)
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
	if err := h.assertOnObjects(ctx, tenantID, args.Collection, args.ObjectIDs, capability.OpPut); err != nil {
		return uuid.Nil, err
	}
	if err := h.authorize(ctx, p, tenantID, args.Collection, cedar.ActionRestoreObject); err != nil {
		return uuid.Nil, err
	}
	md, _ := json.Marshal(args)
	return h.chargeAndSubmit(ctx, "BatchRestoreObjects", md)
}

// batchObjects is what a batch will act on: the objects of collection among
// ids, read and filtered as the worker reads them. Ids missing here are left
// for the worker to report as not found.
func (h *Handler) batchObjects(ctx context.Context, tenantID uuid.UUID, collection string, ids []uuid.UUID) ([]objecth.Object, error) {
	if h.objects == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("batch: object lookup not wired"))
	}
	found, err := h.objects.FindByIDs(ctx, tenantID, ids)
	if err != nil {
		return nil, apiutil.MapError(fmt.Errorf("batch lookup: %w", err))
	}
	objs := make([]objecth.Object, 0, len(found))
	for _, o := range found {
		if o.Collection == collection {
			objs = append(objs, o)
		}
	}
	return objs, nil
}

// errNothingInScope refuses a resource-restricted capability a batch that
// would act on none of its objects: nothing in it is shown to be in scope, so
// the capability is refused as it would be any unbound operation. Missing ids
// are otherwise the worker's to report, per object.
func errNothingInScope(collection string) error {
	return connect.NewError(connect.CodePermissionDenied,
		fmt.Errorf("%w: none of object_ids is an object of collection %q", capability.ErrResourceNotAllowed, collection))
}

// assertOnObjects checks the capability on ctx, if any, for op over a batch.
// A capability that is not resource-restricted needs only the op, and nothing
// is read. A restricted one is checked against each object the batch will act
// on; the batch names them by id, so they are read to learn their URIs.
func (h *Handler) assertOnObjects(ctx context.Context, tenantID uuid.UUID, collection string, ids []uuid.UUID, op capability.Op) error {
	cap, ok := auth.CapabilityFromContext(ctx)
	if !ok || !cap.Caveats.RestrictsResources() {
		return auth.AssertCapabilityOp(ctx, op, "")
	}
	objs, err := h.batchObjects(ctx, tenantID, collection, ids)
	if err != nil {
		return err
	}
	if len(objs) == 0 {
		return errNothingInScope(collection)
	}
	for _, o := range objs {
		uri := objecth.CapabilityObjectURI(tenantID, o.Collection, o.Key)
		if err := auth.AssertCapabilityOpOnObject(ctx, op, uri, len(o.Taint) > 0); err != nil {
			return err
		}
	}
	return nil
}

// assertCopy checks the capability on ctx, if any, for a BatchCopy: get on
// each source and put on each destination. The sources are read whatever the
// capability's resource caveats, because a copy reads them: a capability not
// allowed tainted reads must not copy a tainted object where it can read it.
func (h *Handler) assertCopy(ctx context.Context, tenantID uuid.UUID, args BatchCopyArgs) error {
	cap, ok := auth.CapabilityFromContext(ctx)
	if !ok {
		return nil
	}
	objs, err := h.batchObjects(ctx, tenantID, args.SrcCollection, args.ObjectIDs)
	if err != nil {
		return err
	}
	if len(objs) == 0 {
		if cap.Caveats.RestrictsResources() {
			return errNothingInScope(args.SrcCollection)
		}
		if err := auth.AssertCapabilityOp(ctx, capability.OpGet, ""); err != nil {
			return err
		}
		return auth.AssertCapabilityOp(ctx, capability.OpPut, "")
	}
	for _, o := range objs {
		src := objecth.CapabilityObjectURI(tenantID, o.Collection, o.Key)
		if err := auth.AssertCapabilityOpOnObject(ctx, capability.OpGet, src, len(o.Taint) > 0); err != nil {
			return err
		}
	}
	// Put last: the op asserted last is the one a charge is attributed to.
	for _, o := range objs {
		// The worker writes each copy at KeyPrefix + the source key.
		dst := objecth.CapabilityObjectURI(tenantID, args.DstCollection, args.KeyPrefix+o.Key)
		if err := auth.AssertCapabilityOpOnObject(ctx, capability.OpPut, dst, false); err != nil {
			return err
		}
	}
	return nil
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
func (h *Handler) authorize(ctx context.Context, p *auth.Principal, tenantID uuid.UUID, collection string, action cedar.Action) error {
	var backendID, bucket string
	if h.buckets != nil {
		backendID, bucket, _ = h.buckets.LookupBucket(ctx, tenantID, collection, false)
	}
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipal(p),
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
