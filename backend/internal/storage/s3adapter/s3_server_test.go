package s3adapter

import (
	"bytes"
	"context"
	"crypto/md5" // #nosec G501 — mirrors the adapter's content digest, not a secret
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/multiparth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

// These tests stand a real aws-sdk-go-v2 S3 client up against an in-process
// S3-compatible HTTP endpoint (httptest). That covers the parts of the adapter
// a pure-fake cannot reach — request construction, XML/header decoding, the
// multipart stream writer, and the routers' delegation path — without a
// container, real credentials, or any off-box network. router_test.go noted
// that the cross-backend stream-through "needs a real/mock S3 on both ends";
// newFakeS3 is that mock.

// ─── fake S3 endpoint ──────────────────────────────────────────────────────

type s3Req struct {
	Method string
	Bucket string
	Key    string
	Query  url.Values
	Header http.Header
	Body   []byte
}

type fakeS3 struct {
	srv *httptest.Server

	mu   sync.Mutex
	reqs []s3Req

	// route, when set, runs before the default dispatcher. Returning true
	// means the request was fully handled (used to inject S3 error replies).
	route func(w http.ResponseWriter, r *http.Request, bucket, key string) bool
}

func newFakeS3(t *testing.T) *fakeS3 {
	t.Helper()
	f := &fakeS3{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bucket, key := splitPathStyle(r.URL.Path)

		f.mu.Lock()
		f.reqs = append(f.reqs, s3Req{
			Method: r.Method, Bucket: bucket, Key: key,
			Query: r.URL.Query(), Header: r.Header.Clone(), Body: body,
		})
		f.mu.Unlock()

		if f.route != nil && f.route(w, r, bucket, key) {
			return
		}
		f.dispatch(w, r, bucket, key, body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// splitPathStyle turns "/bucket/a/b/c" into ("bucket", "a/b/c"). The adapter
// always builds path-style requests in these tests (ForcePathStyle).
func splitPathStyle(p string) (bucket, key string) {
	parts := strings.SplitN(strings.TrimPrefix(p, "/"), "/", 2)
	bucket = parts[0]
	if len(parts) == 2 {
		key = parts[1]
	}
	return bucket, key
}

const (
	fakeHeadETag     = "head-etag"
	fakeHeadSize     = int64(1234)
	fakeUploadID     = "upload-abc"
	fakePartETag     = "part-etag"
	fakeCompleteETag = "final-etag"
	fakeCopyETag     = "copy-etag"
	fakeBodyText     = "hello-stream-body"
)

func (f *fakeS3) dispatch(w http.ResponseWriter, r *http.Request, bucket, key string, _ []byte) {
	q := r.URL.Query()
	_, hasUploads := q["uploads"]
	uploadID := q.Get("uploadId")

	switch {
	// ── bucket-scoped ──
	case r.Method == http.MethodGet && bucket == "":
		writeXML(w, http.StatusOK,
			`<ListAllMyBucketsResult><Buckets><Bucket><Name>b1</Name></Bucket></Buckets></ListAllMyBucketsResult>`)
	case r.Method == http.MethodPut && key == "" && q.Has("tagging"):
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodPut && key == "":
		w.WriteHeader(http.StatusOK) // CreateBucket
	case r.Method == http.MethodHead && key == "":
		w.WriteHeader(http.StatusOK) // HeadBucket
	case r.Method == http.MethodDelete && key == "":
		w.WriteHeader(http.StatusNoContent) // DeleteBucket

	// ── multipart (must precede the generic object cases) ──
	case r.Method == http.MethodPost && hasUploads:
		writeXML(w, http.StatusOK, fmt.Sprintf(
			`<InitiateMultipartUploadResult><UploadId>%s</UploadId></InitiateMultipartUploadResult>`, fakeUploadID))
	case r.Method == http.MethodPost && uploadID != "":
		writeXML(w, http.StatusOK, fmt.Sprintf(
			`<CompleteMultipartUploadResult><ETag>&quot;%s&quot;</ETag></CompleteMultipartUploadResult>`, fakeCompleteETag))
	case r.Method == http.MethodPut && uploadID != "":
		w.Header().Set("ETag", `"`+fakePartETag+`"`)
		w.WriteHeader(http.StatusOK) // UploadPart
	case r.Method == http.MethodDelete && uploadID != "":
		w.WriteHeader(http.StatusNoContent) // AbortMultipartUpload

	// ── object-scoped ──
	case r.Method == http.MethodHead && key != "":
		w.Header().Set("ETag", `"`+fakeHeadETag+`"`)
		w.Header().Set("Content-Length", fmt.Sprint(fakeHeadSize))
		w.Header().Set("x-amz-checksum-sha256", "sha256-chk")
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodPut && r.Header.Get("x-amz-copy-source") != "":
		writeXML(w, http.StatusOK, fmt.Sprintf(
			`<CopyObjectResult><ETag>&quot;%s&quot;</ETag></CopyObjectResult>`, fakeCopyETag))
	case r.Method == http.MethodGet && key != "":
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(fakeBodyText))
	case r.Method == http.MethodDelete && key != "":
		w.WriteHeader(http.StatusNoContent)

	default:
		writeS3Error(w, http.StatusBadRequest, "BadRequest", "unhandled in fake")
	}
}

func writeXML(w http.ResponseWriter, code int, body string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(code)
	_, _ = io.WriteString(w, xml(body))
}

func writeS3Error(w http.ResponseWriter, code int, s3code, msg string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(code)
	_, _ = io.WriteString(w, xml(fmt.Sprintf(
		`<Error><Code>%s</Code><Message>%s</Message><RequestId>req-1</RequestId></Error>`, s3code, msg)))
}

func xml(body string) string { return `<?xml version="1.0" encoding="UTF-8"?>` + body }

// requestsFor returns every recorded request matching method (and, when
// non-empty, key), so assertions can prove what the adapter actually sent.
func (f *fakeS3) requestsFor(method, key string) []s3Req {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []s3Req
	for _, r := range f.reqs {
		if r.Method != method {
			continue
		}
		if key != "" && r.Key != key {
			continue
		}
		out = append(out, r)
	}
	return out
}

// ─── client construction ───────────────────────────────────────────────────

// hermeticAWS points the SDK's credential/config discovery at empty paths and
// kills IMDS, so a developer's real ~/.aws never leaks into a test run.
func hermeticAWS(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "absent-config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "absent-creds"))
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_REGION", "us-east-1")
}

func newTestClient(t *testing.T, endpoint string, mut ...func(*config.StorageBackend)) *Client {
	t.Helper()
	hermeticAWS(t)
	b := config.StorageBackend{
		Bucket:         "default-bkt",
		Region:         "us-east-1",
		Endpoint:       endpoint,
		ForcePathStyle: true,
		Auth: config.StorageBackendAuth{
			Mode:      config.AuthModeStaticKeys,
			AccessKey: "AKIAEXAMPLE",
			SecretKey: "secret-example",
		},
	}
	for _, m := range mut {
		m(&b)
	}
	c, err := New(context.Background(), b)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// registryWith seeds the registry cache directly so routers resolve to the
// given clients without going through build/AWS at all.
func registryWith(clients map[string]*Client) *BackendRegistry {
	cfg := config.Storage{Backends: map[string]config.StorageBackend{}}
	for id := range clients {
		cfg.Backends[id] = config.StorageBackend{}
	}
	reg := NewBackendRegistry(cfg)
	for id, c := range clients {
		reg.clients[id] = c
	}
	return reg
}

var (
	testTenant = uuid.MustParse("11111111-2222-3333-4444-555555555555")
	testCtx    = context.Background()
)

// ─── bucket-scoped operations ──────────────────────────────────────────────

func TestClientProbe(t *testing.T) {
	f := newFakeS3(t)
	c := newTestClient(t, f.srv.URL)

	if err := c.Probe(testCtx); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if got := f.requestsFor(http.MethodGet, ""); len(got) == 0 {
		t.Fatal("Probe should issue a ListBuckets GET")
	}
}

func TestClientProbeSurfacesBackendError(t *testing.T) {
	f := newFakeS3(t)
	f.route = func(w http.ResponseWriter, _ *http.Request, _, _ string) bool {
		writeS3Error(w, http.StatusForbidden, "AccessDenied", "bad creds")
		return true
	}
	c := newTestClient(t, f.srv.URL)

	if err := c.Probe(testCtx); err == nil {
		t.Fatal("Probe against a rejecting backend: want error")
	}
}

func TestClientCreateBucket(t *testing.T) {
	f := newFakeS3(t)
	c := newTestClient(t, f.srv.URL)

	if err := c.CreateBucket(testCtx, "primary", "new-bkt", "eu-central-1"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	puts := f.requestsFor(http.MethodPut, "")
	if len(puts) != 1 || puts[0].Bucket != "new-bkt" {
		t.Fatalf("want one PUT to new-bkt, got %+v", puts)
	}
	// A non-us-east-1 region must travel as a LocationConstraint.
	if !bytes.Contains(puts[0].Body, []byte("eu-central-1")) {
		t.Errorf("region constraint missing from body: %q", puts[0].Body)
	}
	// Success is only reported after the HeadBucket reachability check.
	if len(f.requestsFor(http.MethodHead, "")) != 1 {
		t.Error("CreateBucket must verify reachability with HeadBucket")
	}
}

func TestClientCreateBucketOmitsUSEast1Constraint(t *testing.T) {
	f := newFakeS3(t)
	c := newTestClient(t, f.srv.URL)

	if err := c.CreateBucket(testCtx, "primary", "b", "us-east-1"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	puts := f.requestsFor(http.MethodPut, "")
	if bytes.Contains(puts[0].Body, []byte("LocationConstraint")) {
		t.Errorf("us-east-1 must not send a LocationConstraint, got %q", puts[0].Body)
	}
}

// An "already owned" CreateBucket is the idempotent re-run of a reconciler
// pass and must not surface as an error.
func TestClientCreateBucketIdempotent(t *testing.T) {
	for _, code := range []string{"BucketAlreadyOwnedByYou", "BucketAlreadyExists"} {
		t.Run(code, func(t *testing.T) {
			f := newFakeS3(t)
			f.route = func(w http.ResponseWriter, r *http.Request, _, key string) bool {
				if r.Method == http.MethodPut && key == "" {
					writeS3Error(w, http.StatusConflict, code, "exists")
					return true
				}
				return false
			}
			c := newTestClient(t, f.srv.URL)

			if err := c.CreateBucket(testCtx, "primary", "b", ""); err != nil {
				t.Fatalf("%s must be tolerated, got %v", code, err)
			}
			if len(f.requestsFor(http.MethodHead, "")) != 1 {
				t.Error("still must confirm reachability after tolerating the conflict")
			}
		})
	}
}

func TestClientCreateBucketOtherErrorFails(t *testing.T) {
	f := newFakeS3(t)
	f.route = func(w http.ResponseWriter, r *http.Request, _, key string) bool {
		if r.Method == http.MethodPut && key == "" {
			writeS3Error(w, http.StatusForbidden, "AccessDenied", "nope")
			return true
		}
		return false
	}
	c := newTestClient(t, f.srv.URL)

	if err := c.CreateBucket(testCtx, "primary", "b", ""); err == nil {
		t.Fatal("AccessDenied on create: want error")
	}
}

// The reachability gate is the point of the HeadBucket call: a backend that
// accepts CreateBucket but cannot serve the bucket must keep the row failed.
func TestClientCreateBucketUnreachableFails(t *testing.T) {
	f := newFakeS3(t)
	f.route = func(w http.ResponseWriter, r *http.Request, _, key string) bool {
		if r.Method == http.MethodHead && key == "" {
			writeS3Error(w, http.StatusNotFound, "NoSuchBucket", "gone")
			return true
		}
		return false
	}
	c := newTestClient(t, f.srv.URL)

	err := c.CreateBucket(testCtx, "primary", "b", "")
	if err == nil {
		t.Fatal("created-but-unreachable: want error")
	}
	if !strings.Contains(err.Error(), "not reachable") {
		t.Errorf("error should name the reachability failure, got %v", err)
	}
}

func TestClientDeleteBucket(t *testing.T) {
	f := newFakeS3(t)
	c := newTestClient(t, f.srv.URL)

	if err := c.DeleteBucket(testCtx, "primary", "doomed"); err != nil {
		t.Fatalf("DeleteBucket: %v", err)
	}
	dels := f.requestsFor(http.MethodDelete, "")
	if len(dels) != 1 || dels[0].Bucket != "doomed" {
		t.Fatalf("want one DELETE of doomed, got %+v", dels)
	}
}

func TestClientDeleteBucketError(t *testing.T) {
	f := newFakeS3(t)
	f.route = func(w http.ResponseWriter, r *http.Request, _, _ string) bool {
		if r.Method == http.MethodDelete {
			writeS3Error(w, http.StatusConflict, "BucketNotEmpty", "not empty")
			return true
		}
		return false
	}
	c := newTestClient(t, f.srv.URL)

	if err := c.DeleteBucket(testCtx, "primary", "full"); err == nil {
		t.Fatal("non-empty bucket delete: want error")
	}
}

// Deleting a bucket the backend never had is done, not failed: a bucket
// deleted while still pending provisioning used to sit in 'deleting' while the
// reconciler retried the 404 without end.
func TestClientDeleteBucketAlreadyGone(t *testing.T) {
	f := newFakeS3(t)
	f.route = func(w http.ResponseWriter, r *http.Request, _, _ string) bool {
		if r.Method == http.MethodDelete {
			writeS3Error(w, http.StatusNotFound, codeNoSuchBucket, "gone")
			return true
		}
		return false
	}
	c := newTestClient(t, f.srv.URL)

	if err := c.DeleteBucket(testCtx, "primary", "never-made"); err != nil {
		t.Fatalf("NoSuchBucket on delete: want success, got %v", err)
	}
}

// A 404 that does not say NoSuchBucket proves nothing about the bucket — a
// wrong endpoint answers the same way — so it stays an error.
func TestClientDeleteBucketBare404Fails(t *testing.T) {
	f := newFakeS3(t)
	f.route = func(w http.ResponseWriter, r *http.Request, _, _ string) bool {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNotFound)
			return true
		}
		return false
	}
	c := newTestClient(t, f.srv.URL)

	if err := c.DeleteBucket(testCtx, "primary", "somewhere"); err == nil {
		t.Fatal("bare 404 on delete: want error")
	}
}

func TestClientTagBucketOwner(t *testing.T) {
	f := newFakeS3(t)
	c := newTestClient(t, f.srv.URL)

	if err := c.TagBucketOwner(testCtx, "primary", "bkt", testTenant); err != nil {
		t.Fatalf("TagBucketOwner: %v", err)
	}
	puts := f.requestsFor(http.MethodPut, "")
	if len(puts) != 1 {
		t.Fatalf("want one tagging PUT, got %d", len(puts))
	}
	if !puts[0].Query.Has("tagging") {
		t.Error("tagging PUT must carry the ?tagging subresource")
	}
	// Cost attribution depends on the tenant id actually reaching the store.
	if !bytes.Contains(puts[0].Body, []byte(testTenant.String())) {
		t.Errorf("tenant id missing from tag body: %q", puts[0].Body)
	}
	if !bytes.Contains(puts[0].Body, []byte("tenant_id")) {
		t.Errorf("tag key missing from body: %q", puts[0].Body)
	}
}

func TestClientTagBucketOwnerError(t *testing.T) {
	f := newFakeS3(t)
	f.route = func(w http.ResponseWriter, r *http.Request, _, _ string) bool {
		if r.Method == http.MethodPut {
			writeS3Error(w, http.StatusNotImplemented, "NotImplemented", "no tagging")
			return true
		}
		return false
	}
	c := newTestClient(t, f.srv.URL)

	if err := c.TagBucketOwner(testCtx, "primary", "bkt", testTenant); err == nil {
		t.Fatal("backend without PutBucketTagging: want error (caller decides it is non-fatal)")
	}
}

// ─── object-scoped operations ──────────────────────────────────────────────

func TestClientHead(t *testing.T) {
	f := newFakeS3(t)
	c := newTestClient(t, f.srv.URL)

	etag, size, checksum, seq, err := c.Head(testCtx, "bkt", testTenant, "ok", "k1")
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if etag != fakeHeadETag {
		t.Errorf("ETag must be unquoted: got %q want %q", etag, fakeHeadETag)
	}
	if size != fakeHeadSize {
		t.Errorf("size = %d, want %d", size, fakeHeadSize)
	}
	if checksum != "sha256-chk" {
		t.Errorf("checksum = %q, want the SHA256 value", checksum)
	}
	if seq != "" {
		t.Errorf("HEAD carries no sequencer, got %q", seq)
	}
	// The tenant-scoped key layout is the adapter's core contract.
	heads := f.requestsFor(http.MethodHead, "")
	if len(heads) != 1 {
		t.Fatalf("want one HEAD, got %d", len(heads))
	}
	wantKey := composeKey(testTenant, "ok", "k1")
	if heads[0].Key != wantKey {
		t.Errorf("key = %q, want %q", heads[0].Key, wantKey)
	}
	if heads[0].Bucket != "bkt" {
		t.Errorf("per-call bucket must win over the configured default, got %q", heads[0].Bucket)
	}
}

// Checksum selection is a documented precedence chain, so each rung needs its
// own case — a single SHA256 test would not catch a broken fallback.
func TestClientHeadChecksumPrecedence(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{"sha256 wins", map[string]string{
			"x-amz-checksum-sha256": "s", "x-amz-checksum-crc32c": "c", "x-amz-checksum-crc32": "z"}, "s"},
		{"crc32c when no sha256", map[string]string{
			"x-amz-checksum-crc32c": "c", "x-amz-checksum-crc32": "z"}, "c"},
		{"crc32 last", map[string]string{"x-amz-checksum-crc32": "z"}, "z"},
		{"none", map[string]string{}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeS3(t)
			f.route = func(w http.ResponseWriter, r *http.Request, _, key string) bool {
				if r.Method != http.MethodHead || key == "" {
					return false
				}
				w.Header().Set("ETag", `"e"`)
				w.Header().Set("Content-Length", "1")
				for k, v := range tc.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(http.StatusOK)
				return true
			}
			c := newTestClient(t, f.srv.URL)

			_, _, checksum, _, err := c.Head(testCtx, "b", testTenant, "ok", "k")
			if err != nil {
				t.Fatalf("Head: %v", err)
			}
			if checksum != tc.want {
				t.Errorf("checksum = %q, want %q", checksum, tc.want)
			}
		})
	}
}

func TestClientHeadError(t *testing.T) {
	f := newFakeS3(t)
	f.route = func(w http.ResponseWriter, r *http.Request, _, key string) bool {
		if r.Method == http.MethodHead && key != "" {
			writeS3Error(w, http.StatusNotFound, "NoSuchKey", "missing")
			return true
		}
		return false
	}
	c := newTestClient(t, f.srv.URL)

	if _, _, _, _, err := c.Head(testCtx, "b", testTenant, "ok", "k"); err == nil {
		t.Fatal("HEAD of a missing object: want error")
	}
}

// Falling back to the configured bucket when the per-call one is empty is what
// lets single-bucket deployments omit it everywhere.
func TestClientHeadFallsBackToConfiguredBucket(t *testing.T) {
	f := newFakeS3(t)
	c := newTestClient(t, f.srv.URL)

	if _, _, _, _, err := c.Head(testCtx, "", testTenant, "ok", "k"); err != nil {
		t.Fatalf("Head: %v", err)
	}
	if got := f.requestsFor(http.MethodHead, "")[0].Bucket; got != "default-bkt" {
		t.Errorf("bucket = %q, want the configured default", got)
	}
}

func TestClientCopyObject(t *testing.T) {
	f := newFakeS3(t)
	c := newTestClient(t, f.srv.URL)

	src := objecth.Location{Bucket: "src-b", TenantID: testTenant, Collection: "ok1", Key: "k1"}
	dst := objecth.Location{Bucket: "dst-b", TenantID: testTenant, Collection: "ok2", Key: "k2"}
	if err := c.CopyObject(testCtx, src, dst); err != nil {
		t.Fatalf("CopyObject: %v", err)
	}

	puts := f.requestsFor(http.MethodPut, "")
	var copyReq *s3Req
	for i := range f.reqs {
		if f.reqs[i].Header.Get("x-amz-copy-source") != "" {
			copyReq = &f.reqs[i]
		}
	}
	_ = puts
	if copyReq == nil {
		t.Fatal("CopyObject must send x-amz-copy-source")
	}
	// A wrong copy-source silently copies the wrong bytes, so pin it exactly.
	wantSrc := "src-b/" + composeKey(testTenant, "ok1", "k1")
	if got := copyReq.Header.Get("x-amz-copy-source"); got != wantSrc {
		t.Errorf("copy-source = %q, want %q", got, wantSrc)
	}
	if copyReq.Bucket != "dst-b" {
		t.Errorf("destination bucket = %q, want dst-b", copyReq.Bucket)
	}
	if want := composeKey(testTenant, "ok2", "k2"); copyReq.Key != want {
		t.Errorf("destination key = %q, want %q", copyReq.Key, want)
	}
}

func TestClientCopyObjectError(t *testing.T) {
	f := newFakeS3(t)
	f.route = func(w http.ResponseWriter, r *http.Request, _, _ string) bool {
		if r.Method == http.MethodPut {
			writeS3Error(w, http.StatusNotFound, "NoSuchKey", "missing source")
			return true
		}
		return false
	}
	c := newTestClient(t, f.srv.URL)

	err := c.CopyObject(testCtx,
		objecth.Location{Bucket: "a", TenantID: testTenant, Collection: "o", Key: "k"},
		objecth.Location{Bucket: "b", TenantID: testTenant, Collection: "o", Key: "k"})
	if err == nil {
		t.Fatal("copy of a missing source: want error")
	}
}

func TestClientGetStream(t *testing.T) {
	f := newFakeS3(t)
	c := newTestClient(t, f.srv.URL)

	rc, contentType, err := c.GetStream(testCtx, "b", testTenant, "ok", "k")
	if err != nil {
		t.Fatalf("GetStream: %v", err)
	}
	defer func() { _ = rc.Close() }()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(got) != fakeBodyText {
		t.Errorf("body = %q, want %q", got, fakeBodyText)
	}
	if contentType != "text/plain" {
		t.Errorf("contentType = %q, want text/plain", contentType)
	}
}

func TestClientGetStreamError(t *testing.T) {
	f := newFakeS3(t)
	f.route = func(w http.ResponseWriter, r *http.Request, _, key string) bool {
		if r.Method == http.MethodGet && key != "" {
			writeS3Error(w, http.StatusNotFound, "NoSuchKey", "missing")
			return true
		}
		return false
	}
	c := newTestClient(t, f.srv.URL)

	if _, _, err := c.GetStream(testCtx, "b", testTenant, "ok", "k"); err == nil {
		t.Fatal("GetStream of a missing object: want error")
	}
}

func TestClientDeleteObject(t *testing.T) {
	f := newFakeS3(t)
	c := newTestClient(t, f.srv.URL)

	if err := c.DeleteObject(testCtx, "b", testTenant, "ok", "k"); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	dels := f.requestsFor(http.MethodDelete, composeKey(testTenant, "ok", "k"))
	if len(dels) != 1 {
		t.Fatalf("want one keyed DELETE, got %d", len(dels))
	}
}

func TestClientDeleteObjectError(t *testing.T) {
	f := newFakeS3(t)
	f.route = func(w http.ResponseWriter, r *http.Request, _, _ string) bool {
		if r.Method == http.MethodDelete {
			writeS3Error(w, http.StatusForbidden, "AccessDenied", "no")
			return true
		}
		return false
	}
	c := newTestClient(t, f.srv.URL)

	if err := c.DeleteObject(testCtx, "b", testTenant, "ok", "k"); err == nil {
		t.Fatal("DeleteObject rejection: want error")
	}
}

// ─── multipart ─────────────────────────────────────────────────────────────

func TestClientInitiateMultipart(t *testing.T) {
	f := newFakeS3(t)
	c := newTestClient(t, f.srv.URL)

	id, err := c.InitiateMultipart(testCtx, "b", testTenant, "ok", "k", "application/pdf")
	if err != nil {
		t.Fatalf("InitiateMultipart: %v", err)
	}
	if id != fakeUploadID {
		t.Errorf("uploadID = %q, want %q", id, fakeUploadID)
	}
	posts := f.requestsFor(http.MethodPost, composeKey(testTenant, "ok", "k"))
	if len(posts) != 1 {
		t.Fatalf("want one initiate POST, got %d", len(posts))
	}
	if got := posts[0].Header.Get("Content-Type"); got != "application/pdf" {
		t.Errorf("content type = %q, want application/pdf", got)
	}
}

func TestClientInitiateMultipartError(t *testing.T) {
	f := newFakeS3(t)
	f.route = func(w http.ResponseWriter, r *http.Request, _, _ string) bool {
		if r.Method == http.MethodPost {
			writeS3Error(w, http.StatusForbidden, "AccessDenied", "no")
			return true
		}
		return false
	}
	c := newTestClient(t, f.srv.URL)

	if _, err := c.InitiateMultipart(testCtx, "b", testTenant, "ok", "k", ""); err == nil {
		t.Fatal("initiate rejection: want error")
	}
}

func TestClientCompleteMultipart(t *testing.T) {
	f := newFakeS3(t)
	c := newTestClient(t, f.srv.URL)

	parts := []multiparth.PartETag{{PartNumber: 1, ETag: "e1"}, {PartNumber: 2, ETag: "e2"}}
	etag, size, err := c.CompleteMultipart(testCtx, "b", testTenant, fakeUploadID, "ok", "k", parts)
	if err != nil {
		t.Fatalf("CompleteMultipart: %v", err)
	}
	if etag != fakeCompleteETag {
		t.Errorf("etag = %q, want unquoted %q", etag, fakeCompleteETag)
	}
	// Size comes from the follow-up HEAD, not from the complete response.
	if size != fakeHeadSize {
		t.Errorf("size = %d, want %d (from the HEAD after complete)", size, fakeHeadSize)
	}
	posts := f.requestsFor(http.MethodPost, composeKey(testTenant, "ok", "k"))
	if len(posts) != 1 {
		t.Fatalf("want one complete POST, got %d", len(posts))
	}
	// Part list ordering/content is what S3 validates the upload against.
	for _, want := range []string{"e1", "e2", "<PartNumber>1</PartNumber>", "<PartNumber>2</PartNumber>"} {
		if !bytes.Contains(posts[0].Body, []byte(want)) {
			t.Errorf("complete body missing %q: %s", want, posts[0].Body)
		}
	}
}

func TestClientCompleteMultipartError(t *testing.T) {
	f := newFakeS3(t)
	f.route = func(w http.ResponseWriter, r *http.Request, _, _ string) bool {
		if r.Method == http.MethodPost {
			writeS3Error(w, http.StatusBadRequest, "InvalidPart", "bad part")
			return true
		}
		return false
	}
	c := newTestClient(t, f.srv.URL)

	if _, _, err := c.CompleteMultipart(testCtx, "b", testTenant, "u", "ok", "k", nil); err == nil {
		t.Fatal("complete with a bad part: want error")
	}
}

// A complete that succeeds but whose HEAD fails still returns the etag, with
// size 0 and an error — callers need the etag to reconcile.
func TestClientCompleteMultipartHeadFailure(t *testing.T) {
	f := newFakeS3(t)
	f.route = func(w http.ResponseWriter, r *http.Request, _, key string) bool {
		if r.Method == http.MethodHead && key != "" {
			writeS3Error(w, http.StatusNotFound, "NoSuchKey", "gone")
			return true
		}
		return false
	}
	c := newTestClient(t, f.srv.URL)

	etag, size, err := c.CompleteMultipart(testCtx, "b", testTenant, "u", "ok", "k", nil)
	if err == nil {
		t.Fatal("head-after-complete failure: want error")
	}
	if etag != fakeCompleteETag {
		t.Errorf("etag should still be returned, got %q", etag)
	}
	if size != 0 {
		t.Errorf("size = %d, want 0 when the HEAD failed", size)
	}
}

func TestClientAbortMultipart(t *testing.T) {
	f := newFakeS3(t)
	c := newTestClient(t, f.srv.URL)

	if err := c.AbortMultipart(testCtx, "b", testTenant, fakeUploadID, "ok", "k"); err != nil {
		t.Fatalf("AbortMultipart: %v", err)
	}
	dels := f.requestsFor(http.MethodDelete, composeKey(testTenant, "ok", "k"))
	if len(dels) != 1 {
		t.Fatalf("want one abort DELETE, got %d", len(dels))
	}
	if got := dels[0].Query.Get("uploadId"); got != fakeUploadID {
		t.Errorf("uploadId = %q, want %q", got, fakeUploadID)
	}
}

func TestClientAbortMultipartError(t *testing.T) {
	f := newFakeS3(t)
	f.route = func(w http.ResponseWriter, r *http.Request, _, _ string) bool {
		if r.Method == http.MethodDelete {
			writeS3Error(w, http.StatusNotFound, "NoSuchUpload", "gone")
			return true
		}
		return false
	}
	c := newTestClient(t, f.srv.URL)

	if err := c.AbortMultipart(testCtx, "b", testTenant, "u", "ok", "k"); err == nil {
		t.Fatal("abort of an unknown upload: want error")
	}
}

// ─── stream writer ─────────────────────────────────────────────────────────

func TestClientOpenAndStreamWrite(t *testing.T) {
	f := newFakeS3(t)
	// A tiny part size forces the multi-part path on a small payload.
	c := newTestClient(t, f.srv.URL, func(b *config.StorageBackend) { b.PartSizeBytes = 8 })

	w, err := c.Open(testCtx, "b", testTenant, "ok", "k", "text/plain", 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// 18 bytes at partSize=8 ⇒ two full 8-byte parts plus a 2-byte trailer,
	// regardless of how the caller chunked its Writes.
	chunks := [][]byte{[]byte("0123456789"), []byte("abcdefXY")}
	var payload []byte
	for _, chunk := range chunks {
		n, err := w.Write(chunk)
		if err != nil {
			t.Fatalf("Write(%q): %v", chunk, err)
		}
		if n != len(chunk) {
			t.Fatalf("Write returned %d, want %d", n, len(chunk))
		}
		payload = append(payload, chunk...)
	}

	etag, total, checksum, err := w.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if etag != fakeCompleteETag {
		t.Errorf("etag = %q, want %q", etag, fakeCompleteETag)
	}
	if total != int64(len(payload)) {
		t.Errorf("total = %d, want %d", total, len(payload))
	}
	// The reported digest must be the md5 of everything written.
	sum := md5.Sum(payload) // #nosec G401
	if want := hex.EncodeToString(sum[:]); checksum != want {
		t.Errorf("checksum = %q, want %q", checksum, want)
	}

	// Bytes must reach the store intact and in order across parts.
	sizes, assembled := uploadedParts(f)
	if want := []int{8, 8, 2}; !slicesEqual(sizes, want) {
		t.Errorf("part sizes = %v, want %v (partSize=8, trailer last)", sizes, want)
	}
	if !bytes.Equal(assembled, payload) {
		t.Errorf("reassembled parts = %q, want %q", assembled, payload)
	}
}

// A caller that hands over one big buffer must still get partSize-sized parts:
// folding it into a single part would break past S3's 5 GiB per-part ceiling.
func TestStreamWriterSplitsOneLargeWrite(t *testing.T) {
	f := newFakeS3(t)
	c := newTestClient(t, f.srv.URL, func(b *config.StorageBackend) { b.PartSizeBytes = 4 })

	w, err := c.Open(testCtx, "b", testTenant, "ok", "k", "", 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	payload := []byte("abcdefghijklmno") // 15 bytes at partSize=4 ⇒ 4+4+4+3
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("Write: %v", err)
	}
	_, total, checksum, err := w.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	sizes, assembled := uploadedParts(f)
	if want := []int{4, 4, 4, 3}; !slicesEqual(sizes, want) {
		t.Errorf("part sizes = %v, want %v", sizes, want)
	}
	if !bytes.Equal(assembled, payload) {
		t.Errorf("reassembled = %q, want %q", assembled, payload)
	}
	if total != int64(len(payload)) {
		t.Errorf("total = %d, want %d", total, len(payload))
	}
	// Splitting must not disturb the rolling digest.
	sum := md5.Sum(payload) // #nosec G401
	if want := hex.EncodeToString(sum[:]); checksum != want {
		t.Errorf("checksum = %q, want %q", checksum, want)
	}
}

// An exact multiple of partSize must not emit a trailing empty part.
func TestStreamWriterExactMultipleOfPartSize(t *testing.T) {
	f := newFakeS3(t)
	c := newTestClient(t, f.srv.URL, func(b *config.StorageBackend) { b.PartSizeBytes = 4 })

	w, err := c.Open(testCtx, "b", testTenant, "ok", "k", "", 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := w.Write([]byte("12345678")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, _, _, err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	sizes, _ := uploadedParts(f)
	if want := []int{4, 4}; !slicesEqual(sizes, want) {
		t.Errorf("part sizes = %v, want %v (no empty trailer)", sizes, want)
	}
}

// uploadedParts returns the byte length of each UploadPart body, in request
// order, alongside the concatenation of all of them.
func uploadedParts(f *fakeS3) (sizes []int, assembled []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.reqs {
		if r.Method == http.MethodPut && r.Query.Get("uploadId") != "" {
			sizes = append(sizes, len(r.Body))
			assembled = append(assembled, r.Body...)
		}
	}
	return sizes, assembled
}

func slicesEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestStreamWriterCloseIsIdempotentlyGuarded(t *testing.T) {
	f := newFakeS3(t)
	c := newTestClient(t, f.srv.URL)

	w, err := c.Open(testCtx, "b", testTenant, "ok", "k", "", 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, _, _, err := w.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if _, _, _, err := w.Close(); err == nil {
		t.Fatal("second Close must be rejected")
	}
}

// Abort after a successful Close is a no-op — the upload is already gone, and
// issuing a real abort would error against the store.
func TestStreamWriterAbortAfterCloseIsNoop(t *testing.T) {
	f := newFakeS3(t)
	c := newTestClient(t, f.srv.URL)

	w, err := c.Open(testCtx, "b", testTenant, "ok", "k", "", 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, _, _, err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	before := len(f.requestsFor(http.MethodDelete, ""))
	if err := w.Abort(); err != nil {
		t.Fatalf("Abort after Close should be a no-op, got %v", err)
	}
	if after := len(f.requestsFor(http.MethodDelete, "")); after != before {
		t.Error("Abort after Close must not issue an AbortMultipartUpload")
	}
}

func TestStreamWriterAbort(t *testing.T) {
	f := newFakeS3(t)
	c := newTestClient(t, f.srv.URL)

	w, err := c.Open(testCtx, "b", testTenant, "ok", "k", "", 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := w.Write([]byte("partial")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Abort(); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if len(f.requestsFor(http.MethodDelete, composeKey(testTenant, "ok", "k"))) != 1 {
		t.Error("Abort must issue AbortMultipartUpload")
	}
	// Second abort is a no-op.
	if err := w.Abort(); err != nil {
		t.Fatalf("second Abort: %v", err)
	}
}

func TestClientOpenError(t *testing.T) {
	f := newFakeS3(t)
	f.route = func(w http.ResponseWriter, r *http.Request, _, _ string) bool {
		if r.Method == http.MethodPost {
			writeS3Error(w, http.StatusForbidden, "AccessDenied", "no")
			return true
		}
		return false
	}
	c := newTestClient(t, f.srv.URL)

	if _, err := c.Open(testCtx, "b", testTenant, "ok", "k", "", 0); err == nil {
		t.Fatal("Open against a rejecting backend: want error")
	}
}

// A failing UploadPart must surface through Write and leave the caller able to
// abort — silently dropping bytes would corrupt the object.
func TestStreamWriterUploadPartFailure(t *testing.T) {
	f := newFakeS3(t)
	f.route = func(w http.ResponseWriter, r *http.Request, _, _ string) bool {
		if r.Method == http.MethodPut && r.URL.Query().Get("uploadId") != "" {
			writeS3Error(w, http.StatusInternalServerError, "InternalError", "boom")
			return true
		}
		return false
	}
	c := newTestClient(t, f.srv.URL, func(b *config.StorageBackend) { b.PartSizeBytes = 4 })

	w, err := c.Open(testCtx, "b", testTenant, "ok", "k", "", 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := w.Write([]byte("12345678")); err == nil {
		t.Fatal("Write past partSize with a failing UploadPart: want error")
	}
}

// ─── presigning ────────────────────────────────────────────────────────────

func TestClientPresignPutGetPost(t *testing.T) {
	f := newFakeS3(t)
	c := newTestClient(t, f.srv.URL)
	ttl := 15 * time.Minute
	wantKey := composeKey(testTenant, "ok", "k")

	t.Run("put", func(t *testing.T) {
		url, hdrs, exp, err := c.PresignPut(testCtx, objecth.PresignPutArgs{
			TenantID: testTenant, Bucket: "b", Collection: "ok", Key: "k",
			ContentType: "text/plain", TTL: ttl,
		})
		if err != nil {
			t.Fatalf("PresignPut: %v", err)
		}
		assertPresigned(t, url, wantKey, "b")
		if hdrs == nil {
			t.Error("signed headers must be returned")
		}
		assertExpiry(t, exp, ttl)
	})

	t.Run("get", func(t *testing.T) {
		url, _, exp, err := c.PresignGet(testCtx, objecth.PresignGetArgs{
			TenantID: testTenant, Bucket: "b", Collection: "ok", Key: "k",
			TTL: ttl, ContentDisposition: `attachment; filename="r.pdf"`,
		})
		if err != nil {
			t.Fatalf("PresignGet: %v", err)
		}
		assertPresigned(t, url, wantKey, "b")
		// The disposition must ride along or the browser ignores it.
		if !strings.Contains(url, "response-content-disposition") {
			t.Errorf("disposition missing from presigned URL: %s", url)
		}
		assertExpiry(t, exp, ttl)
	})

	t.Run("post falls back to a signed put", func(t *testing.T) {
		url, fields, _, err := c.PresignPost(testCtx, objecth.PresignPostArgs{
			TenantID: testTenant, Bucket: "b", Collection: "ok", Key: "k",
			ContentType: "text/plain", MaxSizeBytes: 4096, TTL: ttl,
		})
		if err != nil {
			t.Fatalf("PresignPost: %v", err)
		}
		assertPresigned(t, url, wantKey, "b")
		if fields["X-Amz-Signed-Fallback"] != "put" {
			t.Errorf("callers must be told this is the PUT fallback, got %v", fields)
		}
		if fields["Content-Length-Range"] != "0,4096" {
			t.Errorf("Content-Length-Range = %q, want 0,4096", fields["Content-Length-Range"])
		}
		if fields["Content-Type"] != "text/plain" {
			t.Errorf("Content-Type field = %q", fields["Content-Type"])
		}
	})
	_ = f
}

func TestPresignPart(t *testing.T) {
	f := newFakeS3(t)
	c := newTestClient(t, f.srv.URL)

	url, _, exp, err := c.PresignPart(testCtx, "b", testTenant, fakeUploadID, "ok", "k", 3, time.Minute)
	if err != nil {
		t.Fatalf("PresignPart: %v", err)
	}
	assertPresigned(t, url, composeKey(testTenant, "ok", "k"), "b")
	// Part number and upload id are what bind the URL to one slot of one upload.
	if !strings.Contains(url, "partNumber=3") {
		t.Errorf("partNumber missing: %s", url)
	}
	if !strings.Contains(url, "uploadId="+fakeUploadID) {
		t.Errorf("uploadId missing: %s", url)
	}
	assertExpiry(t, exp, time.Minute)
}

// The whole point of the two-client split: signed URLs must embed the
// browser-reachable host, never the in-cluster one.
func TestPresignUsesPublicEndpoint(t *testing.T) {
	f := newFakeS3(t)
	c := newTestClient(t, f.srv.URL, func(b *config.StorageBackend) {
		b.PublicEndpoint = "https://s3.public.example.com"
	})

	url, _, _, err := c.PresignGet(testCtx, objecth.PresignGetArgs{
		TenantID: testTenant, Bucket: "b", Collection: "ok", Key: "k", TTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	if !strings.HasPrefix(url, "https://s3.public.example.com/") {
		t.Errorf("presigned URL must use the public endpoint, got %s", url)
	}
	if strings.Contains(url, f.srv.URL) {
		t.Error("presigned URL leaked the internal endpoint")
	}
}

func assertPresigned(t *testing.T, url, wantKey, wantBucket string) {
	t.Helper()
	if !strings.Contains(url, "X-Amz-Signature=") {
		t.Errorf("URL is not v4-signed: %s", url)
	}
	if !strings.Contains(url, wantBucket+"/"+wantKey) {
		t.Errorf("URL %s does not address %s/%s", url, wantBucket, wantKey)
	}
}

func assertExpiry(t *testing.T, got time.Time, ttl time.Duration) {
	t.Helper()
	want := time.Now().Add(ttl)
	if d := got.Sub(want); d > 5*time.Second || d < -5*time.Second {
		t.Errorf("expiry %v is not ~now+%v (off by %v)", got, ttl, d)
	}
}

// ─── routers: delegation to the resolved client ────────────────────────────

func TestObjectRouterDelegatesToResolvedBackend(t *testing.T) {
	fPrimary, fSecondary := newFakeS3(t), newFakeS3(t)
	reg := registryWith(map[string]*Client{
		"primary":   newTestClient(t, fPrimary.srv.URL),
		"secondary": newTestClient(t, fSecondary.srv.URL),
	})
	rt := NewObjectRouter(reg)

	// Routing on the id carried by the call is the router's entire job, so
	// assert the *other* backend stayed untouched.
	if _, _, _, _, err := rt.Head(testCtx, "secondary", "b", testTenant, "ok", "k"); err != nil {
		t.Fatalf("Head: %v", err)
	}
	if len(fSecondary.requestsFor(http.MethodHead, "")) != 1 {
		t.Error("HEAD should have gone to secondary")
	}
	if len(fPrimary.reqs) != 0 {
		t.Error("primary must not see a call routed to secondary")
	}

	if err := rt.DeleteObject(testCtx, "primary", "b", testTenant, "ok", "k"); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	if len(fPrimary.requestsFor(http.MethodDelete, "")) != 1 {
		t.Error("DELETE should have gone to primary")
	}

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"PresignPut", func() error {
			_, _, _, err := rt.PresignPut(testCtx, objecth.PresignPutArgs{
				BackendID: "primary", TenantID: testTenant, Bucket: "b", Collection: "ok", Key: "k", TTL: time.Minute})
			return err
		}},
		{"PresignPost", func() error {
			_, _, _, err := rt.PresignPost(testCtx, objecth.PresignPostArgs{
				BackendID: "primary", TenantID: testTenant, Bucket: "b", Collection: "ok", Key: "k", TTL: time.Minute})
			return err
		}},
		{"PresignGet", func() error {
			_, _, _, err := rt.PresignGet(testCtx, objecth.PresignGetArgs{
				BackendID: "primary", TenantID: testTenant, Bucket: "b", Collection: "ok", Key: "k", TTL: time.Minute})
			return err
		}},
	} {
		if err := tc.call(); err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}

func TestObjectRouterSameBackendCopyStaysServerSide(t *testing.T) {
	f := newFakeS3(t)
	reg := registryWith(map[string]*Client{"primary": newTestClient(t, f.srv.URL)})
	rt := NewObjectRouter(reg)

	src := objecth.Location{BackendID: "primary", Bucket: "b", TenantID: testTenant, Collection: "o1", Key: "k1"}
	dst := objecth.Location{BackendID: "primary", Bucket: "b", TenantID: testTenant, Collection: "o2", Key: "k2"}
	if err := rt.CopyObject(testCtx, src, dst); err != nil {
		t.Fatalf("CopyObject: %v", err)
	}
	// Same backend ⇒ one server-side CopyObject, no bytes through this process.
	if len(f.requestsFor(http.MethodGet, "")) != 0 {
		t.Error("same-backend copy must not GET the source body")
	}
	var sawCopy bool
	for _, r := range f.reqs {
		if r.Header.Get("x-amz-copy-source") != "" {
			sawCopy = true
		}
	}
	if !sawCopy {
		t.Error("expected a server-side CopyObject")
	}
}

// Cross-backend copy is the ADR-0015 Phase 3 stream-through: GET on the source
// piped into the destination's multipart writer.
func TestObjectRouterCrossBackendStreamsThrough(t *testing.T) {
	fSrc, fDst := newFakeS3(t), newFakeS3(t)
	reg := registryWith(map[string]*Client{
		"src": newTestClient(t, fSrc.srv.URL),
		"dst": newTestClient(t, fDst.srv.URL),
	})
	rt := NewObjectRouter(reg)

	src := objecth.Location{BackendID: "src", Bucket: "sb", TenantID: testTenant, Collection: "o1", Key: "k1"}
	dst := objecth.Location{BackendID: "dst", Bucket: "db", TenantID: testTenant, Collection: "o2", Key: "k2"}
	if err := rt.CopyObject(testCtx, src, dst); err != nil {
		t.Fatalf("cross-backend CopyObject: %v", err)
	}

	if len(fSrc.requestsFor(http.MethodGet, composeKey(testTenant, "o1", "k1"))) != 1 {
		t.Error("source backend should have been read once")
	}
	// The destination must receive the source bytes verbatim.
	var assembled []byte
	for _, r := range fDst.reqs {
		if r.Method == http.MethodPut && r.Query.Get("uploadId") != "" {
			assembled = append(assembled, r.Body...)
		}
	}
	if string(assembled) != fakeBodyText {
		t.Errorf("destination received %q, want %q", assembled, fakeBodyText)
	}
	if len(fDst.requestsFor(http.MethodPost, composeKey(testTenant, "o2", "k2"))) < 2 {
		t.Error("destination should see both an initiate and a complete POST")
	}
}

func TestObjectRouterStreamThroughErrors(t *testing.T) {
	fSrc, fDst := newFakeS3(t), newFakeS3(t)
	good := map[string]*Client{
		"src": newTestClient(t, fSrc.srv.URL),
		"dst": newTestClient(t, fDst.srv.URL),
	}
	src := objecth.Location{BackendID: "src", Bucket: "sb", TenantID: testTenant, Collection: "o", Key: "k"}
	dst := objecth.Location{BackendID: "dst", Bucket: "db", TenantID: testTenant, Collection: "o", Key: "k"}

	t.Run("unknown source backend", func(t *testing.T) {
		rt := NewObjectRouter(registryWith(map[string]*Client{"dst": good["dst"]}))
		err := rt.CopyObject(testCtx, src, dst)
		if err == nil || !strings.Contains(err.Error(), "source backend") {
			t.Fatalf("want a source-backend error, got %v", err)
		}
	})

	t.Run("unknown destination backend", func(t *testing.T) {
		rt := NewObjectRouter(registryWith(map[string]*Client{"src": good["src"]}))
		err := rt.CopyObject(testCtx, src, dst)
		if err == nil || !strings.Contains(err.Error(), "dest backend") {
			t.Fatalf("want a dest-backend error, got %v", err)
		}
	})

	t.Run("source read fails", func(t *testing.T) {
		f1, f2 := newFakeS3(t), newFakeS3(t)
		f1.route = func(w http.ResponseWriter, r *http.Request, _, key string) bool {
			if r.Method == http.MethodGet && key != "" {
				writeS3Error(w, http.StatusNotFound, "NoSuchKey", "missing")
				return true
			}
			return false
		}
		rt := NewObjectRouter(registryWith(map[string]*Client{
			"src": newTestClient(t, f1.srv.URL), "dst": newTestClient(t, f2.srv.URL),
		}))
		err := rt.CopyObject(testCtx, src, dst)
		if err == nil || !strings.Contains(err.Error(), "open source") {
			t.Fatalf("want an open-source error, got %v", err)
		}
	})

	t.Run("destination open fails", func(t *testing.T) {
		f1, f2 := newFakeS3(t), newFakeS3(t)
		f2.route = func(w http.ResponseWriter, r *http.Request, _, _ string) bool {
			if r.Method == http.MethodPost {
				writeS3Error(w, http.StatusForbidden, "AccessDenied", "no")
				return true
			}
			return false
		}
		rt := NewObjectRouter(registryWith(map[string]*Client{
			"src": newTestClient(t, f1.srv.URL), "dst": newTestClient(t, f2.srv.URL),
		}))
		err := rt.CopyObject(testCtx, src, dst)
		if err == nil || !strings.Contains(err.Error(), "open dest") {
			t.Fatalf("want an open-dest error, got %v", err)
		}
	})
}

func TestPresignRouterDelegates(t *testing.T) {
	f := newFakeS3(t)
	reg := registryWith(map[string]*Client{"primary": newTestClient(t, f.srv.URL)})
	rt := NewPresignRouter(reg)
	wantKey := composeKey(testTenant, "ok", "k")

	url, _, _, err := rt.PresignGet(testCtx, "primary", "b", testTenant, "ok", "k", time.Minute, "")
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	assertPresigned(t, url, wantKey, "b")

	url, _, _, err = rt.PresignPut(testCtx, "primary", "b", testTenant, "ok", "k", "text/plain", "", time.Minute, 0)
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}
	assertPresigned(t, url, wantKey, "b")
}

func TestPresignRouterUnknownBackend(t *testing.T) {
	rt := NewPresignRouter(registryWith(map[string]*Client{}))

	if _, _, _, err := rt.PresignGet(testCtx, "nope", "b", testTenant, "ok", "k", time.Minute, ""); err == nil {
		t.Error("PresignGet to unknown backend: want error")
	}
	if _, _, _, err := rt.PresignPut(testCtx, "nope", "b", testTenant, "ok", "k", "", "", time.Minute, 0); err == nil {
		t.Error("PresignPut to unknown backend: want error")
	}
}

func TestMultipartRouterDelegates(t *testing.T) {
	f := newFakeS3(t)
	reg := registryWith(map[string]*Client{"primary": newTestClient(t, f.srv.URL)})
	rt := NewMultipartRouter(reg)

	id, err := rt.InitiateMultipart(testCtx, "primary", "b", testTenant, "ok", "k", "text/plain")
	if err != nil {
		t.Fatalf("InitiateMultipart: %v", err)
	}
	if id != fakeUploadID {
		t.Errorf("uploadID = %q, want %q", id, fakeUploadID)
	}

	etag, size, err := rt.CompleteMultipart(testCtx, "primary", "b", testTenant, id, "ok", "k",
		[]multiparth.PartETag{{PartNumber: 1, ETag: "e1"}})
	if err != nil {
		t.Fatalf("CompleteMultipart: %v", err)
	}
	if etag != fakeCompleteETag || size != fakeHeadSize {
		t.Errorf("complete returned (%q,%d), want (%q,%d)", etag, size, fakeCompleteETag, fakeHeadSize)
	}

	if _, _, _, err := rt.PresignPart(testCtx, "primary", "b", testTenant, id, "ok", "k", 1, time.Minute); err != nil {
		t.Fatalf("PresignPart: %v", err)
	}
	if err := rt.AbortMultipart(testCtx, "primary", "b", testTenant, id, "ok", "k"); err != nil {
		t.Fatalf("AbortMultipart: %v", err)
	}
}

func TestMultipartRouterUnknownBackend(t *testing.T) {
	rt := NewMultipartRouter(registryWith(map[string]*Client{}))

	if _, err := rt.InitiateMultipart(testCtx, "nope", "b", testTenant, "ok", "k", ""); err == nil {
		t.Error("InitiateMultipart to unknown backend: want error")
	}
	if _, _, err := rt.CompleteMultipart(testCtx, "nope", "b", testTenant, "u", "ok", "k", nil); err == nil {
		t.Error("CompleteMultipart to unknown backend: want error")
	}
	if err := rt.AbortMultipart(testCtx, "nope", "b", testTenant, "u", "ok", "k"); err == nil {
		t.Error("AbortMultipart to unknown backend: want error")
	}
	if _, _, _, err := rt.PresignPart(testCtx, "nope", "b", testTenant, "u", "ok", "k", 1, time.Minute); err == nil {
		t.Error("PresignPart to unknown backend: want error")
	}
}

func TestStreamRouterDelegates(t *testing.T) {
	f := newFakeS3(t)
	reg := registryWith(map[string]*Client{"primary": newTestClient(t, f.srv.URL)})
	rt := NewStreamRouter(reg)

	w, err := rt.Open(testCtx, "primary", "b", testTenant, "ok", "k", "text/plain", 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := w.Write([]byte("abc")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, total, _, err := w.Close(); err != nil || total != 3 {
		t.Fatalf("Close: total=%d err=%v", total, err)
	}
}

func TestStreamRouterUnknownBackend(t *testing.T) {
	rt := NewStreamRouter(registryWith(map[string]*Client{}))

	if _, err := rt.Open(testCtx, "nope", "b", testTenant, "ok", "k", "", 0); err == nil {
		t.Error("Open to unknown backend: want error")
	}
}

func TestProvisionerRouterDelegates(t *testing.T) {
	f := newFakeS3(t)
	reg := registryWith(map[string]*Client{"primary": newTestClient(t, f.srv.URL)})
	rt := NewProvisionerRouter(reg)

	if err := rt.CreateBucket(testCtx, "primary", "b1", "us-east-1"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if err := rt.TagBucketOwner(testCtx, "primary", "b1", testTenant); err != nil {
		t.Fatalf("TagBucketOwner: %v", err)
	}
	if err := rt.DeleteBucket(testCtx, "primary", "b1"); err != nil {
		t.Fatalf("DeleteBucket: %v", err)
	}
}

func TestProvisionerRouterTagUnknownBackend(t *testing.T) {
	rt := NewProvisionerRouter(registryWith(map[string]*Client{}))

	if err := rt.TagBucketOwner(testCtx, "nope", "b", testTenant); err == nil {
		t.Error("TagBucketOwner to unknown backend: want error")
	}
}

// ─── construction ──────────────────────────────────────────────────────────

func TestNewSetsCompletionModeFromEvents(t *testing.T) {
	f := newFakeS3(t)

	explicit := newTestClient(t, f.srv.URL)
	if got := explicit.CompletionMode("any"); got != objecth.CompletionModeExplicit {
		t.Errorf("events disabled ⇒ Explicit, got %v", got)
	}

	implicit := newTestClient(t, f.srv.URL, func(b *config.StorageBackend) { b.Events.Enabled = true })
	if got := implicit.CompletionMode("any"); got != objecth.CompletionModeImplicit {
		t.Errorf("events enabled ⇒ Implicit, got %v", got)
	}
}

func TestNewCarriesSSEConfig(t *testing.T) {
	f := newFakeS3(t)
	c := newTestClient(t, f.srv.URL, func(b *config.StorageBackend) {
		b.SSE.Type = "aws:kms"
		b.SSE.KeyID = "key-1"
	})
	if c.sseType != "aws:kms" || c.sseKey != "key-1" {
		t.Errorf("SSE config not carried onto the client: %q/%q", c.sseType, c.sseKey)
	}
}

// Every credential mode must be constructible offline: the providers resolve
// lazily, so a boot that only builds clients must never reach STS/IMDS. A
// regression here turns a misconfigured backend into a hang at startup.
func TestNewCredentialModes(t *testing.T) {
	f := newFakeS3(t)
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("jwt-token"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	base := func(auth config.StorageBackendAuth) config.StorageBackend {
		return config.StorageBackend{
			Bucket: "b", Region: "us-east-1", Endpoint: f.srv.URL,
			ForcePathStyle: true, Auth: auth,
		}
	}

	t.Run("assume_role", func(t *testing.T) {
		hermeticAWS(t)
		if _, err := New(testCtx, base(config.StorageBackendAuth{
			Mode: config.AuthModeAssumeRole, RoleARN: "arn:aws:iam::1:role/r",
			SessionName: "sess", ExternalID: "ext", DurationSeconds: 900,
		})); err != nil {
			t.Fatalf("New(assume_role): %v", err)
		}
	})

	t.Run("assume_role with defaulted session name", func(t *testing.T) {
		hermeticAWS(t)
		if _, err := New(testCtx, base(config.StorageBackendAuth{
			Mode: config.AuthModeAssumeRole, RoleARN: "arn:aws:iam::1:role/r",
		})); err != nil {
			t.Fatalf("New(assume_role, defaults): %v", err)
		}
	})

	t.Run("web_identity from explicit file", func(t *testing.T) {
		hermeticAWS(t)
		if _, err := New(testCtx, base(config.StorageBackendAuth{
			Mode: config.AuthModeWebIdentity, RoleARN: "arn:aws:iam::1:role/r",
			WebIdentityTokenFile: tokenFile, SessionName: "sess", DurationSeconds: 900,
		})); err != nil {
			t.Fatalf("New(web_identity): %v", err)
		}
	})

	// EKS IRSA injects the token path via env rather than config.
	t.Run("web_identity from IRSA env", func(t *testing.T) {
		hermeticAWS(t)
		// The pod identity webhook injects both of these together; the SDK's
		// own bootstrap chain reads them before our provider is built.
		t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", tokenFile)
		t.Setenv("AWS_ROLE_ARN", "arn:aws:iam::1:role/r")
		if _, err := New(testCtx, base(config.StorageBackendAuth{
			Mode: config.AuthModeWebIdentity, RoleARN: "arn:aws:iam::1:role/r",
		})); err != nil {
			t.Fatalf("New(web_identity via env): %v", err)
		}
	})

	t.Run("web_identity without a token file fails", func(t *testing.T) {
		hermeticAWS(t)
		t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", "")
		_, err := New(testCtx, base(config.StorageBackendAuth{
			Mode: config.AuthModeWebIdentity, RoleARN: "arn:aws:iam::1:role/r",
		}))
		if err == nil {
			t.Fatal("web_identity with no token file: want error")
		}
		if !strings.Contains(err.Error(), "web_identity_token_file") {
			t.Errorf("error should name the missing setting, got %v", err)
		}
	})

	// A missing or bogus mode must fail loudly at build time rather than
	// silently falling back to some ambient credential source.
	t.Run("empty mode fails", func(t *testing.T) {
		hermeticAWS(t)
		if _, err := New(testCtx, base(config.StorageBackendAuth{})); err == nil {
			t.Fatal("empty auth.mode: want error")
		}
	})

	t.Run("unknown mode fails", func(t *testing.T) {
		hermeticAWS(t)
		_, err := New(testCtx, base(config.StorageBackendAuth{Mode: "telepathy"}))
		if err == nil {
			t.Fatal("unknown auth.mode: want error")
		}
		if !strings.Contains(err.Error(), "telepathy") {
			t.Errorf("error should echo the bad mode, got %v", err)
		}
	})
}

// default_chain must build without static keys present; it resolves lazily, so
// construction alone must not dial anything.
func TestNewDefaultChainBuilds(t *testing.T) {
	f := newFakeS3(t)
	hermeticAWS(t)
	c, err := New(testCtx, config.StorageBackend{
		Bucket: "b", Region: "us-east-1", Endpoint: f.srv.URL, ForcePathStyle: true,
		Auth: config.StorageBackendAuth{Mode: config.AuthModeDefaultChain},
	})
	if err != nil {
		t.Fatalf("New(default_chain): %v", err)
	}
	if c == nil {
		t.Fatal("New returned a nil client")
	}
}
