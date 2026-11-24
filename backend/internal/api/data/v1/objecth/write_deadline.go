package objecth

import (
	"context"
	"errors"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// WriteDeadlines reads the deadline a URL that writes a bucket must expire
// by: set while a storage migration empties the bucket (ADR-0051), so the
// migration can finish once its last URL has expired. ok false: nothing
// bounds a URL for the bucket but its own TTL.
type WriteDeadlines interface {
	UploadWritesCloseAt(ctx context.Context, tenantID uuid.UUID, backendID, bucket string) (deadline time.Time, ok bool, err error)
}

// ErrWritesClosed is a URL asked for a bucket whose writes have closed: a
// storage migration is emptying it, and every URL that could write it has
// expired. A new upload goes to the bucket the collection is bound to now.
var ErrWritesClosed = errors.New("the bucket this upload was started in is being emptied by a storage migration; start a new upload")

func init() {
	apiutil.RegisterError(ErrWritesClosed, connect.CodeFailedPrecondition, commonv1.ErrorReason_ERROR_REASON_FAILED_PRECONDITION)
}

// CapWriteTTL is ttl cut so a URL signed now expires by deadline (ok: there
// is one). A deadline already passed is ErrWritesClosed.
func CapWriteTTL(ttl time.Duration, deadline time.Time, ok bool, now time.Time) (time.Duration, error) {
	if !ok {
		return ttl, nil
	}
	left := deadline.Sub(now)
	if left <= 0 {
		return 0, ErrWritesClosed
	}
	return min(ttl, left), nil
}
