package objecth

import (
	"context"
	"errors"
	"uuid"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
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
// object is placed in, or for a PENDING object, placed nowhere, the one its
// upload URL was signed for, where an upload not yet completed may be. The
// schema gives every row one of the two (migration 132); a row with neither
// is ErrNotStored, logged, and nothing is deleted.
func deletedBytesAt(ctx context.Context, tenantID uuid.UUID, obj Object) (backendID, bucket string, err error) {
	ref := obj.Stored
	if ref == nil {
		ref = obj.Upload
	}
	at, err := ref.At(tenantID, obj.Collection, obj.Key, bucketgate.BytesWrite)
	if errors.Is(err, ErrNotStored) {
		logger.FromContext(ctx).Error("object is neither placed nor anchored; refusing its permanent delete",
			append(objectFields(obj.Collection, obj.ObjectID.String()), zap.String(fieldState, string(obj.State)))...)
	}
	return at.BackendID, at.Bucket, err
}
