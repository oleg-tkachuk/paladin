package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/batchh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
	"github.com/oleg-tkachuk/paladin/backend/internal/publicread"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgerr"
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
//  4. storage.CopyObject. On error: DiscardFailed on the destination
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

	// Pending and Operations let a reissue finish a destination row its dead
	// run left PENDING: Operations says whether the row's creator can still
	// write it, Pending hands the row over. Required: without them a reissue
	// waits out the row's presign deadline.
	Pending    PendingAdopter
	Operations OperationLiveness

	// Logger records items found already copied, rows adopted, and how the
	// batch ended. Optional.
	Logger *zap.Logger
}

// PendingAdopter hands a PENDING object row from the operation that created
// it to another (objects.created_by_operation).
type PendingAdopter interface {
	AdoptPending(ctx context.Context, tenantID, objectID, creator, adopter uuid.UUID, deadline time.Time) (bool, error)
}

// OperationLiveness says whether an operation is still PENDING or RUNNING.
type OperationLiveness interface {
	IsLive(ctx context.Context, opID uuid.UUID) (bool, error)
}

// BatchCopyResponse mirrors the BatchDelete shape.
type BatchCopyResponse struct {
	Total     int `json:"total"`
	Succeeded int `json:"succeeded"`
	// AlreadyApplied counts the succeeded items whose destination this run
	// found holding the source's content already (see applied.go).
	AlreadyApplied int                `json:"already_applied,omitempty"`
	Failed         int                `json:"failed"`
	Failures       []BatchCopyFailure `json:"failures,omitempty"`
}

// errAlreadyCopied is copyOne's report that the destination key already
// holds the source's content: success, with nothing written.
var errAlreadyCopied = errors.New("already copied")

type BatchCopyFailure struct {
	ObjectID string `json:"object_id"`
	Reason   string `json:"reason"`
}

