package s3adapter

import (
	"context"
	"crypto/md5" // #nosec G501 — the fake's ETag digest, not a secret
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
)

// The sizes the fake S3 reports: a HEAD's Content-Length, and the GET body's.
const (
	copySize   = fakeHeadSize
	streamSize = int64(len(fakeBodyText))
)

func replicaLocs(srcBackend, dstBackend string) (objecth.Location, objecth.Location) {
	return objecth.Location{BackendID: srcBackend, Bucket: "sb", TenantID: testTenant, Collection: "c", Key: "k"},
		objecth.Location{BackendID: dstBackend, Bucket: "db", TenantID: testTenant, Collection: "c", Key: "k"}
}

// sawServerSideCopy reports whether f received a CopyObject request.
func sawServerSideCopy(f *fakeS3) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.reqs {
		if r.Header.Get("x-amz-copy-source") != "" {
			return true
		}
	}
	return false
}

// streamedBody is what f received as multipart parts, in order.
func streamedBody(f *fakeS3) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []byte
	for _, r := range f.reqs {
		if r.Method == http.MethodPut && r.Query.Get("uploadId") != "" {
			out = append(out, r.Body...)
		}
	}
	return string(out)
}

// objectDeletes counts the DELETEs f received for an object (not an upload).
func objectDeletes(f *fakeS3) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.reqs {
		if r.Method == http.MethodDelete && r.Key != "" && r.Query.Get("uploadId") == "" {
			n++
		}
	}
	return n
}

// headSize makes f answer an object HEAD with the given size.
func headSize(f *fakeS3, size int64) {
	f.route = func(w http.ResponseWriter, r *http.Request, _, key string) bool {
		if r.Method == http.MethodHead && key != "" {
			w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
			w.WriteHeader(http.StatusOK)
			return true
		}
		return false
	}
}

// getWithETag makes f answer an object GET with the fake body and etag.
func getWithETag(f *fakeS3, etag string) {
	f.route = func(w http.ResponseWriter, r *http.Request, _, key string) bool {
		if r.Method == http.MethodGet && key != "" {
			w.Header().Set("ETag", `"`+etag+`"`)
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, fakeBodyText)
			return true
		}
		return false
	}
}

// chunkedBody, as a case's etag, makes the source answer without a length.
const chunkedBody = "chunked"

// getChunked makes f answer an object GET with the fake body, flushed before
// it is written whole so the response carries no Content-Length.
func getChunked(f *fakeS3) {
	f.route = func(w http.ResponseWriter, r *http.Request, _, key string) bool {
		if r.Method == http.MethodGet && key != "" {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
			_, _ = io.WriteString(w, fakeBodyText)
			return true
		}
		return false
	}
}

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s)) // #nosec G401
	return hex.EncodeToString(sum[:])
}

func TestReplicateSameBackendCopiesServerSide(t *testing.T) {
	f := newFakeS3(t)
	rt := NewObjectRouter(registryWith(map[string]*Client{"primary": newTestClient(t, f.srv.URL)}))
	src, dst := replicaLocs("primary", "primary")

	if err := rt.Replicate(testCtx, src, dst, copySize); err != nil {
		t.Fatalf("Replicate: %v", err)
	}
	if !sawServerSideCopy(f) {
		t.Error("a same-backend replica must be a server-side CopyObject")
	}
	if got := f.requestsFor(http.MethodGet, ""); len(got) != 0 {
		t.Errorf("a server-side copy read the source body %d times", len(got))
	}
	if got := f.requestsFor(http.MethodHead, mustComposeKey(t, testTenant, "c", "k")); len(got) != 1 {
		t.Errorf("the copy was read back %d times, want once", len(got))
	}
}

// A server-side copy whose replica is not the row's size is removed and
// reported as a mismatch.
func TestReplicateSameBackendDiscardsACopyOfTheWrongSize(t *testing.T) {
	f := newFakeS3(t)
	rt := NewObjectRouter(registryWith(map[string]*Client{"primary": newTestClient(t, f.srv.URL)}))
	src, dst := replicaLocs("primary", "primary")

	err := rt.Replicate(testCtx, src, dst, copySize+1)
	if !errors.Is(err, ErrReplicaMismatch) || !IsTransient(err) {
		t.Fatalf("Replicate = %v, want a transient ErrReplicaMismatch", err)
	}
	if objectDeletes(f) != 1 {
		t.Errorf("deletes = %d, want the mismatched replica removed", objectDeletes(f))
	}
}

