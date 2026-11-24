package objecth

import (
	"context"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
)

// StoredAt is where obj's bytes are — the bucket the object is placed in
// (Object.Stored, ADR-0051), never the collection's binding, which a storage
// migration or an operator may have moved since — admitted by the bucket
// gate for access there (BucketRef.At).
func StoredAt(obj Object, access bucketgate.Access) (Location, error) {
	return obj.Stored.At(obj.TenantID, obj.Collection, obj.Key, access)
}

// deletedBytesAt is the bucket a permanent delete of obj removes bytes from
// and owes them in, admitted by the gate as a write of bytes: the bucket the
// object is placed in; for a PENDING object, placed nowhere, the one its
// upload URL was signed for, where an upload not yet completed may be; and
// for one with no anchor either (a row from before migration 113), its
// collection's binding, as every write was then.
func (h *Handler) deletedBytesAt(ctx context.Context, tenantID uuid.UUID, obj Object) (backendID, bucket string, err error) {
	ref := obj.Stored
	if ref == nil {
		ref = obj.Upload
	}
	if ref == nil {
		return h.repo.LookupBucket(ctx, tenantID, obj.Collection, bucketgate.BytesWrite)
	}
	at, err := ref.At(tenantID, obj.Collection, obj.Key, bucketgate.BytesWrite)
	return at.BackendID, at.Bucket, err
}
