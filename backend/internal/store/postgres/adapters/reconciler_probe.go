package adapters

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
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
	// CopyObject moves an upload's bytes from the bucket its URL named into
	// the one its collection is bound to (HeadByObjectID).
	CopyObject(ctx context.Context, src, dst objecth.Location) error
}

// ReconcilerProbe satisfies worker.StorageProbe. It looks up the object's
// (tenant, bucket, key) routing in one query, then calls the storage HEAD.
// Missing rows → found=false, no error.
type ReconcilerProbe struct {
	q    *sqlc.Queries
	head HeadProber
	log  *zap.Logger
}

func NewReconcilerProbe(q *sqlc.Queries, head HeadProber) *ReconcilerProbe {
	return &ReconcilerProbe{q: q, head: head, log: zap.NewNop()}
}

// WithLogger has the probe log where it moves an upload between buckets.
func (r *ReconcilerProbe) WithLogger(l *zap.Logger) *ReconcilerProbe {
	if l != nil {
		r.log = l
	}
	return r
}

// HeadByObjectID resolves routing + HEADs the bytes, in the bucket the
// collection is bound to — once they are there.
//
// An upload whose URL named another bucket (objects.upload_bucket_id: a
// storage migration rebound the collection since the presign) is looked for
// there first, and its bytes are copied into the collection's bucket, the
// one the object is read from once promoted (objecth.UploadOrigin). The copy
// is under the same key, so a retry after a crash repeats it harmlessly; the
// anchored copy stays for the migration's cleanup. The copy writes bytes, so
// it asks the bucket gate as a write on the collection's bucket. Not in the
// anchored bucket, the bytes are looked for in the collection's.
//
// placed is the collection's bucket, where found bytes are by then: the
// promote checks the collection is still bound there.
func (r *ReconcilerProbe) HeadByObjectID(ctx context.Context, objectID uuid.UUID) (etag string, sizeBytes int64, checksum, sequencer string, placed statemachine.Placement, found bool, err error) {
	row, err := r.q.LookupObjectByID(ctx, pgUUID(objectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, "", "", placed, false, nil
		}
		return "", 0, "", "", placed, false, fmt.Errorf("lookup object: %w", err)
	}
	placed = statemachine.Placement{BackendID: row.BackendName, Bucket: row.BucketName}
	tenantID := uuidFrom(row.TenantID)
	// LookupObjectByID already materializes the backend binding, so the HEAD
	// is routed to the object's own backend (not the default).
	bound := objecth.Location{BackendID: row.BackendName, TenantID: tenantID, Bucket: row.BucketName, Collection: row.CollectionName, Key: row.Path}
	algo := checksumAlgoName(row.ChecksumAlgorithm)
	anchor, err := r.uploadAnchor(ctx, objectID)
	if err != nil {
		return "", 0, "", "", placed, false, err
	}
	if origin, relocate := objecth.UploadOrigin(anchor, bound); relocate {
		b, moved, err := r.relocate(ctx, objectID, origin, bound, algo, bucketgate.State{
			Enabled: row.Enabled, ReadOnly: row.ReadOnly, ProvisionState: row.ProvisionState,
		}, uploadRelocation)
		if err != nil || moved {
			return b.etag, b.size, b.checksum, b.sequencer, placed, moved, err
		}
	}
	b, found, err := r.headAt(ctx, bound, algo)
	return b.etag, b.size, b.checksum, b.sequencer, placed, found, err
}

// probedBytes is what a HEAD reports of an object's bytes.
type probedBytes struct {
	etag, checksum, sequencer string
	size                      int64
}

