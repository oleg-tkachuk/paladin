package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/batchh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
	"github.com/oleg-tkachuk/paladin/backend/internal/publicread"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// BatchDeleteExecutor implements the BatchDelete operation type.
//
// Reads JSON-encoded batch.BatchDeleteArgs from operation.Metadata and
// iterates ObjectIDs. Soft-deletes each via the state machine, or — when
// args.Permanent is set — hard-deletes each through the SAME
// object.Handler.PermanentDelete the DeleteObject RPC uses, so the DB-then-S3
// ordering, the purge debt and the object-lock rules have one implementation
// rather than two. A locked object fails that call and lands in the failure
// list; there is no batch-wide governance bypass.
//
// Continues past per-object failures — partial success is the standard batch
// contract; the response carries success / failure counts and a list of
// failed IDs so polling clients can retry just those.
//
// Resource-version semantics: we always pass the row's CURRENT
// resource_version to SoftDelete, so a concurrent UPDATE in flight when the
// worker picks this op up surfaces as a version conflict. The object is then
// read again and tried again, up to conflictAttempts times; one still in
// conflict lands in the failure list.
type BatchDeleteExecutor struct {
	Objects     objecth.Repository
	Transitions Transitioner

	// Permanent performs the hard delete. Optional: leave it nil and a
	// request that asks for one fails the operation outright rather than
	// silently soft-deleting, which is the shape this whole path exists to
	// avoid — a caller told its erasure succeeded while the objects sit in
	// the trash.
	Permanent PermanentDeleter

	// Erasures tells an object a permanent delete already erased from one
	// that never existed — both are missing rows. Required with Permanent: a
	// reissued permanent delete would otherwise report its first run's
	// erasures as failures.
	Erasures ErasureLookup

	// Logger records items found already deleted and how the batch ended.
	// Optional.
	Logger *zap.Logger
}

// PermanentDeleter is the seam onto object.Handler.PermanentDelete. It is
// narrow on purpose: the handler the worker holds is built without a policy
// engine (there is no principal out here to authorize), so this interface is
// what stops anything in the executor from reaching a method that would need
// one.
type PermanentDeleter interface {
	PermanentDelete(ctx context.Context, tenantID uuid.UUID, obj objecth.Object, rv int64, bypassGovernance bool) error
}

// ErasureLookup is the seam onto the tombstones a permanent delete leaves
// (erased_objects).
type ErasureLookup interface {
	ErasedIDs(ctx context.Context, tenantID uuid.UUID, collection string, ids []uuid.UUID) (map[uuid.UUID]bool, error)
}

// BatchDeleteResponse is what we marshal into operation.Response on
// success. Polling clients render this directly. Counts + per-failure
// detail; no per-success row to keep payload small at scale.
type BatchDeleteResponse struct {
	Total     int `json:"total"`
	Succeeded int `json:"succeeded"`
	// AlreadyApplied counts the succeeded items this run found deleted
	// already — by an earlier run of the same batch, typically one whose
	// worker died (see applied.go).
	AlreadyApplied int                  `json:"already_applied,omitempty"`
	Failed         int                  `json:"failed"`
	Failures       []BatchDeleteFailure `json:"failures,omitempty"`
}

// BatchDeleteFailure carries one per-object error so a client can
// surface "5 of 100 deletes failed; here are the IDs" without a
// secondary RPC.
type BatchDeleteFailure struct {
	ObjectID string `json:"object_id"`
	Reason   string `json:"reason"`
}

