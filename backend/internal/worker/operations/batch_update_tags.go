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

// BatchUpdateTagsExecutor implements the BatchUpdateTags operation type.
//
// Reads JSON-encoded batch.BatchUpdateTagsArgs from operation.Metadata,
// iterates ObjectIDs, replaces the tag map on each via the object
// repository's UpdateMetadata seam (UpdatedFields=["tags"]).
//
// Resource-version semantics: per-object UpdateMetadata uses the row's
// CURRENT ResourceVersion, read fresh inside the loop. Concurrent
// UPDATE in flight surfaces as ErrVersionMismatch on that row; the
// failure list carries the ID and the caller retries with a fresh
// BatchUpdateTags. Total / Succeeded / Failed counts mirror the
// BatchDelete contract.
//
// Tags semantics: the supplied Tags map REPLACES the existing tag
// map for each object — empty map clears tags entirely. Per-key
// merge would require a richer args shape; that's a follow-up if
// the user-facing API ever wants partial-merge.
type BatchUpdateTagsExecutor struct {
	Objects object.Repository
}

// BatchUpdateTagsResponse mirrors the BatchDelete shape.
type BatchUpdateTagsResponse struct {
	Total     int                      `json:"total"`
	Succeeded int                      `json:"succeeded"`
	Failed    int                      `json:"failed"`
	Failures  []BatchUpdateTagsFailure `json:"failures,omitempty"`
}

type BatchUpdateTagsFailure struct {
	ObjectID string `json:"object_id"`
	Reason   string `json:"reason"`
}

// Execute implements Executor.
func (e *BatchUpdateTagsExecutor) Execute(ctx context.Context, op operation.Operation) ([]byte, error) {
	if e.Objects == nil {
		return nil, errors.New("BatchUpdateTagsExecutor: Objects dependency missing")
	}

	var args batch.BatchUpdateTagsArgs
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

	resp := BatchUpdateTagsResponse{Total: len(args.ObjectIDs)}

	// One batched read supplies each row's ResourceVersion for the OCC
	// check in UpdateMetadata; per-id not-found reporting is preserved.
	byID, err := findByIDs(ctx, e.Objects, args.TenantID, args.ObjectIDs)
	if err != nil {
		return nil, fmt.Errorf("batch lookup: %w", err)
	}

	for i, objectID := range args.ObjectIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ReportProgress(ctx, i, resp.Total)

		obj, found := byID[objectID]
		if !found {
			resp.Failed++
			resp.Failures = append(resp.Failures, BatchUpdateTagsFailure{
				ObjectID: objectID.String(),
				Reason:   "not found",
			})
			continue
		}
		if _, err := e.Objects.UpdateMetadata(ctx, object.UpdateMetadataArgs{
			TenantID:        args.TenantID,
			ObjectID:        obj.ObjectID,
			ResourceVersion: obj.ResourceVersion,
			UpdatedFields:   []string{"tags"},
			Tags:            args.Tags,
		}); err != nil {
			resp.Failed++
			resp.Failures = append(resp.Failures, BatchUpdateTagsFailure{
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