// Execute implements Executor.
func (e *BatchCopyExecutor) Execute(ctx context.Context, op operationh.Operation) ([]byte, error) {
	if e.Objects == nil || e.Storage == nil || e.Transitions == nil || e.Pending == nil || e.Operations == nil {
		return nil, errors.New("BatchCopyExecutor: dependencies missing (Objects / Storage / Transitions / Pending / Operations)")
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
	if dstMeta.PublicRead {
		// A batch copy names its copies after their sources (KeyPrefix + key);
		// a public collection names its objects itself (ADR-0027).
		return nil, publicread.Rulef("a batch copy cannot write into a public collection; copy objects one at a time")
	}
	dstBackendID, dstBucket := dstMeta.BackendID, dstMeta.BucketName
	dstPolicy := uploadpolicy.For(e.Limits, dstMeta.Constraints)

	resp := BatchCopyResponse{Total: len(args.ObjectIDs)}

	// One batched read replaces a per-id FindByName round-trip inside
	// copyOne; missing ids surface as per-row "not found" failures.
	byID, err := findByIDs(ctx, e.Objects, args.TenantID, args.SrcCollection, args.ObjectIDs)
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
				Reason:   errObjectNotFound.Error(),
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
		dstKey := args.KeyPrefix + src.Key
		err := e.copyOne(ctx, copyJob{
			op: op, args: args, src: src, dstKey: dstKey,
			from: objecth.Location{BackendID: srcBackendID, TenantID: args.TenantID, Bucket: srcBucket, Collection: args.SrcCollection, Key: src.Key},
			to:   objecth.Location{BackendID: dstBackendID, TenantID: args.TenantID, Bucket: dstBucket, Collection: args.DstCollection, Key: dstKey},
		})
		if errors.Is(err, errAlreadyCopied) {
			logAlreadyApplied(e.Logger, op, srcID, reasonAlreadyCopied)
			resp.AlreadyApplied++
			resp.Succeeded++
			continue
		}
		if err != nil {
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
	logBatchOutcome(e.Logger, op, resp.Total, resp.Succeeded, resp.AlreadyApplied, resp.Failed)

	body, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("encode response: %w", err)
	}
	return body, nil
}

// copyJob is one item of a batch copy: the source, where its bytes are, and
// where the copy goes.
type copyJob struct {
	op     operationh.Operation
	args   batchh.BatchCopyArgs
	src    objecth.Object
	dstKey string
	from   objecth.Location
	to     objecth.Location
}

// copyOne does the per-object create + S3 copy + promote dance. Pulled
// out of Execute so the loop body stays linear: create → copy →
// promote (or compensate). Returns the first error encountered with
// enough context for the caller's per-row failure entry.
func (e *BatchCopyExecutor) copyOne(ctx context.Context, job copyJob) error {
	if job.src.State != statemachine.StateAvailable {
		return fmt.Errorf("source state %s; cannot copy", job.src.State)
	}

	dst, err := e.Objects.CreateObject(ctx, objecth.CreateObjectArgs{
		TenantID:           job.args.TenantID,
		Collection:         job.args.DstCollection,
		Key:                job.dstKey,
		ContentType:        job.src.ContentType,
		SizeBytes:          &job.src.SizeBytes,
		Metadata:           copyMap(job.src.Metadata),
		Tags:               copyMap(job.src.Tags),
		ExternalRef:        job.src.ExternalRef,
		PresignExpiresAt:   time.Now().Add(e.PendingTTL),
		CreatedByOperation: job.op.OperationID,
	})
	if pgerr.Is(err, pgerr.UniqueViolation) {
		return e.occupiedDestination(ctx, job)
	}
	if err != nil {
		return fmt.Errorf("create dst row: %w", err)
	}
	return e.fill(ctx, job, dst.ObjectID)
}

// fill copies the bytes into the PENDING destination row dstID and promotes
// it, or marks it failed when the copy does not land.
func (e *BatchCopyExecutor) fill(ctx context.Context, job copyJob, dstID uuid.UUID) error {
	if err := e.Storage.CopyObject(ctx, job.from, job.to); err != nil {
		// Compensate: dst row is PENDING. Without this it lingers
		// until the reconciler hard-deletes it (`min_object_age`).
		if mfErr := e.Transitions.DiscardFailed(ctx, dstID, statemachine.FailedStorageCopy); mfErr != nil {
			return fmt.Errorf("storage copy: %w (compensation also failed: %w)", err, mfErr)
		}
		return fmt.Errorf("storage copy: %w", err)
	}

	if _, err := e.Transitions.PromoteToAvailable(
		ctx,
		dstID,
		job.src.ETag,
		job.src.SizeBytes,
		job.src.Checksum,
		"", // sequencer: copy doesn't carry one
		statemachine.SourceRPC,
	); err != nil {
		return fmt.Errorf("promote dst: %w", err)
	}
	return nil
}

// occupiedDestination explains a destination key that already has a row.
//
// The usual cause is an earlier run of this same batch: its copy of src
// landed, and the batch is being reissued because its worker died. That copy
// carries src's etag, size and checksum — PromoteToAvailable writes exactly
// those — so a live destination matching all three is src's content and counts
// as copied. Anything else is a real conflict, named in the caller's terms
// rather than as the database's unique violation.
func (e *BatchCopyExecutor) occupiedDestination(ctx context.Context, job copyJob) error {
	dst, err := e.Objects.FindByPath(ctx, job.args.TenantID, job.args.DstCollection, job.dstKey)
	// FindByPath hands back the query's own pgx.ErrNoRows, unmapped — unlike
	// FindByName, which maps it to objecth.ErrObjectNotFound. Mapping one
	// without the other moves this branch into the lookup error below.
	if errors.Is(err, pgx.ErrNoRows) {
		// FindByPath skips DELETED rows, and a trashed object still holds its
		// key.
		return fmt.Errorf("destination key %q is held by an object in the trash", job.dstKey)
	}
	if err != nil {
		return fmt.Errorf("look up destination key %q: %w", job.dstKey, err)
	}
	switch dst.State {
	case statemachine.StateAvailable:
		if sameContent(job.src, dst) {
			return errAlreadyCopied
		}
		return fmt.Errorf("destination key %q already holds a different object", job.dstKey)
	case statemachine.StatePending:
		return e.adoptPending(ctx, job, dst)
	default:
		return fmt.Errorf("destination key %q is held by a %s object", job.dstKey, dst.State)
	}
}

// adoptPending finishes a destination row an earlier run of a batch copy left
// PENDING — its worker died between creating the row and promoting it. The
// row is adopted only when the operation that created it can no longer write
// it, and only when it was made for an object of src's size and type; a
// compare-and-set on the creator settles a race between two reissues. Any
// other PENDING row is a write someone is still making.
func (e *BatchCopyExecutor) adoptPending(ctx context.Context, job copyJob, dst objecth.Object) error {
	inProgress := fmt.Errorf("destination key %q has a write in progress; retry once it settles", job.dstKey)
	creator := dst.CreatedByOperation
	if creator == uuid.Nil || creator == job.op.OperationID {
		// Not a batch copy's row — an upload — or this very operation's.
		return inProgress
	}
	if dst.SizeBytes != job.src.SizeBytes || dst.ContentType != job.src.ContentType {
		return fmt.Errorf("destination key %q has a pending copy of a different object", job.dstKey)
	}
	live, err := e.Operations.IsLive(ctx, creator)
	if err != nil {
		return fmt.Errorf("destination key %q: %w", job.dstKey, err)
	}
	if live {
		return inProgress
	}
	adopted, err := e.Pending.AdoptPending(ctx, job.args.TenantID, dst.ObjectID, creator, job.op.OperationID,
		time.Now().Add(e.PendingTTL))
	if err != nil {
		return fmt.Errorf("destination key %q: %w", job.dstKey, err)
	}
	if !adopted {
		return inProgress
	}
	logAdopted(e.Logger, job.op, job.src.ObjectID, dst.ObjectID, creator)
	return e.fill(ctx, job, dst.ObjectID)
}

// sameContent reports whether dst is a copy of src's bytes.
func sameContent(src, dst objecth.Object) bool {
	return dst.ETag == src.ETag && dst.SizeBytes == src.SizeBytes && dst.Checksum == src.Checksum
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