// Execute implements Executor.
func (e *BatchDeleteExecutor) Execute(ctx context.Context, op operationh.Operation) ([]byte, error) {
	if e.Objects == nil || e.Transitions == nil {
		return nil, errors.New("BatchDeleteExecutor: dependencies missing (Objects / Transitions)")
	}

	var args batchh.BatchDeleteArgs
	if err := json.Unmarshal(op.Metadata, &args); err != nil {
		return nil, fmt.Errorf("decode metadata: %w", err)
	}
	if args.TenantID == uuid.Nil || args.Collection == "" || len(args.ObjectIDs) == 0 {
		return nil, errors.New("invalid metadata: tenant_id, collection, object_ids required")
	}
	if args.TenantID != op.TenantID {
		// Defence in depth: handler stamps tenant_id from the auth
		// context AND embeds it in metadata. If they disagree
		// something tampered with the row.
		return nil, fmt.Errorf("metadata tenant_id %s != operation tenant_id %s",
			args.TenantID, op.TenantID)
	}

	if args.Permanent && (e.Permanent == nil || e.Erasures == nil) {
		return nil, errors.New("BatchDeleteExecutor: permanent delete requested but no PermanentDeleter / ErasureLookup wired")
	}

	resp := BatchDeleteResponse{Total: len(args.ObjectIDs)}

	// Read phase is one batched query: each row's CURRENT
	// ResourceVersion feeds the optimistic-concurrency check in
	// SoftDelete. Ids missing from the result are reported per-id
	// below — same contract as the old per-id lookup, minus the N
	// sequential round-trips.
	byID, err := findByIDs(ctx, e.Objects, args.TenantID, args.Collection, args.ObjectIDs)
	if err != nil {
		return nil, fmt.Errorf("batch lookup: %w", err)
	}
	erased, err := e.erasedAmong(ctx, args, byID)
	if err != nil {
		return nil, err
	}

	for i, objectID := range args.ObjectIDs {
		if err := ctx.Err(); err != nil {
			// Worker shutting down — surface as a partial success.
			// Already-deleted rows are committed; the operation row
			// reverts to PENDING via no UpdateState call (parent
			// caller decides; we just stop iterating).
			return nil, err
		}
		ReportProgress(ctx, i, resp.Total)

		obj, found := byID[objectID]
		already, derr := e.deleteOne(ctx, op, args, objectID, obj, found, erased[objectID])
		// A version conflict is an object changed between the read and the
		// delete, which a fresh read and a second try resolve; the try reads
		// whatever the change left — in the trash already, or locked.
		for attempt := 1; isVersionConflict(derr) && attempt < conflictAttempts; attempt++ {
			fresh, err := findByIDs(ctx, e.Objects, args.TenantID, args.Collection, []uuid.UUID{objectID})
			if err != nil {
				return nil, fmt.Errorf("batch lookup: %w", err)
			}
			obj, found = fresh[objectID]
			already, derr = e.deleteOne(ctx, op, args, objectID, obj, found, erased[objectID])
		}
		if derr != nil {
			resp.Failed++
			resp.Failures = append(resp.Failures, BatchDeleteFailure{
				ObjectID: objectID.String(),
				Reason:   derr.Error(),
			})
			continue
		}
		if already {
			resp.AlreadyApplied++
		}
		resp.Succeeded++
	}
	ReportProgress(ctx, resp.Total, resp.Total)
	logBatchOutcome(e.Logger, op, resp.Total, resp.Succeeded, resp.AlreadyApplied, resp.Failed)

	body, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("encode response: %w", err)
	}
	return body, nil
}

// deleteOne deletes one object of the batch, or says why not: already when
// the object is gone or in the trash already, which counts as done.
func (e *BatchDeleteExecutor) deleteOne(ctx context.Context, op operationh.Operation, args batchh.BatchDeleteArgs, objectID uuid.UUID, obj objecth.Object, found, erased bool) (already bool, err error) {
	switch {
	case !found && erased:
		logAlreadyApplied(e.Logger, op, objectID, reasonAlreadyErased)
		return true, nil
	case !found:
		return false, errObjectNotFound
	case !args.Permanent && obj.State == statemachine.StateDeleted:
		// A soft delete wants the object in the trash, and it is there.
		// A permanent delete still has work to do on a trashed object.
		logAlreadyApplied(e.Logger, op, objectID, reasonAlreadyDeleted)
		return true, nil
	case !args.Permanent && obj.State != statemachine.StateAvailable:
		// Only an AVAILABLE object moves to the trash; the state machine
		// would report anything else as a version conflict.
		return false, objecth.SoftDeleteRefusal(obj.State)
	case !args.Permanent && obj.PublicURL != "":
		// A public collection has no trash: its bytes would stay served
		// (ADR-0027).
		return false, publicread.Rulef("a public object is deleted permanently or not at all")
	case args.Permanent:
		// bypassGovernance is always false here: the override needs a
		// principal to check the role against, and this runs without one.
		return false, e.Permanent.PermanentDelete(ctx, args.TenantID, obj, obj.ResourceVersion, false)
	default:
		return false, e.Transitions.SoftDelete(ctx, obj.ObjectID, obj.ResourceVersion)
	}
}

// erasedAmong reads the tombstones of the batch's missing ids. Only a
// permanent delete asks: a soft delete never erases, so for it a missing id is
// simply not found.
func (e *BatchDeleteExecutor) erasedAmong(ctx context.Context, args batchh.BatchDeleteArgs, byID map[uuid.UUID]objecth.Object) (map[uuid.UUID]bool, error) {
	if !args.Permanent {
		return nil, nil
	}
	var missing []uuid.UUID
	for _, id := range args.ObjectIDs {
		if _, found := byID[id]; !found {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return nil, nil
	}
	erased, err := e.Erasures.ErasedIDs(ctx, args.TenantID, args.Collection, missing)
	if err != nil {
		return nil, fmt.Errorf("erasure lookup: %w", err)
	}
	return erased, nil
}
