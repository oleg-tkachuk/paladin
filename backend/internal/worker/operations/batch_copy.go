package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/batch"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/operation"
	"github.com/oleg-tkachuk/paladin/internal/statemachine"
)

// BatchCopyExecutor implements the BatchCopy operation type.
//
// Reads JSON-encoded batch.BatchCopyArgs, iterates ObjectIDs, performs
// a server-side copy of each from (SrcObjectKey, source key) to
// (DstObjectKey, KeyPrefix + source key) via the storage adapter's
// CopyObject. Bookkeeping mirrors the data-plane's CopyObject handler:
//
//  1. Look up source row + source bucket.
//  2. Compute destination key — KeyPrefix + src.Key (empty prefix
//     = reuse src.Key under the destination namespace).
//  3. CreateObject on the destination (state=PENDING). FK to
//     object_keys validates before we issue the storage call.
//  4. storage.CopyObject. On error: MarkFailed on the destination
//     row to compensate (otherwise PENDING leaks).
//  5. PromoteToAvailable on the destination with the source's etag /
//     size / checksum (the copied object is bit-identical at S3 level).
//
// Per-row failures land in the response with a typed reason; partial
// success is the contract.
//
// What's intentionally NOT here:
//
//   - Cedar policy check per row. The handler already authorised at
//     submission time at the object-key level. Per-object Cedar would
//     be defence-in-depth; not free in latency, and the same caveat
//     scope applies to every row in the batch.
//   - Source-state filter beyond AVAILABLE. Copying from PENDING /
//     FAILED would race with the producer; we record a per-row
//     failure rather than try.
//   - Cross-tenant copy. Args carry one TenantID; we never look up
//     a destination owned by a different tenant.
type BatchCopyExecutor struct {
	Objects     object.Repository
	Storage     object.Storage
	Transitions Transitioner

	// PresignDefaultTTL pins the destination row's presign_expires_at.
	// Wired from the same cfg.Limits.Presign.DefaultTTL the data plane
	// uses, so reaper / reconciler windows match RPC-driven creates.
	PresignDefaultTTL time.Duration
}

// BatchCopyResponse mirrors the BatchDelete shape.
type BatchCopyResponse struct {
	Total     int                `json:"total"`
	Succeeded int                `json:"succeeded"`
	Failed    int                `json:"failed"`
	Failures  []BatchCopyFailure `json:"failures,omitempty"`
}

type BatchCopyFailure struct {
	ObjectID string `json:"object_id"`
	Reason   string `json:"reason"`
}

// Execute implements Executor.
func (e *BatchCopyExecutor) Execute(ctx context.Context, op operation.Operation) ([]byte, error) {
	if e.Objects == nil || e.Storage == nil || e.Transitions == nil {
		return nil, errors.New("BatchCopyExecutor: dependencies missing (Objects / Storage / Transitions)")
	}

	var args batch.BatchCopyArgs
	if err := json.Unmarshal(op.Metadata, &args); err != nil {
		return nil, fmt.Errorf("decode metadata: %w", err)
	}
	if args.TenantID == uuid.Nil ||
		args.SrcObjectKey == "" ||
		args.DstObjectKey == "" ||
		len(args.ObjectIDs) == 0 {
		return nil, errors.New("invalid metadata: tenant_id, src_object_key, dst_object_key, object_ids required")
	}
	if args.TenantID != op.TenantID {
		return nil, fmt.Errorf("metadata tenant_id %s != operation tenant_id %s",
			args.TenantID, op.TenantID)
	}

	// Bucket lookups are batch-invariant: same source / dest object_key
	// across the whole batch ⇒ resolve once.
	srcBucket, err := e.Objects.LookupBucket(ctx, args.TenantID, args.SrcObjectKey)
	if err != nil {
		return nil, fmt.Errorf("lookup src bucket: %w", err)
	}
	dstBucket, err := e.Objects.LookupBucket(ctx, args.TenantID, args.DstObjectKey)
	if err != nil {
		return nil, fmt.Errorf("lookup dst bucket: %w", err)
	}

	resp := BatchCopyResponse{Total: len(args.ObjectIDs)}

	presignTTL := e.PresignDefaultTTL
	if presignTTL <= 0 {
		presignTTL = 15 * time.Minute
	}

	// One batched read replaces a per-id FindByName round-trip inside
	// copyOne; missing ids surface as per-row "not found" failures.
	byID, err := findByIDs(ctx, e.Objects, args.TenantID, args.ObjectIDs)
	if err != nil {
		return nil, fmt.Errorf("batch lookup: %w", err)
	}

	for _, srcID := range args.ObjectIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		src, found := byID[srcID]
		if !found {
			resp.Failed++
			resp.Failures = append(resp.Failures, BatchCopyFailure{
				ObjectID: srcID.String(),
				Reason:   "not found",
			})
			continue
		}
		if err := e.copyOne(ctx, args, src, srcBucket, dstBucket, presignTTL); err != nil {
			resp.Failed++
			resp.Failures = append(resp.Failures, BatchCopyFailure{
				ObjectID: srcID.String(),
				Reason:   err.Error(),
			})
			continue
		}
		resp.Succeeded++
	}

	body, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("encode response: %w", err)
	}
	return body, nil
}

