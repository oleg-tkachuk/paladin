package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/batch"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/operation"
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
// resource_version (read fresh inside the loop) to SoftDelete, so a
// concurrent UPDATE in flight when the worker picks this op up will
// surface as ErrVersionMismatch and that ID will land in the failure
// list. Callers retry with a fresh BatchDelete if needed.
type BatchDeleteExecutor struct {
	Objects     object.Repository
	Transitions Transitioner

	// Permanent performs the hard delete. Optional: leave it nil and a
	// request that asks for one fails the operation outright rather than
	// silently soft-deleting, which is the shape this whole path exists to
	// avoid — a caller told its erasure succeeded while the objects sit in
	// the trash.
	Permanent PermanentDeleter
}

// PermanentDeleter is the seam onto object.Handler.PermanentDelete. It is
// narrow on purpose: the handler the worker holds is built without a policy
// engine (there is no principal out here to authorize), so this interface is
// what stops anything in the executor from reaching a method that would need
// one.
type PermanentDeleter interface {
	PermanentDelete(ctx context.Context, tenantID uuid.UUID, obj object.Object, rv int64, bypassGovernance bool) error
}

// BatchDeleteResponse is what we marshal into operation.Response on
// success. Polling clients render this directly. Counts + per-failure
// detail; no per-success row to keep payload small at scale.
type BatchDeleteResponse struct {
	Total     int                  `json:"total"`
	Succeeded int                  `json:"succeeded"`
	Failed    int                  `json:"failed"`
	Failures  []BatchDeleteFailure `json:"failures,omitempty"`
}

// BatchDeleteFailure carries one per-object error so a client can
// surface "5 of 100 deletes failed; here are the IDs" without a
// secondary RPC.
type BatchDeleteFailure struct {
	ObjectID string `json:"object_id"`
	Reason   string `json:"reason"`
}

// Execute implements Executor.
func (e *BatchDeleteExecutor) Execute(ctx context.Context, op operation.Operation) ([]byte, error) {
	if e.Objects == nil || e.Transitions == nil {
		return nil, errors.New("BatchDeleteExecutor: dependencies missing (Objects / Transitions)")
	}

	var args batch.BatchDeleteArgs
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

	if args.Permanent && e.Permanent == nil {
		return nil, errors.New("BatchDeleteExecutor: permanent delete requested but no PermanentDeleter wired")
	}

	resp := BatchDeleteResponse{Total: len(args.ObjectIDs)}

	// Read phase is one batched query: each row's CURRENT
	// ResourceVersion feeds the optimistic-concurrency check in
	// SoftDelete. Ids missing from the result are reported per-id
	// below — same contract as the old per-id lookup, minus the N
	// sequential round-trips.
	byID, err := findByIDs(ctx, e.Objects, args.TenantID, args.ObjectIDs)
	if err != nil {
		return nil, fmt.Errorf("batch lookup: %w", err)
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
		if !found {
			resp.Failed++
			resp.Failures = append(resp.Failures, BatchDeleteFailure{
				ObjectID: objectID.String(),
				Reason:   "not found",
			})
			continue
		}
		var derr error
		if args.Permanent {
			// bypassGovernance is always false here: the override needs a
			// principal to check the role against, and this runs without one.
			derr = e.Permanent.PermanentDelete(ctx, args.TenantID, obj, obj.ResourceVersion, false)
		} else {
			derr = e.Transitions.SoftDelete(ctx, obj.ObjectID, obj.ResourceVersion)
		}
		if err := derr; err != nil {
			resp.Failed++
			resp.Failures = append(resp.Failures, BatchDeleteFailure{
				ObjectID: objectID.String(),
				Reason:   err.Error(),
			})
			continue
		}
		resp.Succeeded++
	}
	ReportProgress(ctx, resp.Total, resp.Total)

	body, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("encode response: %w", err)
	}
	return body, nil
}
