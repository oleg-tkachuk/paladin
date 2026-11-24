package s3adapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
)

// MaxServerSideCopyBytes is the largest object one S3 CopyObject request may
// copy (5 GiB). A larger object on the same backend is streamed instead,
// since a single CopyObject refuses it.
const MaxServerSideCopyBytes int64 = 5 << 30

// ErrReplicaMismatch is a replica whose bytes did not match the source: a
// size other than the row's, or a digest other than the source's. The partial
// replica is removed. It is transient — the source may have been rewritten
// under the copy — so the replication worker retries it and holds its
// watermark meanwhile.
var ErrReplicaMismatch = errors.New("s3adapter: the replica does not match its source")

// ErrReplicaDestinationNotFound is a not-found the destination answered: its
// bucket missing, or the multipart upload gone. It is never the source's
// object being absent, and the replication worker holds on it.
var ErrReplicaDestinationNotFound = errors.New("s3adapter: the replica's destination answered not found")

// plainMD5ETag matches an ETag that is the hex MD5 of the object's bytes: a
// single-part upload's, with no "-<parts>" suffix.
var plainMD5ETag = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Replicate copies one object for bucket replication, from src to dst, on any
// two backends, and verifies the replica. sizeBytes is the object's size as
// its row records it, and decides the path:
//
//   - same backend, at most MaxServerSideCopyBytes: one server-side
//     CopyObject, and no bytes pass through this process; the replica's size
//     is then read back with a HEAD;
//   - otherwise: a GET on the source streamed into the destination's
//     multipart writer, which holds one part in memory at a time, so an
//     object of any size copies without being buffered whole; the bytes
//     written are counted, and digested where the source's ETag is a digest.
//
// A replica that does not match is deleted and answers ErrReplicaMismatch.
// ErrObjectNotFound means the source's key is absent from a bucket confirmed
// reachable, never a not-found whose cause is unknown; a not-found from the
// destination answers ErrReplicaDestinationNotFound. Whether any other
// failure is worth retrying is IsTransient's to say.
func (r *ObjectRouter) Replicate(ctx context.Context, src, dst objecth.Location, sizeBytes int64) error {
	if src.BackendID != dst.BackendID || sizeBytes > MaxServerSideCopyBytes {
		return r.replicaStream(ctx, src, dst, sizeBytes)
	}
	c, err := r.reg.For(ctx, dst.BackendID)
	if err != nil {
		return err
	}
	if err := c.CopyObject(ctx, src, dst); err != nil {
		if notFound(err) {
			// The copy names two buckets and one key that must exist: the
			// source's. Only with both buckets confirmed is the key the
			// missing thing.
			return c.qualifyNotFound(ctx, err, c.resolveBucket(src.Bucket), c.resolveBucket(dst.Bucket))
		}
		return err
	}
	_, got, _, _, err := c.Head(ctx, dst.Bucket, dst.TenantID, dst.Collection, dst.Key, "")
	if err != nil {
		return fmt.Errorf("replica: read back the copy: %w", err)
	}
	if got != sizeBytes {
		return c.discardReplica(ctx, dst, fmt.Errorf("%w: copy holds %d bytes, the row %d", ErrReplicaMismatch, got, sizeBytes))
	}
	return nil
}

// replicaStream copies src to dst through this process and verifies the
// bytes written against the row's size and the source's digest.
func (r *ObjectRouter) replicaStream(ctx context.Context, src, dst objecth.Location, sizeBytes int64) error {
	srcC, err := r.reg.For(ctx, src.BackendID)
	if err != nil {
		return fmt.Errorf("replica: source backend: %w", err)
	}
	dstC, err := r.reg.For(ctx, dst.BackendID)
	if err != nil {
		return fmt.Errorf("replica: destination backend: %w", err)
	}
	fullKey, err := composeKey(src.TenantID, src.Collection, src.Key)
	if err != nil {
		return fmt.Errorf("replica: source key: %w", err)
	}
	srcBucket := srcC.resolveBucket(src.Bucket)
	out, err := srcC.s3.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(srcBucket), Key: aws.String(fullKey)})
	if notFound(err) {
		return srcC.qualifyNotFound(ctx, err, srcBucket)
	}
	if err != nil {
		return fmt.Errorf("replica: open source: %w", err)
	}
	defer func() { _ = out.Body.Close() }()
	if out.ContentLength != nil && *out.ContentLength != sizeBytes {
		// Rewritten since the row was read: nothing is written, and a later
		// attempt reads the row again.
		return fmt.Errorf("%w: source holds %d bytes, the row %d", ErrReplicaMismatch, *out.ContentLength, sizeBytes)
	}

	w, err := dstC.OpenWithHeaders(ctx, dst.Bucket, dst.TenantID, dst.Collection, dst.Key, sourceHeaders(out), sizeBytes)
	if err != nil {
		return destinationErr("open destination", err)
	}
	if _, err := io.Copy(w, out.Body); err != nil {
		_ = w.Abort()
		return destinationErr("transfer", err)
	}
	_, written, digest, err := w.Close()
	if err != nil {
		return destinationErr("finalize destination", err)
	}
	if written != sizeBytes {
		return dstC.discardReplica(ctx, dst, fmt.Errorf("%w: wrote %d bytes, the row %d", ErrReplicaMismatch, written, sizeBytes))
	}
	if want, ok := sourceDigest(out); ok && digest != want {
		return dstC.discardReplica(ctx, dst, fmt.Errorf("%w: wrote md5 %s, the source's ETag is %s", ErrReplicaMismatch, digest, want))
	}
	return nil
}

