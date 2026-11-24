package objecth

import (
	"errors"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
)

// BucketRef is a bucket an object's row names, with the bucket gate's state
// there: the bucket its bytes are in (Object.Stored), or the one a PENDING
// object's upload URL was signed for (Object.Upload).
type BucketRef struct {
	BackendID string
	Bucket    string
	State     bucketgate.State
}

// ErrNotStored is an object whose bytes are confirmed in no bucket: a
// PENDING upload. There is nothing to read, copy or HEAD.
var ErrNotStored = errors.New("the object's bytes are not stored in any bucket yet")

// At is the location of the object (tenantID, collection, key) in b, admitted
// by the bucket gate for access there. A refusal is the gate's
// (bucketgate.Refused), for MapResolveErr; a nil b — an object placed
// nowhere — is ErrNotStored.
func (b *BucketRef) At(tenantID uuid.UUID, collection, key string, access bucketgate.Access) (Location, error) {
	if b == nil {
		return Location{}, ErrNotStored
	}
	if err := bucketgate.Check(access, b.State); err != nil {
		return Location{}, err
	}
	return Location{
		BackendID:  b.BackendID,
		TenantID:   tenantID,
		Bucket:     b.Bucket,
		Collection: collection,
		Key:        key,
	}, nil
}
