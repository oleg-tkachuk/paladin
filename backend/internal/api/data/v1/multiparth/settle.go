package multiparth

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// maxBindingMoves bounds how often one completion follows its collection to
// another bucket: a storage migration rebinds a tenant once, and once more if
// it rolls back, so a third move in one call is not a migration's doing.
const maxBindingMoves = 2

// logFieldUploadID is the log key of a multipart upload's id.
const logFieldUploadID = "upload_id"

// msgUploadNotMoved answers a completion whose assembled object could not be
// moved into the collection's bucket. The object stays PENDING, anchored to
// the session's bucket, and the reconciler settles it from there.
const msgUploadNotMoved = "the assembled object could not be moved into the collection's bucket; it will be settled shortly"

// settle promotes an object the store assembled at anchor, the bucket the
// session was anchored to at initiate, in the bucket its collection is bound
// to now.
//
// A storage migration may have rebound the collection since the session was
// initiated (ADR-0015). The parts are assembled where they were uploaded,
// the anchor, while the object is read from the collection's bucket; and the
// migration's cleanup deletes the anchor's copies. So an object assembled
// anywhere else than the binding is copied there under the same key, its size
// checked, and promoted there (relocate); the anchored copy is left for the
// cleanup, which waits while the row is PENDING and anchored to its source.
//
// The promote checks, under the migration's fence, that the binding is still
// the one the bytes were put in (statemachine.Placement). A rebind between
// the lookup and the promote refuses it, and the object follows the
// collection again, at most maxBindingMoves times: the storage upload is
// consumed, so a client's retry could not assemble it again.
//
// A crash at any step leaves the row PENDING and anchored; the reconciler
// moves the bytes from the anchor and promotes them once the session's
// deadline passes, the same way.
func (h *Handler) settle(ctx context.Context, sess Session, anchor objecth.Location, a assembled, uploadID string) (assembled, error) {
	log := logger.FromContext(ctx).With(zap.String(objecth.LogFieldObjectID, sess.ObjectID.String()),
		zap.String(objecth.LogFieldTenantID, sess.TenantID.String()), zap.String(objecth.LogFieldCollection, sess.Collection),
		zap.String(logFieldUploadID, uploadID))
	for moves := 0; ; moves++ {
		bound, err := h.boundLocation(ctx, sess)
		if err != nil {
			log.Warn("the collection's bucket refuses the assembled object; it stays pending", zap.Error(err))
			return assembled{}, err
		}
		stored := a
		var at []objecth.Location
		if bound.BackendID == anchor.BackendID && bound.Bucket == anchor.Bucket {
			at = []objecth.Location{anchor}
		} else {
			if stored, err = h.relocate(ctx, log, anchor, bound, a); err != nil {
				return assembled{}, err
			}
			at = []objecth.Location{anchor, bound}
		}
		// No sequencer from multipart completion — events will supply one later.
		placed := &statemachine.Placement{BackendID: bound.BackendID, Bucket: bound.Bucket}
		changed, err := h.sm.PromoteToAvailable(ctx, sess.ObjectID, placed, stored.etag, stored.size, "", "", statemachine.SourceRPC)
		switch {
		case errors.Is(err, statemachine.ErrContentMismatch):
			return assembled{}, h.discardMismatched(ctx, sess, uploadID, err, at...)
		case errors.Is(err, statemachine.ErrBindingMoved) && moves < maxBindingMoves:
			log.Info("the collection was rebound since its bucket was looked up; following it", zap.Error(err))
			continue
		case errors.Is(err, statemachine.ErrBindingMoved):
			log.Warn("the collection keeps moving between buckets; the object stays pending for the reconciler", zap.Error(err))
			return assembled{}, connect.NewError(connect.CodeUnavailable, msgUploadNotMoved).WithCause(err)
		case err != nil:
			return assembled{}, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		stored.changed = changed
		return stored, nil
	}
}

// boundLocation is where the session's object is read from once promoted:
// the collection's current binding, through the bucket gate as a write of
// bytes, which the move into it is.
func (h *Handler) boundLocation(ctx context.Context, sess Session) (objecth.Location, error) {
	meta, err := h.repo.LookupBucketMeta(ctx, sess.TenantID, sess.Collection, bucketgate.BytesWrite)
	if err != nil {
		return objecth.Location{}, objecth.MapResolveErr(err)
	}
	return objecth.Location{BackendID: meta.BackendID, TenantID: sess.TenantID, Bucket: meta.BucketName,
		Collection: sess.Collection, Key: sess.Key}, nil
}

// relocate copies the object assembled at from into to under the same key
// and reports what to holds: its ETag, which the object is then read with,
// and its size, which must be the assembled one. The copy is a same-key
// overwrite, so repeating it after a crash or a refused promote is harmless.
func (h *Handler) relocate(ctx context.Context, log *zap.Logger, from, to objecth.Location, a assembled) (assembled, error) {
	log = log.With(zap.String(objecth.LogFieldFromBucket, from.Bucket), zap.String(objecth.LogFieldToBucket, to.Bucket))
	if err := h.storage.CopyObject(ctx, from, to); err != nil {
		log.Error("moving the assembled object into the collection's bucket failed", zap.Error(err))
		return assembled{}, connect.NewError(connect.CodeUnavailable, msgUploadNotMoved).WithCause(fmt.Errorf("move the assembled object: %w", err))
	}
	etag, size, _, _, err := h.storage.Head(ctx, to.BackendID, to.Bucket, to.TenantID, to.Collection, to.Key, "")
	if err != nil {
		log.Error("reading the moved object back failed", zap.Error(err))
		return assembled{}, rpcerr.New(connect.CodeUnavailable, fmt.Errorf("read the moved object: %w", err))
	}
	if size != a.size {
		err := fmt.Errorf("%w: %d, want %d", objecth.ErrRelocatedSize, size, a.size)
		log.Error("moving the assembled object into the collection's bucket failed", zap.Error(err))
		return assembled{}, connect.NewError(connect.CodeUnavailable, msgUploadNotMoved).WithCause(err)
	}
	log.Info("assembled object moved into the collection's bucket, which a storage migration rebound since the upload was initiated")
	return assembled{etag: etag, size: size}, nil
}
