//go:build conformance

package conformance

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/multiparth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/checksum"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/s3adapter"
)

// TestCapabilities measures what varies. Nothing here fails the run: every
// probe records yes / no / unknown, and the profile is the output.
//
// The distinction from required_test.go is the design decision this suite
// exists to support. A divergence that fails a build gets special-cased in
// production code until nobody can say what is actually required; a divergence
// that is measured can be designed around once, at the seam.
func TestCapabilities(t *testing.T) {
	ctx := context.Background()
	tg := newTarget(t)
	defer reportProfile(t, tg)

	t.Run("bucket tagging", func(t *testing.T) {
		// CreateBucket calls this to stamp the owning tenant. Garage has no
		// bucket tagging, so on that backend the call fails and the caller
		// has to decide: skip, or refuse to provision. Today it just errors.
		err := tg.client.TagBucketOwner(ctx, "", tg.bucket, tg.tenant)
		tg.record("bucket.tagging", yesNo(err == nil), errText(err))
	})

	t.Run("a missing bucket is not reported as a missing object", func(t *testing.T) {
		// The property ReconcilerV2 depends on: not-found is terminal, so
		// "the bucket is gone" must never arrive as "the object is gone" or
		// a whole binding's pending uploads get marked FAILED.
		//
		// Note what this does NOT measure. Whether the backend's bodiless 404
		// carries an error code is invisible from here, because Head confirms
		// the bucket with HeadBucket before it returns the sentinel — so both
		// kinds of backend produce the same non-sentinel error. Measuring the
		// underlying wire behaviour would need a raw signed HEAD, below the
		// adapter, which is a different suite than this one. Naming the probe
		// after what it actually observes is the point; the earlier name
		// claimed the wire answer and would have reported "yes" for every
		// backend alike.
		gone := tg.bucket + "-absent"
		_, _, _, _, err := tg.client.Head(ctx, gone, tg.tenant, tg.collection, "any", "")
		switch {
		case err == nil:
			tg.record("head.missing_bucket_not_notfound", "unknown", "HEAD on a missing bucket succeeded")
		case errors.Is(err, s3adapter.ErrObjectNotFound):
			tg.record("head.missing_bucket_not_notfound", "no", "a missing bucket reads as a missing object")
		default:
			tg.record("head.missing_bucket_not_notfound", "yes", "")
		}
	})

	t.Run("multipart completion checks part checksums", func(t *testing.T) {
		// Each part's checksum is already checked as the part arrives, so this
		// is a second line: whether the store also compares the completion
		// list's checksums with the parts it recorded.
		mkey := "conf/multi-checks"
		uploadID, err := tg.client.InitiateMultipart(ctx, tg.bucket, tg.tenant, tg.collection, mkey, "application/octet-stream", checksum.SHA256)
		if err != nil {
			tg.record("multipart.complete_checks_part_checksums", "unknown", errText(err))
			return
		}
		part := []byte("part-one")
		b := multiparth.PartBinding{Number: 1, SizeBytes: int64(len(part)), ChecksumAlgo: checksum.SHA256, ChecksumValue: digest(checksum.SHA256, part)}
		purl, ph, _, err := tg.client.PresignPart(ctx, tg.bucket, tg.tenant, uploadID, tg.collection, mkey, b, ttl())
		if err != nil {
			tg.record("multipart.complete_checks_part_checksums", "unknown", errText(err))
			return
		}
		status, msg, hdr, err := send(ctx, http.MethodPut, purl, ph, part)
		if err != nil || !ok(status) {
			tg.record("multipart.complete_checks_part_checksums", "unknown", msg+errText(err))
			return
		}
		wrong := digest(checksum.SHA256, []byte("something else"))
		_, _, err = tg.client.CompleteMultipart(ctx, tg.bucket, tg.tenant, uploadID, tg.collection, mkey, checksum.SHA256,
			[]multiparth.PartETag{{PartNumber: 1, ETag: hdr.Get("ETag"), ChecksumValue: wrong}})
		tg.record("multipart.complete_checks_part_checksums", yesNo(err != nil), errText(err))
		_ = tg.client.AbortMultipart(ctx, tg.bucket, tg.tenant, uploadID, tg.collection, mkey)
		_ = tg.client.DeleteObject(ctx, tg.bucket, tg.tenant, tg.collection, mkey)
	})

	t.Run("a presigned GET bound by If-Match", func(t *testing.T) {
		// DownloadObject's require_etag_match signs If-Match; whether the
		// store refuses a stale ETag decides whether that binding holds.
		key := "conf/if-match.txt"
		if err := putSmall(ctx, tg, key, []byte("x")); err != nil {
			tg.record("presign.get_if_match", "unknown", errText(err))
			return
		}
		url, headers, _, err := tg.client.PresignGet(ctx, objecth.PresignGetArgs{
			Bucket: tg.bucket, TenantID: tg.tenant, Collection: tg.collection, Key: key, TTL: ttl(),
			IfMatch: "not-the-etag",
		})
		if err != nil {
			tg.record("presign.get_if_match", "unknown", errText(err))
			return
		}
		status, _, _, err := send(ctx, http.MethodGet, url, headers, nil)
		tg.record("presign.get_if_match", yesNo(err == nil && status == http.StatusPreconditionFailed), errText(err))
		_ = tg.client.DeleteObject(ctx, tg.bucket, tg.tenant, tg.collection, key)
	})

	t.Run("bucket delete on a non-empty bucket", func(t *testing.T) {
		// Paladin refuses this itself, before the backend sees it. What the
		// backend does anyway decides whether that guard is defence in depth
		// or the only thing standing there.
		key := "conf/blocker.txt"
		if err := putSmall(ctx, tg, key, []byte("x")); err != nil {
			tg.record("bucket.delete_refuses_nonempty", "unknown", errText(err))
			return
		}
		err := tg.client.DeleteBucket(ctx, "", tg.bucket)
		tg.record("bucket.delete_refuses_nonempty", yesNo(err != nil), errText(err))
		if err == nil {
			// It deleted a non-empty bucket. Put it back so the rest of the
			// run has somewhere to write.
			tg.record("bucket.delete_destroyed_data", "yes", "non-empty bucket was removed")
			_ = tg.client.CreateBucket(ctx, "", tg.bucket, "us-east-1")
			return
		}
		_ = tg.client.DeleteObject(ctx, tg.bucket, tg.tenant, tg.collection, key)
	})
}

func putSmall(ctx context.Context, tg *target, key string, body []byte) error {
	status, msg, err := putSigned(ctx, tg, key, checksum.SHA256, body, body)
	if err != nil {
		return err
	}
	if !ok(status) {
		return errors.New(msg)
	}
	return nil
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return trim(err.Error())
}

func trim(s string) string {
	s = string(bytes.ReplaceAll([]byte(s), []byte("\n"), []byte(" ")))
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}
