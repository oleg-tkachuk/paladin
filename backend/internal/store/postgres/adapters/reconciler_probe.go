package adapters

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/s3adapter"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// HeadProber is the storage-side seam: HEAD an S3 object given the routing
// triplet. Satisfied by *s3adapter.Client (which already has Head with the
// same shape) and by the ObjectRouter that fronts it.
//
// The seam is about the CALL, not the package: it keeps a unit test from
// having to stand up an S3 client. The package does import s3adapter — for
// ErrObjectNotFound, and only for that. That is the deliberate half of the
// trade: one sentinel comparison here beats re-deriving "is this a 404?"
// from AWS error types in the persistence layer, which is what the stub this
// replaced was written to avoid.
type HeadProber interface {
	Head(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key, checksumAlgo string) (etag string, sizeBytes int64, checksum, sequencer string, err error)
	DeleteObject(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key string) error
}

// ReconcilerProbe satisfies worker.StorageProbe and eventingest.StorageProbe.
// It looks up the object's (tenant, bucket, key) routing, then calls the
// storage HEAD. Missing rows → found=false, no error.
type ReconcilerProbe struct {
	q    *sqlc.Queries
	head HeadProber
}

func NewReconcilerProbe(q *sqlc.Queries, head HeadProber) *ReconcilerProbe {
	return &ReconcilerProbe{q: q, head: head}
}

// ErrNoAnchor is an object whose upload is anchored to no bucket: a promoted
// object the reconciler has settled. No URL of it can write anywhere.
var ErrNoAnchor = errors.New("the object's upload is anchored to no bucket")

// HeadByObjectID HEADs the object's bytes in the bucket its upload is
// anchored to (objects.upload_bucket_id): the one bucket a URL of the row can
// write, which a promote places it in (ADR-0051). placed is that bucket. An
// object with no anchor is ErrNoAnchor.
func (r *ReconcilerProbe) HeadByObjectID(ctx context.Context, objectID uuid.UUID) (etag string, sizeBytes int64, checksum, sequencer string, placed statemachine.Placement, found bool, err error) {
	row, err := r.q.LookupObjectByID(ctx, pgUUID(objectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, "", "", placed, false, nil
		}
		return "", 0, "", "", placed, false, fmt.Errorf("lookup object: %w", err)
	}
	anchor, err := r.q.LookupUploadBucket(ctx, pgUUID(objectID))
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, "", "", placed, false, fmt.Errorf("object %s: %w", objectID, ErrNoAnchor)
	}
	if err != nil {
		return "", 0, "", "", placed, false, fmt.Errorf("lookup upload bucket: %w", err)
	}
	placed = statemachine.Placement{BackendID: anchor.BackendName, Bucket: anchor.BucketName}
	b, found, err := r.headAt(ctx, anchor.BackendName, anchor.BucketName, row)
	return b.etag, b.size, b.checksum, b.sequencer, placed, found, err
}

// HeadPlaced HEADs the object's bytes where its row places them
// (objects.placement_bucket_id), the bucket it is read from: what a storage
// delete event is checked against.
func (r *ReconcilerProbe) HeadPlaced(ctx context.Context, objectID uuid.UUID) (found bool, err error) {
	row, err := r.q.LookupObjectByID(ctx, pgUUID(objectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("lookup object: %w", err)
	}
	_, found, err = r.headAt(ctx, row.BackendName, row.BucketName, row)
	return found, err
}

// probedBytes is what a HEAD reports of an object's bytes.
type probedBytes struct {
	etag, checksum, sequencer string
	size                      int64
}

// headAt HEADs row's key in (backend, bucket): found false, and no error,
// when the backend holds nothing there.
func (r *ReconcilerProbe) headAt(ctx context.Context, backend, bucket string, row sqlc.LookupObjectByIDRow) (probedBytes, bool, error) {
	var b probedBytes
	var err error
	b.etag, b.size, b.checksum, b.sequencer, err = r.head.Head(ctx, backend, bucket, uuidFrom(row.TenantID),
		row.CollectionName, row.Path, checksumAlgoName(row.ChecksumAlgorithm))
	if err != nil {
		// HEAD failure is "not found" if the storage adapter signals 404 via
		// the standard not-found error wrapping; treat as not-found here.
		// Other errors propagate so the worker can log them.
		if isNotFoundErr(err) {
			return probedBytes{}, false, nil
		}
		return probedBytes{}, false, err
	}
	return b, true, nil
}

// DeleteByObjectID removes the stored bytes of a PENDING object — the
// reconciler's answer to bytes that broke the object's registration — in the
// bucket its upload is anchored to, where they landed. A row that is gone,
// or has no anchor, has nothing left to delete for.
//
// The delete is a write of bytes, so it asks the bucket gate as the request
// path does: on a draining or disabled backend, or a bucket not ready, it
// answers the gate's refusal (bucketgate.Refused) and calls nothing.
func (r *ReconcilerProbe) DeleteByObjectID(ctx context.Context, objectID uuid.UUID) error {
	row, err := r.q.LookupObjectByID(ctx, pgUUID(objectID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lookup object: %w", err)
	}
	anchor, err := r.q.LookupUploadBucket(ctx, pgUUID(objectID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lookup upload bucket: %w", err)
	}
	if err := bucketgate.Check(bucketgate.BytesWrite, bucketgate.State{
		Enabled: anchor.Enabled, ReadOnly: anchor.ReadOnly, ProvisionState: anchor.ProvisionState,
	}); err != nil {
		return err
	}
	return r.head.DeleteObject(ctx, anchor.BackendName, anchor.BucketName, uuidFrom(row.TenantID), row.CollectionName, row.Path)
}

// isNotFoundErr reports whether a HEAD failure means the object genuinely is
// not on the backend, as opposed to the backend being unreachable, slow, or
// refusing us.
//
// The distinction is terminal: a true here sends ReconcilerV2 down the
// DiscardFailed branch, which deletes the row. So the classification deliberately lives in
// s3adapter, next to the SDK error shapes it has to reason about, and this
// stays a single errors.Is — no AWS types in the persistence layer, and no
// second place where "is this a 404?" can be got subtly wrong.
//
// This used to be a `return false` stub, which made DiscardFailed unreachable:
// every object whose bytes were genuinely absent logged a HEAD warning and
// was retried on the next tick, forever.
func isNotFoundErr(err error) bool {
	return errors.Is(err, s3adapter.ErrObjectNotFound)
}