// One CopyObject refuses an object above 5 GiB, so a larger one streams even
// on one backend.
func TestReplicateSameBackendStreamsAnObjectAboveTheCopyCeiling(t *testing.T) {
	f := newFakeS3(t)
	rt := NewObjectRouter(registryWith(map[string]*Client{"primary": newTestClient(t, f.srv.URL)}))
	src, dst := replicaLocs("primary", "primary")

	// The fake's body is not 5 GiB, so the size check refuses it; the path
	// taken is what this test reads.
	if err := rt.Replicate(testCtx, src, dst, MaxServerSideCopyBytes+1); !errors.Is(err, ErrReplicaMismatch) {
		t.Fatalf("Replicate = %v, want the size mismatch of the streamed path", err)
	}
	if sawServerSideCopy(f) {
		t.Error("an object above the copy ceiling was sent to CopyObject")
	}
	if got := f.requestsFor(http.MethodGet, mustComposeKey(t, testTenant, "c", "k")); len(got) != 1 {
		t.Errorf("source GETs = %d, want 1", len(got))
	}
}

// The ceiling itself still fits one CopyObject.
func TestReplicateSameBackendCopiesAnObjectAtTheCeiling(t *testing.T) {
	f := newFakeS3(t)
	headSize(f, MaxServerSideCopyBytes)
	rt := NewObjectRouter(registryWith(map[string]*Client{"primary": newTestClient(t, f.srv.URL)}))
	src, dst := replicaLocs("primary", "primary")

	if err := rt.Replicate(testCtx, src, dst, MaxServerSideCopyBytes); err != nil {
		t.Fatalf("Replicate: %v", err)
	}
	if !sawServerSideCopy(f) {
		t.Error("an object at the ceiling must still copy server-side")
	}
}

func TestReplicateAcrossBackendsStreams(t *testing.T) {
	fSrc, fDst := newFakeS3(t), newFakeS3(t)
	rt := NewObjectRouter(registryWith(map[string]*Client{
		"src": newTestClient(t, fSrc.srv.URL), "dst": newTestClient(t, fDst.srv.URL),
	}))
	src, dst := replicaLocs("src", "dst")

	if err := rt.Replicate(testCtx, src, dst, streamSize); err != nil {
		t.Fatalf("Replicate: %v", err)
	}
	if sawServerSideCopy(fDst) || sawServerSideCopy(fSrc) {
		t.Error("a cross-backend replica cannot be a server-side copy")
	}
	if got := streamedBody(fDst); got != fakeBodyText {
		t.Errorf("destination received %q, want %q", got, fakeBodyText)
	}
}

// The streamed replica is checked against the row's size and, where the
// source's ETag is a plain MD5, against that digest.
func TestReplicateStreamVerifiesTheReplica(t *testing.T) {
	cases := []struct {
		name         string
		size         int64
		etag         string // "" keeps the fake's GET, which sends none
		wantMismatch bool
		wantUploads  bool // whether the destination saw an upload at all
		wantDeleted  bool
	}{
		{"matching size, no digest", streamSize, "", false, true, false},
		{"matching digest", streamSize, md5Hex(fakeBodyText), false, true, false},
		{"multipart ETag is no digest", streamSize, md5Hex("other") + "-2", false, true, false},
		{"source size differs from the row", streamSize + 1, "", true, false, false},
		{"digest differs", streamSize, md5Hex("other"), true, true, true},
		// No Content-Length to check up front: the bytes written are what
		// catches the short body.
		{"unsized source short of the row", streamSize + 1, chunkedBody, true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fSrc, fDst := newFakeS3(t), newFakeS3(t)
			switch tc.etag {
			case "":
			case chunkedBody:
				getChunked(fSrc)
			default:
				getWithETag(fSrc, tc.etag)
			}
			rt := NewObjectRouter(registryWith(map[string]*Client{
				"src": newTestClient(t, fSrc.srv.URL), "dst": newTestClient(t, fDst.srv.URL),
			}))
			src, dst := replicaLocs("src", "dst")

			err := rt.Replicate(testCtx, src, dst, tc.size)
			if got := errors.Is(err, ErrReplicaMismatch); got != tc.wantMismatch {
				t.Fatalf("Replicate = %v, mismatch %v, want %v", err, got, tc.wantMismatch)
			}
			if tc.wantMismatch && !IsTransient(err) {
				t.Errorf("a mismatch must be transient: %v", err)
			}
			if got := streamedBody(fDst) != ""; got != tc.wantUploads {
				t.Errorf("destination upload = %v, want %v", got, tc.wantUploads)
			}
			if got := objectDeletes(fDst) == 1; got != tc.wantDeleted {
				t.Errorf("replica removed = %v, want %v", got, tc.wantDeleted)
			}
		})
	}
}

