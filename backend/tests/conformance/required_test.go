//go:build conformance

package conformance

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/multiparth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/s3adapter"
)

// TestConformance walks the whole contract in one test so the profile is
// printed once, in order, for one backend — and so the required steps run
// against state the earlier ones created, which is how the product uses them.
func TestConformance(t *testing.T) {
	ctx := context.Background()
	tg := newTarget(t)
	defer reportProfile(t, tg)

	// ─── required ──────────────────────────────────────────────────────
	// Each of these is load-bearing: a backend that fails one cannot serve
	// Paladin's upload path at all.

	key := "conf/hello.txt"
	body := []byte("paladin conformance probe")

	t.Run("required/presigned PUT is usable by a plain HTTP client", func(t *testing.T) {
		url, headers, _, err := tg.client.PresignPut(ctx, objecth.PresignPutArgs{
			Bucket: tg.bucket, TenantID: tg.tenant, Collection: tg.collection,
			Key: key, ContentType: "text/plain", SizeHint: int64(len(body)), TTL: ttl(),
		})
		if err != nil {
			t.Fatalf("PresignPut: %v", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
		if err != nil {
			t.Fatalf("build PUT: %v", err)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PUT to presigned URL: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode/100 != 2 {
			b, _ := io.ReadAll(res.Body)
			t.Fatalf("presigned PUT: status %d\n%s", res.StatusCode, string(b))
		}
	})

	t.Run("required/HEAD returns the object just written", func(t *testing.T) {
		etag, size, checksum, sequencer, err := tg.client.Head(ctx, tg.bucket, tg.tenant, tg.collection, key)
		if err != nil {
			t.Fatalf("Head: %v", err)
		}
		if size != int64(len(body)) {
			t.Errorf("size = %d, want %d", size, len(body))
		}
		if etag == "" {
			t.Error("HEAD returned no ETag — the promote path compares it")
		}
		// Not required, measured: the reconciler uses these when present.
		tg.record("head.checksum", yesNo(checksum != ""), checksum)
		tg.record("head.sequencer", yesNo(sequencer != ""), sequencer)
	})

	t.Run("required/GetStream reads the bytes back", func(t *testing.T) {
		rc, _, err := tg.client.GetStream(ctx, tg.bucket, tg.tenant, tg.collection, key)
		if err != nil {
			t.Fatalf("GetStream: %v", err)
		}
		defer rc.Close()
		got, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("read stream: %v", err)
		}
		if !bytes.Equal(got, body) {
			t.Errorf("stream returned %q, want %q", got, body)
		}
	})

	t.Run("required/HEAD of a missing key is classified as not-found", func(t *testing.T) {
		// The reconciler makes a terminal decision on this: anything other
		// than ErrObjectNotFound must stay retryable, and anything that is
		// not-found must say so.
		_, _, _, _, err := tg.client.Head(ctx, tg.bucket, tg.tenant, tg.collection, "conf/definitely-absent")
		if err == nil {
			t.Fatal("HEAD of an absent key returned success")
		}
		if !errors.Is(err, s3adapter.ErrObjectNotFound) {
			t.Fatalf("absent key not classified as not-found: %v", err)
		}
	})

	t.Run("required/CopyObject duplicates within the backend", func(t *testing.T) {
		src := objecth.Location{Bucket: tg.bucket, TenantID: tg.tenant, Collection: tg.collection, Key: key}
		dst := src
		dst.Key = "conf/copied.txt"
		if err := tg.client.CopyObject(ctx, src, dst); err != nil {
			t.Fatalf("CopyObject: %v", err)
		}
		if _, size, _, _, err := tg.client.Head(ctx, tg.bucket, tg.tenant, tg.collection, dst.Key); err != nil {
			t.Fatalf("HEAD the copy: %v", err)
		} else if size != int64(len(body)) {
			t.Errorf("copy size = %d, want %d", size, len(body))
		}
		_ = tg.client.DeleteObject(ctx, tg.bucket, tg.tenant, tg.collection, dst.Key)
	})

	t.Run("required/multipart round trip", func(t *testing.T) {
		mkey := "conf/multi.bin"
		uploadID, err := tg.client.InitiateMultipart(ctx, tg.bucket, tg.tenant, tg.collection, mkey, "application/octet-stream")
		if err != nil {
			t.Fatalf("InitiateMultipart: %v", err)
		}
		// One part: S3 allows a final part below the 5 MiB floor, and a
		// single-part upload is the shape the console produces for small
		// files that still went the multipart route.
		part := bytes.Repeat([]byte("x"), 1024)
		purl, pheaders, _, err := tg.client.PresignPart(ctx, tg.bucket, tg.tenant, uploadID, tg.collection, mkey, 1, ttl())
		if err != nil {
			t.Fatalf("PresignPart: %v", err)
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPut, purl, bytes.NewReader(part))
		for k, v := range pheaders {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PUT part: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode/100 != 2 {
			b, _ := io.ReadAll(res.Body)
			t.Fatalf("part upload: status %d\n%s", res.StatusCode, string(b))
		}
		etag := strings.Trim(res.Header.Get("ETag"), `"`)
		if etag == "" {
			t.Fatal("part upload returned no ETag — CompleteMultipart needs it")
		}
		if _, _, err := tg.client.CompleteMultipart(ctx, tg.bucket, tg.tenant, uploadID, tg.collection, mkey,
			[]multiparth.PartETag{{PartNumber: 1, ETag: etag}}); err != nil {
			t.Fatalf("CompleteMultipart: %v", err)
		}
		if _, size, _, _, err := tg.client.Head(ctx, tg.bucket, tg.tenant, tg.collection, mkey); err != nil {
			t.Fatalf("HEAD the completed upload: %v", err)
		} else if size != int64(len(part)) {
			t.Errorf("completed size = %d, want %d", size, len(part))
		}
		_ = tg.client.DeleteObject(ctx, tg.bucket, tg.tenant, tg.collection, mkey)
	})

	t.Run("required/DeleteObject removes it", func(t *testing.T) {
		if err := tg.client.DeleteObject(ctx, tg.bucket, tg.tenant, tg.collection, key); err != nil {
			t.Fatalf("DeleteObject: %v", err)
		}
		if _, _, _, _, err := tg.client.Head(ctx, tg.bucket, tg.tenant, tg.collection, key); !errors.Is(err, s3adapter.ErrObjectNotFound) {
			t.Errorf("after delete, HEAD = %v, want not-found", err)
		}
	})
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
