package objecth

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
)

// The log fields an upload's move between buckets is told by, wherever it
// is moved: CompleteObject here, the reconciler's probe in adapters.
const (
	LogFieldObjectID   = fieldObjectID
	LogFieldTenantID   = "tenant_id"
	LogFieldCollection = fieldCollection
	LogFieldFromBucket = "from_bucket"
	LogFieldToBucket   = "to_bucket"
)

// ErrRelocatedSize is a copy of an upload into the collection's bucket that
// did not come out the size the upload's bucket holds.
var ErrRelocatedSize = errors.New("the copy in the collection's bucket is not the size of the upload")

// UploadOrigin is where an upload's bytes are looked for first, and whether
// they then have to move to bound, the bucket the collection is bound to now:
// the anchor, when it names another bucket than bound. No anchor — a row from
// before migration 113 — or one naming bound needs no move.
func UploadOrigin(anchor *BucketRef, bound Location) (Location, bool) {
	if anchor == nil || (anchor.BackendID == bound.BackendID && anchor.Bucket == bound.Bucket) {
		return bound, false
	}
	origin := bound
	origin.BackendID, origin.Bucket = anchor.BackendID, anchor.Bucket
	return origin, true
}

// uploadedBytes reads what the backend holds for obj's upload, ready to
// promote in bound: the stored bytes there, and the ETag the client's upload
// produced, which its claims are checked against.
//
// Where the upload's URL named another bucket (a storage migration rebound
// the collection since the presign), the bytes are copied from there into
// bound first. The copy is under the same key, so a retry after a crash at
// any step repeats it harmlessly; the anchored copy is left in place, for the
// migration's cleanup, which waits until no upload is anchored to its source
// (worker.StorageMigrationWorker). Not there — the client used a URL
// regenerated after the rebind, which names bound — the bytes are looked for
// in bound, as for an unanchored upload.
func (h *Handler) uploadedBytes(ctx context.Context, obj Object, bound Location) (storedBytes, string, error) {
	origin, relocate := UploadOrigin(obj.Upload, bound)
	if !relocate {
		b, err := h.head(ctx, bound, obj.ChecksumAlgo)
		return b, b.etag, err
	}
	log := logger.FromContext(ctx).With(zap.String(LogFieldObjectID, obj.ObjectID.String()),
		zap.String(LogFieldTenantID, bound.TenantID.String()), zap.String(LogFieldCollection, bound.Collection),
		zap.String(LogFieldFromBucket, origin.Bucket), zap.String(LogFieldToBucket, bound.Bucket))
	// Reading the upload's bytes there is a read; the copy writes bound,
	// which the caller's write gate admitted.
	if err := bucketgate.Check(bucketgate.Read, obj.Upload.State); err != nil {
		log.Warn("the bucket gate refuses the bucket the upload's URL named; the upload is not completed", zap.Error(err))
		return storedBytes{}, "", MapResolveErr(err)
	}
	up, err := h.head(ctx, origin, obj.ChecksumAlgo)
	if errors.Is(err, ErrStoredObjectNotFound) {
		log.Info("upload not in the bucket its URL named; looking in the collection's bucket")
		b, err := h.head(ctx, bound, obj.ChecksumAlgo)
		return b, b.etag, err
	}
	if err != nil {
		return storedBytes{}, "", err
	}
	if err := h.storage.CopyObject(ctx, origin, bound); err != nil {
		log.Error("moving the upload into the collection's bucket failed", zap.Error(err))
		return storedBytes{}, "", connect.NewError(connect.CodeUnavailable, msgUploadUnchecked).WithCause(fmt.Errorf("move the upload: %w", err))
	}
	b, err := h.head(ctx, bound, obj.ChecksumAlgo)
	if err != nil {
		return storedBytes{}, "", err
	}
	if b.size != up.size {
		err := fmt.Errorf("%w: %d, want %d", ErrRelocatedSize, b.size, up.size)
		log.Error("moving the upload into the collection's bucket failed", zap.Error(err))
		return storedBytes{}, "", connect.NewError(connect.CodeUnavailable, msgUploadUnchecked).WithCause(err)
	}
	// The checksum the backend verified at the PUT is the upload's; a copy
	// need not carry it.
	if b.checksum == "" {
		b.checksum = up.checksum
	}
	log.Info("upload moved into the collection's bucket, which a storage migration rebound since its URL was signed")
	return b, up.etag, nil
}

// head reads the backend's answer for loc: ErrStoredObjectNotFound when it
// holds nothing there.
func (h *Handler) head(ctx context.Context, loc Location, checksumAlgo string) (storedBytes, error) {
	var (
		b   storedBytes
		err error
	)
	b.etag, b.size, b.checksum, b.sequencer, err = h.storage.Head(ctx, loc.BackendID, loc.Bucket, loc.TenantID, loc.Collection, loc.Key, checksumAlgo)
	return b, err
}