// route404OnBucketHead makes f answer HeadBucket with status, so a not-found
// cannot be attributed to the key.
func failBucketHead(f *fakeS3, status int) {
	inner := f.route
	f.route = func(w http.ResponseWriter, r *http.Request, bucket, key string) bool {
		if r.Method == http.MethodHead && key == "" {
			w.WriteHeader(status)
			return true
		}
		return inner != nil && inner(w, r, bucket, key)
	}
}

// failWith answers every request matching method with an S3 error.
func failWith(f *fakeS3, method string, status int, code string) {
	f.route = func(w http.ResponseWriter, r *http.Request, _, key string) bool {
		if r.Method == method && key != "" {
			writeS3Error(w, status, code, "injected")
			return true
		}
		return false
	}
}

// ErrObjectNotFound — the one failure the replication worker may pass over —
// means the source's key is absent from a bucket confirmed reachable. A
// missing bucket, a bucket that cannot be confirmed, and a not-found the
// destination answered are none of them that.
func TestReplicateNamesAMissingSourceOnlyWhenConfirmed(t *testing.T) {
	cases := []struct {
		name        string
		crossBorder bool
		method      string
		status      int
		code        string
		bucketHead  int // 0 leaves HeadBucket answering 200
		wantMissing bool
	}{
		{"stream, no such key", true, http.MethodGet, http.StatusNotFound, "NoSuchKey", 0, true},
		{"stream, no such key, bucket unconfirmed", true, http.MethodGet, http.StatusNotFound, "NoSuchKey", http.StatusForbidden, false},
		{"stream, no such bucket", true, http.MethodGet, http.StatusNotFound, codeNoSuchBucket, 0, false},
		{"stream, access denied", true, http.MethodGet, http.StatusForbidden, "AccessDenied", 0, false},
		{"server-side, no such key", false, http.MethodPut, http.StatusNotFound, "NoSuchKey", 0, true},
		{"server-side, no such key, bucket unconfirmed", false, http.MethodPut, http.StatusNotFound, "NoSuchKey", http.StatusForbidden, false},
		{"server-side, no such bucket", false, http.MethodPut, http.StatusNotFound, codeNoSuchBucket, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fSrc, fDst := newFakeS3(t), newFakeS3(t)
			clients := map[string]*Client{"src": newTestClient(t, fSrc.srv.URL)}
			dstBackend := "src"
			if tc.crossBorder {
				clients["dst"] = newTestClient(t, fDst.srv.URL)
				dstBackend = "dst"
			}
			failWith(fSrc, tc.method, tc.status, tc.code)
			if tc.bucketHead != 0 {
				failBucketHead(fSrc, tc.bucketHead)
			}
			rt := NewObjectRouter(registryWith(clients))
			src, dst := replicaLocs("src", dstBackend)

			err := rt.Replicate(testCtx, src, dst, copySize)
			if err == nil {
				t.Fatal("Replicate succeeded against an injected failure")
			}
			if got := errors.Is(err, ErrObjectNotFound); got != tc.wantMissing {
				t.Errorf("errors.Is(err, ErrObjectNotFound) = %v, want %v (err: %v)", got, tc.wantMissing, err)
			}
		})
	}
}