// headAt HEADs loc: found false, and no error, when the backend holds
// nothing there.
func (r *ReconcilerProbe) headAt(ctx context.Context, loc objecth.Location, algo string) (probedBytes, bool, error) {
	var b probedBytes
	var err error
	b.etag, b.size, b.checksum, b.sequencer, err = r.head.Head(ctx, loc.BackendID, loc.Bucket, loc.TenantID, loc.Collection, loc.Key, algo)
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

// uploadAnchor is the bucket the object's upload URL was signed for; nil when
// the row has none.
func (r *ReconcilerProbe) uploadAnchor(ctx context.Context, objectID uuid.UUID) (*objecth.UploadAnchor, error) {
	a, err := r.q.LookupUploadBucket(ctx, pgUUID(objectID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lookup upload bucket: %w", err)
	}
	return &objecth.UploadAnchor{BackendID: a.BackendName, Bucket: a.BucketName, State: bucketgate.State{
		Enabled: a.Enabled, ReadOnly: a.ReadOnly, ProvisionState: a.ProvisionState,
	}}, nil
}

// relocation is what one kind of move logs: when origin holds nothing, and
// once the bytes are moved.
type relocation struct {
	notInOrigin string
	moved       string
}

// uploadRelocation is an upload's move from the bucket its URL named.
var uploadRelocation = relocation{
	notInOrigin: "upload not in the bucket its URL named; looking in the collection's bucket",
	moved:       "upload moved into the collection's bucket, which a storage migration rebound since its URL was signed",
}

// lateWriteRelocation is a late source write's move (MoveObjectBytes).
var lateWriteRelocation = relocation{
	notInOrigin: "late source write: the migration's source no longer holds the object",
	moved:       "late source write moved into the collection's bucket, which a storage migration rebound since it was written",
}

// relocate copies an object's bytes from origin into bound, and reports
// what bound then holds. moved is false, with no error, when origin holds
// nothing: the bytes, if any, are in bound.
func (r *ReconcilerProbe) relocate(ctx context.Context, objectID uuid.UUID, origin, bound objecth.Location, algo string, boundState bucketgate.State, what relocation) (probedBytes, bool, error) {
	log := r.log.With(zap.String(objecth.LogFieldObjectID, objectID.String()), zap.String(objecth.LogFieldTenantID, bound.TenantID.String()),
		zap.String(objecth.LogFieldCollection, bound.Collection),
		zap.String(objecth.LogFieldFromBucket, origin.Bucket), zap.String(objecth.LogFieldToBucket, bound.Bucket))
	up, found, err := r.headAt(ctx, origin, algo)
	if err != nil {
		return probedBytes{}, false, err
	}
	if !found {
		log.Info(what.notInOrigin)
		return probedBytes{}, false, nil
	}
	if err := bucketgate.Check(bucketgate.BytesWrite, boundState); err != nil {
		return probedBytes{}, false, fmt.Errorf("move upload %s from %s to %s: %w", objectID, origin.Bucket, bound.Bucket, err)
	}
	if err := r.head.CopyObject(ctx, origin, bound); err != nil {
		return probedBytes{}, false, fmt.Errorf("move upload %s from %s to %s: %w", objectID, origin.Bucket, bound.Bucket, err)
	}
	b, found, err := r.headAt(ctx, bound, algo)
	if err != nil {
		return probedBytes{}, false, err
	}
	if !found || b.size != up.size {
		return probedBytes{}, false, fmt.Errorf("move upload %s from %s to %s: %w: %d, want %d",
			objectID, origin.Bucket, bound.Bucket, objecth.ErrRelocatedSize, b.size, up.size)
	}
	// The checksum the backend verified at the PUT is the upload's; a copy
	// need not carry it.
	if b.checksum == "" {
		b.checksum = up.checksum
	}
	log.Info(what.moved)
	return b, true, nil
}

// MoveObjectBytes copies an object's bytes from from, a storage migration's
// source, into to, the bucket its collection is bound to now, under the same
// key, and reports what to then holds (statemachine.LateSourceWrite). The
// copy writes bytes, so it asks the bucket gate as a write on the
// collection's bucket. A source that holds nothing is
// statemachine.ErrLateWriteGone.
func (r *ReconcilerProbe) MoveObjectBytes(ctx context.Context, objectID uuid.UUID, from, to statemachine.Placement) (statemachine.MovedBytes, error) {
	row, err := r.q.LookupObjectByID(ctx, pgUUID(objectID))
	if err != nil {
		return statemachine.MovedBytes{}, fmt.Errorf("lookup object: %w", err)
	}
	bound := objecth.Location{BackendID: to.BackendID, TenantID: uuidFrom(row.TenantID), Bucket: to.Bucket,
		Collection: row.CollectionName, Key: row.Path}
	origin := bound
	origin.BackendID, origin.Bucket = from.BackendID, from.Bucket
	b, moved, err := r.relocate(ctx, objectID, origin, bound, checksumAlgoName(row.ChecksumAlgorithm), bucketgate.State{
		Enabled: row.Enabled, ReadOnly: row.ReadOnly, ProvisionState: row.ProvisionState,
	}, lateWriteRelocation)
	if err != nil {
		return statemachine.MovedBytes{}, err
	}
	if !moved {
		return statemachine.MovedBytes{}, fmt.Errorf("%w: %s/%s", statemachine.ErrLateWriteGone, from.BackendID, from.Bucket)
	}
	return statemachine.MovedBytes{ETag: b.etag, Size: b.size}, nil
}

// DeleteByObjectID removes an object's stored bytes — the reconciler's
// answer to bytes that broke the object's registration. A row that is gone
// has nothing left to delete for.
//
// The delete is a write of bytes, so it asks the bucket gate as the request
// path does: on a draining or disabled backend, or a bucket not ready, it
// answers the gate's refusal (bucketgate.Refused) and calls nothing.
func (r *ReconcilerProbe) DeleteByObjectID(ctx context.Context, objectID uuid.UUID) error {
	row, err := r.q.LookupObjectByID(ctx, pgUUID(objectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("lookup object: %w", err)
	}
	if err := bucketgate.Check(bucketgate.BytesWrite, bucketgate.State{
		Enabled: row.Enabled, ReadOnly: row.ReadOnly, ProvisionState: row.ProvisionState,
	}); err != nil {
		return err
	}
	return r.head.DeleteObject(ctx, row.BackendName, row.BucketName, uuidFrom(row.TenantID), row.CollectionName, row.Path)
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
