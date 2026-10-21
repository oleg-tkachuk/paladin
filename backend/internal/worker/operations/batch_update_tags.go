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
)

// BatchUpdateTagsExecutor implements the BatchUpdateTags operation type.
//
// Reads JSON-encoded batch.BatchUpdateTagsArgs from operation.Metadata,
// iterates ObjectIDs, replaces the tag map on each via the object
// repository's UpdateMetadata seam (UpdatedFields=["tags"]).
//
// Resource-version semantics: per-object UpdateMetadata uses the row's
// CURRENT ResourceVersion. A concurrent UPDATE in flight surfaces as
// ErrVersionMismatch on that row, which is read again and tried again, up to
// conflictAttempts times, so a merge applies to what the row holds then; one
// still in conflict lands in the failure list. Total / Succeeded / Failed
// counts mirror the BatchDelete contract.
//
// Tags semantics: args.Replace selects between the two, matching the RPC.
// Replace=true overwrites the object's tag map wholesale — an empty map then
// clears tags entirely. Replace=false (the default, and what the proto
// documents) merges per key: supplied keys win, keys the caller did not
// mention are left alone.
//
// The merge branch is why this file changed. The executor used to replace
// unconditionally, so the default call quietly deleted tags nobody asked it
// to touch — the opposite of what the request said it would do.
type BatchUpdateTagsExecutor struct {
	Objects objecth.Repository

	// Logger records how the batch ended. Optional. Tags need no
	// already-applied check: writing the same tags again is the same result.
	Logger *zap.Logger
}

// BatchUpdateTagsResponse mirrors the BatchDelete shape.
type BatchUpdateTagsResponse struct {
	BatchCounts
	Failures []BatchUpdateTagsFailure `json:"failures,omitempty"`
}

type BatchUpdateTagsFailure struct {
	ObjectID string `json:"object_id"`
	Reason   string `json:"reason"`
}

// Execute implements Executor.
func (e *BatchUpdateTagsExecutor) Execute(ctx context.Context, op operationh.Operation) ([]byte, error) {
	if e.Objects == nil {
		return nil, errors.New("BatchUpdateTagsExecutor: Objects dependency missing")
	}

	var args batchh.BatchUpdateTagsArgs
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

	resp := BatchUpdateTagsResponse{BatchCounts: BatchCounts{Total: len(args.ObjectIDs)}}

	// One batched read supplies each row's ResourceVersion for the OCC
	// check in UpdateMetadata; per-id not-found reporting is preserved.
	byID, err := findByIDs(ctx, e.Objects, args.TenantID, args.Collection, args.ObjectIDs)
	if err != nil {
		return nil, fmt.Errorf("batch lookup: %w", err)
	}

	for i, objectID := range args.ObjectIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ReportProgress(ctx, i, resp.Total)

		obj, found := byID[objectID]
		uerr := e.updateOne(ctx, args, obj, found)
		// A version conflict is the object changed between the read and the
		// write: a fresh read merges the tags into what it holds now.
		for attempt := 1; isVersionConflict(uerr) && attempt < conflictAttempts; attempt++ {
			fresh, err := findByIDs(ctx, e.Objects, args.TenantID, args.Collection, []uuid.UUID{objectID})
			if err != nil {
				return nil, fmt.Errorf("batch lookup: %w", err)
			}
			obj, found = fresh[objectID]
			uerr = e.updateOne(ctx, args, obj, found)
		}
		if uerr != nil {
			resp.Failed++
			resp.Failures = append(resp.Failures, BatchUpdateTagsFailure{
				ObjectID: objectID.String(),
				Reason:   uerr.Error(),
			})
			continue
		}
		resp.Succeeded++
	}
	ReportProgress(ctx, resp.Total, resp.Total)
	logBatchOutcome(e.Logger, op, resp.Total, resp.Succeeded, 0, resp.Failed)

	body, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("encode response: %w", err)
	}
	return body, nil
}

// updateOne writes one object's tags, or says why not.
func (e *BatchUpdateTagsExecutor) updateOne(ctx context.Context, args batchh.BatchUpdateTagsArgs, obj objecth.Object, found bool) error {
	if !found {
		return errObjectNotFound
	}
	_, err := e.Objects.UpdateMetadata(ctx, objecth.UpdateMetadataArgs{
		TenantID:        args.TenantID,
		ObjectID:        obj.ObjectID,
		ResourceVersion: obj.ResourceVersion,
		UpdatedFields:   []string{"tags"},
		Tags:            resolveTags(obj.Tags, args.Tags, args.Replace),
	})
	return err
}

// resolveTags computes the tag map to write for one object.
//
// Replace hands back the caller's map untouched. Merge copies the object's
// current tags and overlays the supplied keys, so a key the caller did not
// mention survives. The copy matters: writing into obj.Tags would mutate the
// row we read, and the same map is the OCC comparison basis for the retry a
// caller makes after a mismatch.
func resolveTags(current, supplied map[string]string, replace bool) map[string]string {
	if replace {
		return supplied
	}
	merged := make(map[string]string, len(current)+len(supplied))
	for k, v := range current {
		merged[k] = v
	}
	for k, v := range supplied {
		merged[k] = v
	}
	return merged
}