// sourceHeaders are the stored headers and user metadata the source's GET
// answered with, which a streamed replica is written with: a server-side
// copy keeps them by itself (MetadataDirective COPY), a stream must restate
// them.
func sourceHeaders(out *s3.GetObjectOutput) ObjectHeaders {
	return ObjectHeaders{
		ContentType:        aws.ToString(out.ContentType),
		CacheControl:       aws.ToString(out.CacheControl),
		ContentDisposition: aws.ToString(out.ContentDisposition),
		ContentEncoding:    aws.ToString(out.ContentEncoding),
		ContentLanguage:    aws.ToString(out.ContentLanguage),
		Metadata:           out.Metadata,
	}
}

// sourceDigest is the MD5 a GET's ETag states, when it states one: a
// single-part object stored without KMS or customer-key encryption, whose
// ETag is then the hex MD5 of its bytes.
func sourceDigest(out *s3.GetObjectOutput) (string, bool) {
	if out.ServerSideEncryption == s3types.ServerSideEncryptionAwsKms ||
		out.ServerSideEncryption == s3types.ServerSideEncryptionAwsKmsDsse ||
		out.SSECustomerAlgorithm != nil {
		return "", false
	}
	etag := strings.ToLower(strings.Trim(aws.ToString(out.ETag), `"`))
	return etag, plainMD5ETag.MatchString(etag)
}

// destinationErr wraps a failure on the destination side, marking a
// not-found there so it is never read as the source's object being absent.
func destinationErr(step string, err error) error {
	if notFound(err) || bucketGone(err) || bucketAbsent(err) {
		return fmt.Errorf("replica: %s: %w: %w", step, ErrReplicaDestinationNotFound, err)
	}
	return fmt.Errorf("replica: %s: %w", step, err)
}

// discardReplica deletes a replica that failed verification and returns
// mismatch, with the delete's failure when it fails too.
func (c *Client) discardReplica(ctx context.Context, dst objecth.Location, mismatch error) error {
	if err := c.DeleteObject(ctx, dst.Bucket, dst.TenantID, dst.Collection, dst.Key); err != nil {
		return fmt.Errorf("%w (removing the replica failed too: %w)", mismatch, err)
	}
	return mismatch
}

// qualifyNotFound turns a not-found into ErrObjectNotFound only once every
// bucket the request named is confirmed reachable, as Head does: until then
// the 404 may be a bucket's, and the answer is a plain error a later attempt
// decides.
func (c *Client) qualifyNotFound(ctx context.Context, err error, buckets ...string) error {
	for _, b := range buckets {
		if berr := c.confirmBucket(ctx, b); berr != nil {
			return fmt.Errorf("bucket %q unreachable, so the 404 does not mean the object is absent: %w (request: %w)",
				b, berr, err)
		}
	}
	return fmt.Errorf("%w: %w", ErrObjectNotFound, err)
}

// IsTransient reports whether err is a failure a later attempt may cure: the
// ones the SDK's own standard retryer retries (throttling, 5xx, a dropped or
// refused connection), a body cut short mid-stream, and a replica that did
// not match its source. Everything else — a refusal, a missing bucket, a bad
// key, a cancelled context — is not.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, ErrReplicaMismatch) {
		return true
	}
	return retry.IsErrorRetryables(retry.DefaultRetryables).IsErrorRetryable(err) == aws.TrueTernary
}
