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
	"github.com/oleg-tkachuk/paladin/backend/internal/checksum"
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
		status, msg, err := putSigned(ctx, tg, key, checksum.SHA256, body, body)
		if err != nil {
			t.Fatalf("presigned PUT: %v", err)
		}
		if !ok(status) {
			t.Fatalf("presigned PUT: status %d\n%s", status, msg)
		}
	})

	// A presigned URL is bound to one body: its size, its checksum, and to a
	// key nothing occupies. Paladin admits uploads — policy, quota, size caps
	// — against those values, and relies on the store to refuse any other
	// body. A backend that accepts one turns every admission check into a
	// check of something the client did not upload.
	t.Run("required/a PUT whose body breaks the signed checksum is refused", func(t *testing.T) {
		other := bytes.Repeat([]byte("y"), len(body))
		for _, algo := range []string{checksum.SHA256, checksum.CRC32C, checksum.MD5} {
			status, msg, err := putSigned(ctx, tg, "conf/wrong-"+algo, algo, body, other)
			if err != nil {
				t.Fatalf("%s: %v", algo, err)
			}
			if ok(status) {
				t.Errorf("%s: a body with another checksum was stored (status %d)", algo, status)
			}
			_ = msg
		}
	})

	t.Run("required/a PUT of another length is refused", func(t *testing.T) {
		status, _, err := putSigned(ctx, tg, "conf/longer", checksum.SHA256, body, append(append([]byte{}, body...), 'z'))
		if err != nil {
			t.Fatal(err)
		}
		if ok(status) {
			t.Errorf("a longer body was stored (status %d)", status)
		}
	})

	t.Run("required/a PUT cannot overwrite an object at its key", func(t *testing.T) {
		// key holds body from the first step; If-None-Match: * is signed.
		status, _, err := putSigned(ctx, tg, key, checksum.SHA256, body, body)
		if err != nil {
			t.Fatal(err)
		}
		if status != http.StatusPreconditionFailed {
			t.Errorf("overwrite answered %d, want 412", status)
		}
	})

	t.Run("required/a presigned POST stores exactly what its policy binds", func(t *testing.T) {
		pkey := "conf/post.txt"
		status, msg, err := postSigned(ctx, tg, pkey, body, body, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !ok(status) {
			t.Fatalf("POST within its policy: status %d\n%s", status, msg)
		}
		_ = tg.client.DeleteObject(ctx, tg.bucket, tg.tenant, tg.collection, pkey)

		other := bytes.Repeat([]byte("y"), len(body))
		if status, _, err := postSigned(ctx, tg, "conf/post-wrong", body, other, nil); err != nil || ok(status) {
			t.Errorf("POST with another checksum: status %d err %v, want refused", status, err)
		}
		if status, _, err := postSigned(ctx, tg, "conf/post-long", body, append(append([]byte{}, body...), 'z'), nil); err != nil || ok(status) {
			t.Errorf("POST outside its content-length-range: status %d err %v, want refused", status, err)
		}
		if status, _, err := postSigned(ctx, tg, "conf/post-type", body, body, map[string]string{"Content-Type": "image/png"}); err != nil || ok(status) {
			t.Errorf("POST with another Content-Type: status %d err %v, want refused", status, err)
		}
	})

	t.Run("required/HEAD returns the object just written", func(t *testing.T) {
		etag, size, sum, sequencer, err := tg.client.Head(ctx, tg.bucket, tg.tenant, tg.collection, key, checksum.SHA256)
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
		// Promotion compares it with the registered checksum when present.
		tg.record("head.checksum", yesNo(sum == digest(checksum.SHA256, body)), sum)
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
		_, _, _, _, err := tg.client.Head(ctx, tg.bucket, tg.tenant, tg.collection, "conf/definitely-absent", "")
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
		if _, size, _, _, err := tg.client.Head(ctx, tg.bucket, tg.tenant, tg.collection, dst.Key, ""); err != nil {
			t.Fatalf("HEAD the copy: %v", err)
		} else if size != int64(len(body)) {
			t.Errorf("copy size = %d, want %d", size, len(body))
		}
		_ = tg.client.DeleteObject(ctx, tg.bucket, tg.tenant, tg.collection, dst.Key)
	})

	t.Run("required/multipart round trip", func(t *testing.T) {
		mkey := "conf/multi.bin"
		uploadID, err := tg.client.InitiateMultipart(ctx, tg.bucket, tg.tenant, tg.collection, mkey, "application/octet-stream", "", checksum.SHA256)
		if err != nil {
			t.Fatalf("InitiateMultipart: %v", err)
		}
		// One part: S3 allows a final part below the 5 MiB floor, and a
		// single-part upload is the shape the console produces for small
		// files that still went the multipart route.
		part := bytes.Repeat([]byte("x"), 1024)
		binding := multiparth.PartBinding{Number: 1, SizeBytes: int64(len(part)), ChecksumAlgo: checksum.SHA256, ChecksumValue: digest(checksum.SHA256, part)}
		purl, pheaders, _, err := tg.client.PresignPart(ctx, tg.bucket, tg.tenant, uploadID, tg.collection, mkey, binding, ttl())
		if err != nil {
			t.Fatalf("PresignPart: %v", err)
		}
		// The part URL is bound like a PUT: another body is refused.
		if status, _, _, err := send(ctx, http.MethodPut, purl, pheaders, bytes.Repeat([]byte("y"), len(part))); err != nil || ok(status) {
			t.Errorf("a part with another checksum: status %d err %v, want refused", status, err)
		}
		status, msg, hdr, err := send(ctx, http.MethodPut, purl, pheaders, part)
		if err != nil {
			t.Fatalf("PUT part: %v", err)
		}
		if !ok(status) {
			t.Fatalf("part upload: status %d\n%s", status, msg)
		}
		etag := strings.Trim(hdr.Get("ETag"), `"`)
		if etag == "" {
			t.Fatal("part upload returned no ETag — CompleteMultipart needs it")
		}
		if _, _, err := tg.client.CompleteMultipart(ctx, tg.bucket, tg.tenant, uploadID, tg.collection, mkey, checksum.SHA256,
			[]multiparth.PartETag{{PartNumber: 1, ETag: etag, ChecksumValue: binding.ChecksumValue}}); err != nil {
			t.Fatalf("CompleteMultipart: %v", err)
		}
		if _, size, _, _, err := tg.client.Head(ctx, tg.bucket, tg.tenant, tg.collection, mkey, ""); err != nil {
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
		if _, _, _, _, err := tg.client.Head(ctx, tg.bucket, tg.tenant, tg.collection, key, ""); !errors.Is(err, s3adapter.ErrObjectNotFound) {
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
