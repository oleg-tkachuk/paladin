package s3adapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
)

// replicaSize is an object size well under the server-side copy ceiling.
const replicaSize int64 = 1024

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

func TestReplicateSameBackendCopiesServerSide(t *testing.T) {
	f := newFakeS3(t)
	rt := NewObjectRouter(registryWith(map[string]*Client{"primary": newTestClient(t, f.srv.URL)}))
	src, dst := replicaLocs("primary", "primary")

	if err := rt.Replicate(testCtx, src, dst, replicaSize); err != nil {
		t.Fatalf("Replicate: %v", err)
	}
	if !sawServerSideCopy(f) {
		t.Error("a same-backend replica must be a server-side CopyObject")
	}
	if got := f.requestsFor(http.MethodGet, ""); len(got) != 0 {
		t.Errorf("a server-side copy read the source body %d times", len(got))
	}
}

// One CopyObject refuses an object above 5 GiB, so a larger one streams even
// on one backend.
func TestReplicateSameBackendStreamsAnObjectAboveTheCopyCeiling(t *testing.T) {
	f := newFakeS3(t)
	rt := NewObjectRouter(registryWith(map[string]*Client{"primary": newTestClient(t, f.srv.URL)}))
	src, dst := replicaLocs("primary", "primary")

	if err := rt.Replicate(testCtx, src, dst, MaxServerSideCopyBytes+1); err != nil {
		t.Fatalf("Replicate: %v", err)
	}
	if sawServerSideCopy(f) {
		t.Error("an object above the copy ceiling was sent to CopyObject")
	}
	if got := streamedBody(f); got != fakeBodyText {
		t.Errorf("streamed %q, want %q", got, fakeBodyText)
	}
}

// The ceiling itself still fits one CopyObject.
func TestReplicateSameBackendCopiesAnObjectAtTheCeiling(t *testing.T) {
	f := newFakeS3(t)
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

	if err := rt.Replicate(testCtx, src, dst, replicaSize); err != nil {
		t.Fatalf("Replicate: %v", err)
	}
	if sawServerSideCopy(fDst) || sawServerSideCopy(fSrc) {
		t.Error("a cross-backend replica cannot be a server-side copy")
	}
	if got := streamedBody(fDst); got != fakeBodyText {
		t.Errorf("destination received %q, want %q", got, fakeBodyText)
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

// A source with no such key is the one failure the replication worker passes
// over, so it alone answers ErrObjectNotFound — on either path — and a missing
// bucket does not.
func TestReplicateNamesAMissingSource(t *testing.T) {
	cases := []struct {
		name        string
		crossBorder bool
		method      string
		status      int
		code        string
		wantMissing bool
	}{
		{"stream, no such key", true, http.MethodGet, http.StatusNotFound, "NoSuchKey", true},
		{"stream, no such bucket", true, http.MethodGet, http.StatusNotFound, codeNoSuchBucket, false},
		{"stream, access denied", true, http.MethodGet, http.StatusForbidden, "AccessDenied", false},
		{"server-side, no such key", false, http.MethodPut, http.StatusNotFound, "NoSuchKey", true},
		{"server-side, no such bucket", false, http.MethodPut, http.StatusNotFound, codeNoSuchBucket, false},
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
			rt := NewObjectRouter(registryWith(clients))
			src, dst := replicaLocs("src", dstBackend)

			err := rt.Replicate(testCtx, src, dst, replicaSize)
			if err == nil {
				t.Fatal("Replicate succeeded against an injected failure")
			}
			if got := errors.Is(err, ErrObjectNotFound); got != tc.wantMissing {
				t.Errorf("errors.Is(err, ErrObjectNotFound) = %v, want %v (err: %v)", got, tc.wantMissing, err)
			}
		})
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
