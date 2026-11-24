package s3adapter

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
)

// MaxServerSideCopyBytes is the largest object one S3 CopyObject request may
// copy (5 GiB). A larger object on the same backend is streamed instead,
// since a single CopyObject refuses it.
const MaxServerSideCopyBytes int64 = 5 << 30

// Replicate copies one object for bucket replication, from src to dst, on any
// two backends. sizeBytes is the object's size as its row records it, and
// decides the path:
//
//   - same backend, at most MaxServerSideCopyBytes: one server-side
//     CopyObject, and no bytes pass through this process;
//   - otherwise: a GET on the source streamed into the destination's
//     multipart writer, which holds one part in memory at a time, so an
//     object of any size copies without being buffered whole.
//
// A source the backend says holds no such object answers ErrObjectNotFound;
// whether any other failure is worth retrying is IsTransient's to say.
func (r *ObjectRouter) Replicate(ctx context.Context, src, dst objecth.Location, sizeBytes int64) error {
	if src.BackendID != dst.BackendID || sizeBytes > MaxServerSideCopyBytes {
		return r.streamThrough(ctx, src, dst)
	}
	c, err := r.reg.For(ctx, dst.BackendID)
	if err != nil {
		return err
	}
	err = c.CopyObject(ctx, src, dst)
	// A server-side copy names one key that must exist, its source: a missing
	// key is the source's.
	if notFound(err) {
		return fmt.Errorf("%w: %w", ErrObjectNotFound, err)
	}
	return err
}

// IsTransient reports whether err is a failure a later attempt may cure: the
// ones the SDK's own standard retryer retries (throttling, 5xx, a dropped or
// refused connection), and a body cut short mid-stream. Everything else — a
// refusal, a missing bucket, a bad key, a cancelled context — is not.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	return retry.IsErrorRetryables(retry.DefaultRetryables).IsErrorRetryable(err) == aws.TrueTernary
}
