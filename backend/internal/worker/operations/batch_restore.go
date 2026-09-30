package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/batchh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
)

// BatchRestoreExecutor implements the BatchRestoreObjects operation type.
//
// Reads JSON-encoded batch.BatchRestoreObjectsArgs, iterates ObjectIDs,
// transitions each from DELETED → AVAILABLE via statemachine.Restore.
//
// Restore-specific failure modes worth flagging in the per-row reason:
//
//   - statemachine.ErrNotFound — row is missing OR not in DELETED
//     state (can be RESTORE-into-already-AVAILABLE if a concurrent
//     caller raced us). The per-row failure carries the original
//     state-machine error message; callers branch on the string.
//   - statemachine.ErrConflict — guarded by a UNIQUE partial index
//     on (tenant, collection, key) WHERE state <> 'DELETED': if a
//     live row has been created at the same key since the soft-delete,
//     restore would collide and the SQL fails. Caller cleans up the
//     newer live row (or hard-deletes the soft row) before retrying.
//
// No resource-version guard: Restore's SQL gates on state='DELETED'
// alone. A concurrent UPDATE on a DELETED row is unusual (most paths
// reject DELETED state), so adding OCC here would buy little and
// would force the caller to thread the version of a row they don't
// otherwise see in their list-deleted UI.
type BatchRestoreExecutor struct {
	Objects     objecth.Repository
	Transitions Transitioner
}

// BatchRestoreResponse mirrors the BatchDelete shape.
type BatchRestoreResponse struct {
	Total     int                   `json:"total"`
	Succeeded int                   `json:"succeeded"`
	Failed    int                   `json:"failed"`
	Failures  []BatchRestoreFailure `json:"failures,omitempty"`
}

type BatchRestoreFailure struct {
	ObjectID string `json:"object_id"`
	Reason   string `json:"reason"`
}

// Execute implements Executor.
func (e *BatchRestoreExecutor) Execute(ctx context.Context, op operationh.Operation) ([]byte, error) {
	if e.Objects == nil || e.Transitions == nil {
		return nil, errors.New("BatchRestoreExecutor: dependencies missing (Objects / Transitions)")
	}

	var args batchh.BatchRestoreObjectsArgs
	if err := json.Unmarshal(op.Metadata, &args); err != nil {
		return nil, fmt.Errorf("decode metadata: %w", err)
	}
	if args.TenantID == uuid.Nil || args.Collection == "" || len(args.ObjectIDs) == 0 {
		return nil, errors.New("invalid metadata: tenant_id, collection, object_ids required")
	}
	if args.TenantID != op.TenantID {
		return nil, fmt.Errorf("metadata tenant_id %s != operation tenant_id %s",
			args.TenantID, op.TenantID)
	}

	resp := BatchRestoreResponse{Total: len(args.ObjectIDs)}

	// One batched read for existence checks (DELETED rows included —
	// the query has no state filter). Missing ids land in the failure
	// list below, same contract as the old per-id FindByName loop.
	byID, err := findByIDs(ctx, e.Objects, args.TenantID, args.ObjectIDs)
	if err != nil {
		return nil, fmt.Errorf("batch lookup: %w", err)
	}

	for i, objectID := range args.ObjectIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ReportProgress(ctx, i, resp.Total)

		if _, found := byID[objectID]; !found {
			resp.Failed++
			resp.Failures = append(resp.Failures, BatchRestoreFailure{
				ObjectID: objectID.String(),
				Reason:   "not found",
			})
			continue
		}
		if err := e.Transitions.Restore(ctx, objectID); err != nil {
			resp.Failed++
			resp.Failures = append(resp.Failures, BatchRestoreFailure{
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