// copyOne does the per-object create + S3 copy + promote dance. Pulled
// out of Execute so the loop body stays linear: create → copy →
// promote (or compensate). Returns the first error encountered with
// enough context for the caller's per-row failure entry.
func (e *BatchCopyExecutor) copyOne(
	ctx context.Context,
	args batch.BatchCopyArgs,
	src object.Object,
	srcBucket, dstBucket string,
	presignTTL time.Duration,
) error {
	if src.State != statemachine.StateAvailable {
		return fmt.Errorf("source state %s; cannot copy", src.State)
	}

	dstKey := args.KeyPrefix + src.Key
	dst, err := e.Objects.CreateObject(ctx, object.CreateObjectArgs{
		TenantID:         args.TenantID,
		ObjectKey:        args.DstObjectKey,
		Key:              dstKey,
		ContentType:      src.ContentType,
		SizeHint:         src.SizeBytes,
		Metadata:         copyMap(src.Metadata),
		Tags:             copyMap(src.Tags),
		ExternalRef:      src.ExternalRef,
		PresignExpiresAt: time.Now().Add(presignTTL),
	})
	if err != nil {
		return fmt.Errorf("create dst row: %w", err)
	}

	if err := e.Storage.CopyObject(ctx,
		object.Location{TenantID: args.TenantID, Bucket: srcBucket, ObjectKey: args.SrcObjectKey, Key: src.Key},
		object.Location{TenantID: args.TenantID, Bucket: dstBucket, ObjectKey: args.DstObjectKey, Key: dstKey},
	); err != nil {
		// Compensate: dst row is PENDING. Without this it lingers
		// until the reconciler hard-deletes it (`min_object_age`).
		if mfErr := e.Transitions.MarkFailed(ctx, dst.ObjectID, "batch copy storage failed"); mfErr != nil {
			return fmt.Errorf("storage copy: %w (compensation also failed: %v)", err, mfErr)
		}
		return fmt.Errorf("storage copy: %w", err)
	}

	if _, err := e.Transitions.PromoteToAvailable(
		ctx,
		dst.ObjectID,
		src.ETag,
		src.SizeBytes,
		src.Checksum,
		"", // sequencer: copy doesn't carry one
		statemachine.SourceRPC,
	); err != nil {
		return fmt.Errorf("promote dst: %w", err)
	}
	return nil
}

// copyMap returns a shallow copy so the destination row doesn't share
// the source's map (avoids cross-row mutation if upstream code adds a
// per-row metadata tweak before the loop tail).
func copyMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
