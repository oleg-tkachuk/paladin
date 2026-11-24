package multiparth

import (
	"context"
	"errors"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// logFieldUploadID is the log key of a multipart upload's id.
const logFieldUploadID = "upload_id"

// settle promotes an object the store assembled at anchor, the bucket the
// session was anchored to at initiate, where its parts were uploaded: the
// object's row is anchored there too, and the promote places it there
// (ADR-0051), whatever its collection is bound to by now. A storage
// migration moves it later, as any other object.
//
// The promote settles the anchor: the store consumed the upload when it
// assembled it, so no part URL of the session can write again, and the row
// has no PUT URL (RegenerateUploadURL refuses a row registered without a
// checksum). Left anchored, the row would hold a storage migration until the
// session's deadline, a day after the initiate.
//
// A crash before the promote leaves the row PENDING and anchored; the
// reconciler promotes the assembled object once the session's deadline
// passes.
func (h *Handler) settle(ctx context.Context, sess Session, anchor objecth.Location, a assembled, uploadID string) (assembled, error) {
	log := logger.FromContext(ctx).With(zap.String("object_id", sess.ObjectID.String()),
		zap.String("tenant_id", sess.TenantID.String()), zap.String("collection", sess.Collection),
		zap.String(logFieldUploadID, uploadID))
	// No sequencer from multipart completion, and no storage event is applied
	// to it after: the anchor it would have to come from is settled.
	placed := &statemachine.Placement{BackendID: anchor.BackendID, Bucket: anchor.Bucket, Settled: true}
	changed, err := h.sm.PromoteToAvailable(ctx, sess.ObjectID, placed, a.etag, a.size, "", "", statemachine.SourceRPC)
	switch {
	case errors.Is(err, statemachine.ErrContentMismatch):
		return assembled{}, h.discardMismatched(ctx, sess, uploadID, err, anchor)
	case err != nil:
		log.Error("promoting the assembled object failed", zap.Error(err))
		return assembled{}, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	a.changed = changed
	return a, nil
}
