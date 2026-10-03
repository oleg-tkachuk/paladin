package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/batchh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/uploadpolicy"
)

// BatchCopyExecutor implements the BatchCopy operation type.
//
// Reads JSON-encoded batch.BatchCopyArgs, iterates ObjectIDs, performs
// a server-side copy of each from (SrcCollection, source key) to
// (DstCollection, KeyPrefix + source key) via the storage adapter's
// CopyObject. Bookkeeping mirrors the data-plane's CopyObject handler:
//
//  1. Look up source row + source bucket.
//  2. Compute destination key — KeyPrefix + src.Key (empty prefix
//     = reuse src.Key under the destination namespace).
//  3. CreateObject on the destination (state=PENDING). FK to
//     collections validates before we issue the storage call.
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
	Objects     objecth.Repository
	Storage     objecth.Storage
	Transitions Transitioner

	// PendingTTL pins the destination row's presign_expires_at. Wired from
	// cfg.Limits.Presign.PutTTL, the lifetime the data plane gives a PENDING
	// row's upload URL, so reaper / reconciler windows match RPC-driven
	// creates. Required: a copy with no deadline would leave its PENDING row
	// outside the reaper's reach.
	PendingTTL time.Duration

	// Limits are limits.*; with the destination bucket's constraints they
	// decide which objects may be copied in. Required: a copy creates an
	// object, and without the check a batch copy is the way around the
	// destination's allowlist and size cap.
	Limits uploadpolicy.Limits
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
func (e *BatchCopyExecutor) Execute(ctx context.Context, op operationh.Operation) ([]byte, error) {
	if e.Objects == nil || e.Storage == nil || e.Transitions == nil {
		return nil, errors.New("BatchCopyExecutor: dependencies missing (Objects / Storage / Transitions)")
	}
	if e.PendingTTL <= 0 {
		return nil, errors.New("BatchCopyExecutor: PendingTTL must be positive")
	}
	if err := e.Limits.Validate(); err != nil {
		return nil, fmt.Errorf("BatchCopyExecutor: %w", err)
	}

	var args batchh.BatchCopyArgs
	if err := json.Unmarshal(op.Metadata, &args); err != nil {
		return nil, fmt.Errorf("decode metadata: %w", err)
	}
	if args.TenantID == uuid.Nil ||
		args.SrcCollection == "" ||
		args.DstCollection == "" ||
		len(args.ObjectIDs) == 0 {
		return nil, errors.New("invalid metadata: tenant_id, src_collection, dst_collection, object_ids required")
	}
	if args.TenantID != op.TenantID {
		return nil, fmt.Errorf("metadata tenant_id %s != operation tenant_id %s",
			args.TenantID, op.TenantID)
	}

	// Bucket lookups are batch-invariant: same source / dest collection
	// across the whole batch ⇒ resolve once.
	srcBackendID, srcBucket, err := e.Objects.LookupBucket(ctx, args.TenantID, args.SrcCollection, false) // copy source (read)
	if err != nil {
		return nil, fmt.Errorf("lookup src bucket: %w", err)
	}
	dstMeta, err := e.Objects.LookupBucketMeta(ctx, args.TenantID, args.DstCollection, true) // copy dest (mutation)
	if err != nil {
		return nil, fmt.Errorf("lookup dst bucket: %w", err)
	}
	dstBackendID, dstBucket := dstMeta.BackendID, dstMeta.BucketName
	dstPolicy := uploadpolicy.For(e.Limits, dstMeta.Constraints)

	resp := BatchCopyResponse{Total: len(args.ObjectIDs)}

	presignTTL := e.PendingTTL

	// One batched read replaces a per-id FindByName round-trip inside
	// copyOne; missing ids surface as per-row "not found" failures.
	byID, err := findByIDs(ctx, e.Objects, args.TenantID, args.ObjectIDs)
	if err != nil {
		return nil, fmt.Errorf("batch lookup: %w", err)
	}

	for i, srcID := range args.ObjectIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ReportProgress(ctx, i, resp.Total)
		src, found := byID[srcID]
		if !found {
			resp.Failed++
			resp.Failures = append(resp.Failures, BatchCopyFailure{
				ObjectID: srcID.String(),
				Reason:   "not found",
			})
			continue
		}
		if err := dstPolicy.CheckCopy(uploadpolicy.Upload{
			SizeBytes: src.SizeBytes, ContentType: src.ContentType, ChecksumAlgorithm: src.ChecksumAlgo,
		}); err != nil {
			resp.Failed++
			resp.Failures = append(resp.Failures, BatchCopyFailure{
				ObjectID: srcID.String(),
				Reason:   err.Error(),
			})
			continue
		}
		if err := e.copyOne(ctx, args, src, srcBackendID, srcBucket, dstBackendID, dstBucket, presignTTL); err != nil {
			resp.Failed++
			resp.Failures = append(resp.Failures, BatchCopyFailure{
				ObjectID: srcID.String(),
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

// copyOne does the per-object create + S3 copy + promote dance. Pulled
// out of Execute so the loop body stays linear: create → copy →
// promote (or compensate). Returns the first error encountered with
// enough context for the caller's per-row failure entry.
func (e *BatchCopyExecutor) copyOne(
	ctx context.Context,
	args batchh.BatchCopyArgs,
	src objecth.Object,
	srcBackendID, srcBucket, dstBackendID, dstBucket string,
	presignTTL time.Duration,
) error {
	if src.State != statemachine.StateAvailable {
		return fmt.Errorf("source state %s; cannot copy", src.State)
	}

	dstKey := args.KeyPrefix + src.Key
	dst, err := e.Objects.CreateObject(ctx, objecth.CreateObjectArgs{
		TenantID:         args.TenantID,
		Collection:       args.DstCollection,
		Key:              dstKey,
		ContentType:      src.ContentType,
		SizeBytes:        &src.SizeBytes,
		Metadata:         copyMap(src.Metadata),
		Tags:             copyMap(src.Tags),
		ExternalRef:      src.ExternalRef,
		PresignExpiresAt: time.Now().Add(presignTTL),
	})
	if err != nil {
		return fmt.Errorf("create dst row: %w", err)
	}

	if err := e.Storage.CopyObject(ctx,
		objecth.Location{BackendID: srcBackendID, TenantID: args.TenantID, Bucket: srcBucket, Collection: args.SrcCollection, Key: src.Key},
		objecth.Location{BackendID: dstBackendID, TenantID: args.TenantID, Bucket: dstBucket, Collection: args.DstCollection, Key: dstKey},
	); err != nil {
		// Compensate: dst row is PENDING. Without this it lingers
		// until the reconciler hard-deletes it (`min_object_age`).
		if mfErr := e.Transitions.MarkFailed(ctx, dst.ObjectID, "batch copy storage failed"); mfErr != nil {
			return fmt.Errorf("storage copy: %w (compensation also failed: %w)", err, mfErr)
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
