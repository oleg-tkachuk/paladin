package bucketh

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// A bucket row says Paladin manages that physical bucket: its policy — public
// read included — lifecycle, versioning, and its deletion. So a row is never
// written for a bucket Paladin did not mean to take: provision_on_backend
// creates a new bucket and refuses one that exists; registering an existing
// one is asked for by name, with provision_on_backend false; Paladin's own
// buckets are never registered; and only a bucket Paladin created is deleted
// on the backend.

var (
	// ErrBucketExistsOnBackend: provision_on_backend for a bucket the backend
	// already holds.
	ErrBucketExistsOnBackend = errors.New("the bucket already exists on the backend")
	// ErrBucketReserved: a bucket Paladin itself uses on the backend.
	ErrBucketReserved = errors.New("the bucket is reserved for Paladin's own use")
	// ErrBucketNotOnBackend: registering, without provisioning, a bucket the
	// backend does not hold.
	ErrBucketNotOnBackend = errors.New("the bucket does not exist on the backend")
	// ErrBucketNotCreatedByPaladin: a delete on the backend of an adopted bucket.
	ErrBucketNotCreatedByPaladin = errors.New("the bucket was not created by Paladin")
)

func init() {
	apiutil.RegisterError(ErrBucketExistsOnBackend, connect.CodeAlreadyExists,
		commonv1.ErrorReason_ERROR_REASON_BUCKET_EXISTS_ON_BACKEND)
	apiutil.RegisterError(ErrBucketReserved, connect.CodeFailedPrecondition,
		commonv1.ErrorReason_ERROR_REASON_BUCKET_RESERVED)
	apiutil.RegisterError(ErrBucketNotOnBackend, connect.CodeFailedPrecondition,
		commonv1.ErrorReason_ERROR_REASON_BUCKET_NOT_ON_BACKEND)
	apiutil.RegisterError(ErrBucketNotCreatedByPaladin, connect.CodeFailedPrecondition,
		commonv1.ErrorReason_ERROR_REASON_BUCKET_NOT_CREATED_BY_PALADIN)
}

// ReservedBuckets are the buckets Paladin uses itself and never registers.
type ReservedBuckets struct {
	// ByBackend is each backend's configured bucket (storage.backends.<id>.
	// bucket): the default for calls that name no bucket, holding data the
	// registry knows nothing of.
	ByBackend map[string]string
	// Prefix starts the scratch buckets a feature probe creates and removes.
	Prefix string
}

// SetReservedBuckets records the buckets no request may register.
func (h *Handler) SetReservedBuckets(r ReservedBuckets) { h.reserved = r }

func (h *Handler) isReserved(backendID, bucket string) bool {
	if name, ok := h.reserved.ByBackend[backendID]; ok && name == bucket {
		return true
	}
	return h.reserved.Prefix != "" && strings.HasPrefix(bucket, h.reserved.Prefix)
}

// checkOwnership refuses a row for a bucket Paladin should not take:
// a reserved one, an existing one to provision, or a missing one to adopt.
func (h *Handler) checkOwnership(ctx context.Context, backendID, bucket string, provision bool) error {
	if h.isReserved(backendID, bucket) {
		return apiutil.MapError(fmt.Errorf("%w: %q on backend %q", ErrBucketReserved, bucket, backendID))
	}
	if h.provisioner == nil {
		return connect.NewError(connect.CodeUnavailable, errors.New("backend provisioning not wired"))
	}
	exists, err := h.provisioner.BucketExists(ctx, backendID, bucket)
	if err != nil {
		return connect.NewError(connect.CodeUnavailable, fmt.Errorf("check bucket on backend: %w", err))
	}
	switch {
	case provision && exists:
		return apiutil.MapError(fmt.Errorf(
			"%w: %q; to manage an existing bucket, register it with provision_on_backend false",
			ErrBucketExistsOnBackend, bucket))
	case !provision && !exists:
		return apiutil.MapError(fmt.Errorf("%w: %q", ErrBucketNotOnBackend, bucket))
	}
	return nil
}