// GetStream names a missing object only once its bucket is confirmed.
func TestGetStreamQualifiesANotFound(t *testing.T) {
	for _, tc := range []struct {
		name        string
		bucketHead  int
		wantMissing bool
	}{
		{"bucket confirmed", 0, true},
		{"bucket unconfirmed", http.StatusForbidden, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeS3(t)
			failWith(f, http.MethodGet, http.StatusNotFound, "NoSuchKey")
			if tc.bucketHead != 0 {
				failBucketHead(f, tc.bucketHead)
			}
			c := newTestClient(t, f.srv.URL)
			_, _, err := c.GetStream(testCtx, "b", testTenant, "c", "k")
			if got := errors.Is(err, ErrObjectNotFound); got != tc.wantMissing {
				t.Errorf("GetStream = %v, ErrObjectNotFound %v, want %v", err, got, tc.wantMissing)
			}
		})
	}
}

// A not-found from the destination is the destination's, never the source's.
func TestReplicateNamesADestinationNotFound(t *testing.T) {
	fSrc, fDst := newFakeS3(t), newFakeS3(t)
	fDst.route = func(w http.ResponseWriter, r *http.Request, _, _ string) bool {
		if r.Method == http.MethodPost {
			writeS3Error(w, http.StatusNotFound, codeNoSuchBucket, "gone")
			return true
		}
		return false
	}
	rt := NewObjectRouter(registryWith(map[string]*Client{
		"src": newTestClient(t, fSrc.srv.URL), "dst": newTestClient(t, fDst.srv.URL),
	}))
	src, dst := replicaLocs("src", "dst")

	err := rt.Replicate(testCtx, src, dst, streamSize)
	if !errors.Is(err, ErrReplicaDestinationNotFound) {
		t.Errorf("Replicate = %v, want ErrReplicaDestinationNotFound", err)
	}
	if errors.Is(err, ErrObjectNotFound) {
		t.Errorf("a destination not-found was read as the source's: %v", err)
	}
}

// An empty object still completes: CompleteMultipartUpload refuses an upload
// with no parts, so Close sends one empty part.
func TestStreamWriterClosesAnEmptyObjectWithOneEmptyPart(t *testing.T) {
	f := newFakeS3(t)
	c := newTestClient(t, f.srv.URL)
	w, err := c.Open(testCtx, "b", testTenant, "ok", "k", "text/plain", 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, total, _, err := w.Close(); err != nil || total != 0 {
		t.Fatalf("Close = (total %d, %v), want (0, nil)", total, err)
	}
	sizes, _ := uploadedParts(f)
	if want := []int{0}; !slicesEqual(sizes, want) {
		t.Errorf("part sizes = %v, want %v", sizes, want)
	}
}

// codeErr and statusErr carry an S3 error code or an HTTP status the way the
// SDK's errors expose them, so the classification is tested without a server.
type codeErr string

func (e codeErr) Error() string     { return string(e) }
func (e codeErr) ErrorCode() string { return string(e) }

type statusErr int

func (e statusErr) Error() string       { return fmt.Sprintf("status %d", int(e)) }
func (e statusErr) HTTPStatusCode() int { return int(e) }

func TestIsTransient(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"throttled", codeErr("SlowDown"), true},
		{"request timeout", codeErr("RequestTimeout"), true},
		{"internal error status", statusErr(http.StatusInternalServerError), true},
		{"unavailable status", statusErr(http.StatusServiceUnavailable), true},
		{"body cut short", fmt.Errorf("stream copy: transfer: %w", io.ErrUnexpectedEOF), true},
		{"replica mismatch", fmt.Errorf("replica: %w", ErrReplicaMismatch), true},
		{"destination not found", fmt.Errorf("replica: %w", ErrReplicaDestinationNotFound), false},
		{"access denied", codeErr("AccessDenied"), false},
		{"no such bucket", codeErr(codeNoSuchBucket), false},
		{"no such key", fmt.Errorf("%w: %w", ErrObjectNotFound, codeErr("NoSuchKey")), false},
		{"forbidden status", statusErr(http.StatusForbidden), false},
		{"cancelled", context.Canceled, false},
		{"plain error", errors.New("composing the key failed"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsTransient(tc.err); got != tc.want {
				t.Errorf("IsTransient(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
