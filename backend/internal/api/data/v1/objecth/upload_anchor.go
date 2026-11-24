package objecth

import (
	"context"
	"errors"
	"fmt"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
)

// ErrNotAnchored is a PENDING object whose row names no bucket its upload URL
// was signed for: nothing says where its bytes may be. The schema refuses
// such a row (objects_pending_anchored, migration 132).
var ErrNotAnchored = errors.New("the upload is anchored to no bucket")

// uploadedBytes reads what the backend holds for obj's upload in the bucket
// its URL was signed for (Object.Upload): the one bucket a URL of the row can
// write, where the promote places the object (ADR-0051). Reading there is a
// read; the completion asked the write gate of the collection already.
func (h *Handler) uploadedBytes(ctx context.Context, obj Object) (Location, storedBytes, error) {
	at, err := obj.Upload.At(obj.TenantID, obj.Collection, obj.Key, bucketgate.Read)
	if errors.Is(err, ErrNotStored) {
		return Location{}, storedBytes{}, fmt.Errorf("object %s: %w", obj.ObjectID, ErrNotAnchored)
	}
	if err != nil {
		return Location{}, storedBytes{}, MapResolveErr(err)
	}
	b, err := h.head(ctx, at, obj.ChecksumAlgo)
	return at, b, err
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
