//go:build conformance

package conformance

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/object"
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
		_, _, _, _, err := tg.client.Head(ctx, gone, tg.tenant, tg.collection, "any")
		switch {
		case err == nil:
			tg.record("head.missing_bucket_not_notfound", "unknown", "HEAD on a missing bucket succeeded")
		case errors.Is(err, s3adapter.ErrObjectNotFound):
			tg.record("head.missing_bucket_not_notfound", "no", "a missing bucket reads as a missing object")
		default:
			tg.record("head.missing_bucket_not_notfound", "yes", "")
		}
	})

	t.Run("checksum algorithms on presigned PUT", func(t *testing.T) {
		// RequireChecksum makes the adapter demand a checksum header on the
		// PUT. A backend that rejects the algorithm fails the upload, so this
		// decides whether checksum enforcement can be turned on at all.
		for _, algo := range []string{"SHA256", "CRC32C"} {
			key := "conf/checksum-" + algo
			url, headers, _, err := tg.client.PresignPut(ctx, object.PresignPutArgs{
				Bucket: tg.bucket, TenantID: tg.tenant, Collection: tg.collection,
				Key: key, ContentType: "text/plain", ChecksumAlgo: algo,
				SizeHint: 4, TTL: ttl(), RequireChecksum: true,
			})
			if err != nil {
				tg.record("presign.checksum."+algo, "no", errText(err))
				continue
			}
			req, _ := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader([]byte("abcd")))
			for k, v := range headers {
				req.Header.Set(k, v)
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				tg.record("presign.checksum."+algo, "unknown", errText(err))
				continue
			}
			b, _ := io.ReadAll(res.Body)
			res.Body.Close()
			if res.StatusCode/100 == 2 {
				tg.record("presign.checksum."+algo, "yes", "")
				_ = tg.client.DeleteObject(ctx, tg.bucket, tg.tenant, tg.collection, key)
			} else {
				tg.record("presign.checksum."+algo, "no", trim(string(b)))
			}
		}
	})

	t.Run("presigned POST", func(t *testing.T) {
		// Browser form uploads. Not on Paladin's current path, but the adapter
		// exposes it, so whether it works decides if it can be.
		_, _, _, err := tg.client.PresignPost(ctx, object.PresignPostArgs{
			Bucket: tg.bucket, TenantID: tg.tenant, Collection: tg.collection,
			Key: "conf/post.txt", ContentType: "text/plain", TTL: ttl(),
		})
		tg.record("presign.post", yesNo(err == nil), errText(err))
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
	url, headers, _, err := tg.client.PresignPut(ctx, object.PresignPutArgs{
		Bucket: tg.bucket, TenantID: tg.tenant, Collection: tg.collection,
		Key: key, ContentType: "text/plain", SizeHint: int64(len(body)), TTL: ttl(),
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		b, _ := io.ReadAll(res.Body)
		return errors.New(trim(string(b)))
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
