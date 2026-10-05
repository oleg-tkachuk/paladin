package paladintest_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

func read(t *testing.T, r *paladin.ObjectReader, err error) []byte {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestUploadAndDownloadThroughTheFake(t *testing.T) {
	cases := []struct {
		name      string
		size      int
		threshold int64
	}{
		{"one PUT", 1 << 10, 0},
		{"multipart", 2*paladintest.PartSize + 3, paladintest.PartSize},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := paladintest.New(t)
			p := srv.Connect()
			body := bytes.Repeat([]byte("paladin "), tc.size/len("paladin ")+1)[:tc.size]
			obj, err := paladin.Upload(context.Background(), p.Data, paladin.UploadInput{
				Parent: srv.Collection().String(), Key: "docs/a.pdf", ContentType: "application/pdf",
				Size: int64(len(body)), Body: bytes.NewReader(body),
			}, paladin.UploadOptions{MultipartThreshold: tc.threshold})
			if err != nil {
				t.Fatal(err)
			}
			if stored, ok := srv.Content(obj.GetName()); !ok || !bytes.Equal(stored, body) {
				t.Fatal("the fake does not hold what was uploaded")
			}
			r, err := paladin.Download(context.Background(), p.Data, obj.GetName(), paladin.DownloadOptions{})
			if got := read(t, r, err); !bytes.Equal(got, body) {
				t.Errorf("downloaded %d bytes, want %d", len(got), len(body))
			}
			uri := paladin.ObjectURI{Collection: srv.Collection(), Key: "docs/a.pdf"}.String()
			r, err = paladin.DownloadURI(context.Background(), p.Data, uri, paladin.DownloadOptions{Offset: 1, Length: 3})
			if got := read(t, r, err); !bytes.Equal(got, body[1:4]) {
				t.Errorf("range by URI = %q, want %q", got, body[1:4])
			}
		})
	}
}

func TestTheFakeRecordsTheChecksumDownloadVerifies(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	body := []byte("verified")
	obj, err := paladin.Upload(context.Background(), p.Data, paladin.UploadInput{
		Parent: srv.Collection().String(), Key: "k", Size: int64(len(body)), Body: bytes.NewReader(body),
	}, paladin.UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if obj.GetChecksum().GetAlgorithm() != paladin.ChecksumSHA256 || obj.GetChecksum().GetValue() == "" {
		t.Errorf("checksum = %v, want the SHA-256 the upload completed with", obj.GetChecksum())
	}
}

func TestPutListAndDelete(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	for _, key := range []string{"c", "a", "b"} {
		srv.Put(srv.Collection(), key, "text/plain", []byte(key))
	}
	srv.Put(srv.Collection("other"), "z", "text/plain", []byte("z"))
	var keys []string
	for o, err := range paladin.Pages(context.Background(), p.Data.Object.ListObjects,
		&datav1.ListObjectsRequest{Parent: srv.Collection().String()}, (*datav1.ListObjectsResponse).GetObjects) {
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, o.GetKey())
	}
	if len(keys) != 3 || keys[0] != "a" || keys[2] != "c" {
		t.Errorf("listed %v, want [a b c] from the one collection", keys)
	}
	obj, err := paladin.LookupObject(context.Background(), p.Data, paladin.ObjectURI{Collection: srv.Collection(), Key: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Data.Object.DeleteObject(context.Background(), connect.NewRequest(&datav1.DeleteObjectRequest{Name: obj.GetName()})); err != nil {
		t.Fatal(err)
	}
	_, err = p.Data.Object.GetObject(context.Background(), connect.NewRequest(&datav1.GetObjectRequest{Name: obj.GetName()}))
	if !errors.Is(err, paladin.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound after the delete", err)
	}
}

func TestEverythingElseIsUnimplemented(t *testing.T) {
	p := paladintest.New(t).Connect()
	_, err := p.Admin.Tenant.ListTenants(context.Background(), connect.NewRequest(&adminv1.ListTenantsRequest{}))
	if !errors.Is(err, paladin.ErrContractSkew) {
		t.Errorf("err = %v, want Unimplemented, which the SDK reads as a contract skew", err)
	}
}

func TestTheFakeRefusesWhatTheServerRefuses(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	// A collection under the tenant's slug, not its id.
	_, err := p.Data.Object.UploadObject(context.Background(), connect.NewRequest(&datav1.UploadObjectRequest{
		Parent: "tenants/acme/collections/c", Key: "k",
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("err = %v, want InvalidArgument", err)
	}
	// An upload with no checksum to bind its URL to.
	_, err = p.Data.Object.UploadObject(context.Background(), connect.NewRequest(&datav1.UploadObjectRequest{
		Parent: srv.Collection().String(), Key: "k",
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("no checksum: err = %v, want InvalidArgument", err)
	}
	// A completion whose ETag is not the content's.
	up, err := p.Data.Object.UploadObject(context.Background(), connect.NewRequest(&datav1.UploadObjectRequest{
		Parent: srv.Collection().String(), Key: "k", ChecksumValue: emptySHA256,
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Data.Object.CompleteObject(context.Background(), connect.NewRequest(&datav1.CompleteObjectRequest{Name: up.Msg.GetObject().GetName(), Etag: "x"}))
	if !errors.Is(err, paladin.ErrFailedPrecondition) {
		t.Errorf("err = %v, want FailedPrecondition", err)
	}
}

func TestCompleteMatchesTheServer(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	ctx := context.Background()
	body := []byte("no etag")
	sum, _ := paladin.Checksum(paladin.ChecksumSHA256, bytes.NewReader(body))
	up, err := p.Data.Object.UploadObject(ctx, connect.NewRequest(&datav1.UploadObjectRequest{
		Parent: srv.Collection().String(), Key: "k", SizeHintBytes: int64(len(body)), ChecksumValue: sum,
	}))
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, up.Msg.GetUploadUrl().GetUrl(), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range up.Msg.GetUploadUrl().GetRequiredHeaders() {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	name := up.Msg.GetObject().GetName()

	// The ETag is optional, as on the server.
	obj, err := p.Data.Object.CompleteObject(ctx, connect.NewRequest(&datav1.CompleteObjectRequest{Name: name}))
	if err != nil {
		t.Fatalf("complete without an ETag: %v", err)
	}
	if obj.Msg.GetState() != datav1.ObjectState_OBJECT_STATE_AVAILABLE {
		t.Errorf("state = %v, want AVAILABLE", obj.Msg.GetState())
	}
	// Completing it again returns it, whatever the ETag.
	again, err := p.Data.Object.CompleteObject(ctx, connect.NewRequest(&datav1.CompleteObjectRequest{Name: name, Etag: "stale"}))
	if err != nil || again.Msg.GetEtag() != obj.Msg.GetEtag() {
		t.Errorf("second complete: %v, etag %q; want the object as completed", err, again.Msg.GetEtag())
	}
	if got, _ := srv.Content(name); !bytes.Equal(got, body) {
		t.Errorf("content = %q, want %q", got, body)
	}
}

func TestEnsureTenantStorage(t *testing.T) {
	const (
		backend = "seaweedfs"
		bucket  = "paladin-shared"
	)
	p := paladintest.New(t).Connect()
	ensure := func(collections ...string) (*datav1.EnsureTenantStorageResponse, error) {
		resp, err := p.Data.StorageBootstrap.EnsureTenantStorage(context.Background(), connect.NewRequest(
			&datav1.EnsureTenantStorageRequest{BackendId: backend, Bucket: bucket, Collections: collections}))
		if err != nil {
			return nil, err
		}
		return resp.Msg, nil
	}
	first, err := ensure("a", "b")
	if err != nil {
		t.Fatal(err)
	}
	if !first.GetBucketCreated() || !slices.Equal(first.GetCollectionsCreated(), []string{"a", "b"}) || len(first.GetCollectionsExisting()) != 0 {
		t.Errorf("first ensure = %v, want the bucket and both collections created", first)
	}
	second, err := ensure("b", "c")
	if err != nil {
		t.Fatal(err)
	}
	if second.GetBucketCreated() || !slices.Equal(second.GetCollectionsCreated(), []string{"c"}) || !slices.Equal(second.GetCollectionsExisting(), []string{"b"}) {
		t.Errorf("second ensure = %v, want only c created", second)
	}
	_, err = p.Data.StorageBootstrap.EnsureTenantStorage(context.Background(), connect.NewRequest(
		&datav1.EnsureTenantStorageRequest{BackendId: backend, Bucket: "ab"}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a two-character bucket: err = %v, want InvalidArgument", err)
	}
}

func TestRequestsShowWhatTheClientSent(t *testing.T) {
	const token = "test-token"
	srv := paladintest.New(t)
	p := srv.Connect(paladin.WithBearerToken(token))
	if _, err := p.Data.Object.UploadObject(context.Background(), connect.NewRequest(&datav1.UploadObjectRequest{
		Parent: srv.Collection().String(), Key: "k", ChecksumValue: emptySHA256,
	})); err != nil {
		t.Fatal(err)
	}
	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	r := reqs[0]
	if r.Procedure != paladindatav1connect.ObjectServiceUploadObjectProcedure {
		t.Errorf("procedure = %q", r.Procedure)
	}
	if got := r.Header.Get(paladin.HeaderAuthorization); got != "Bearer "+token {
		t.Errorf("Authorization = %q", got)
	}
	if r.Header.Get(paladin.HeaderIdempotencyKey) == "" {
		t.Error("no idempotency key recorded")
	}
	if !strings.HasPrefix(r.Header.Get(paladin.HeaderUserAgent), "paladin-sdk-go/") {
		t.Errorf("User-Agent = %q", r.Header.Get(paladin.HeaderUserAgent))
	}
}

// emptySHA256 is the SHA-256 of an empty body, a checksum_value for uploads
// whose content the test does not send.
const emptySHA256 = "47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="

// The fake's storage holds a URL to its binding, as the object store does:
// another body, a missing signed header, or an overwrite is refused.
func TestTheFakeStorageEnforcesTheBinding(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	ctx := context.Background()
	body := []byte("bound body")
	sum, _ := paladin.Checksum(paladin.ChecksumSHA256, bytes.NewReader(body))
	up, err := p.Data.Object.UploadObject(ctx, connect.NewRequest(&datav1.UploadObjectRequest{
		Parent: srv.Collection().String(), Key: "k", ContentType: "text/plain", SizeHintBytes: int64(len(body)), ChecksumValue: sum,
	}))
	if err != nil {
		t.Fatal(err)
	}
	url := up.Msg.GetUploadUrl()
	put := func(b []byte, drop string) int {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPut, url.GetUrl(), bytes.NewReader(b))
		for k, v := range url.GetRequiredHeaders() {
			if k != drop {
				req.Header.Set(k, v)
			}
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if got := put([]byte("other body"), ""); got != http.StatusBadRequest {
		t.Errorf("another body of the same length: %d, want 400", got)
	}
	if got := put(body, "X-Amz-Checksum-Sha256"); got != http.StatusBadRequest {
		t.Errorf("checksum header dropped: %d, want 400", got)
	}
	if got := put(body, ""); got != http.StatusOK {
		t.Fatalf("the bound body: %d, want 200", got)
	}
	if got := put(body, ""); got != http.StatusPreconditionFailed {
		t.Errorf("a second PUT: %d, want 412", got)
	}
}

// A download URL bound to the object's ETag carries If-Match, and the fake's
// storage refuses a stale one with 412, as S3 does.
func TestTheFakeHonoursIfMatch(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	ctx := context.Background()
	obj := srv.Put(srv.Collection(), "k", "text/plain", []byte("x"))
	resp, err := p.Data.Object.DownloadObject(ctx, connect.NewRequest(&datav1.DownloadObjectRequest{Name: obj.GetName(), RequireEtagMatch: true}))
	if err != nil {
		t.Fatal(err)
	}
	url := resp.Msg.GetDownloadUrl()
	if url.GetRequiredHeaders()["If-Match"] == "" {
		t.Fatal("no If-Match among the required headers")
	}
	get := func(ifMatch string) int {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url.GetUrl(), nil)
		req.Header.Set("If-Match", ifMatch)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = r.Body.Close()
		return r.StatusCode
	}
	if got := get(url.GetRequiredHeaders()["If-Match"]); got != http.StatusOK {
		t.Errorf("the object's ETag: %d, want 200", got)
	}
	if got := get(`"stale"`); got != http.StatusPreconditionFailed {
		t.Errorf("a stale ETag: %d, want 412", got)
	}
}

// A key holds one object, as the server's unique path does: in any state,
// the trash included, until a permanent delete frees it.
func TestTheFakeHoldsOneObjectPerKey(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	ctx := context.Background()
	upload := func() (*datav1.Object, error) {
		return paladin.Upload(ctx, p.Data, paladin.UploadInput{
			Parent: srv.Collection().String(), Key: "k", ContentType: "text/plain",
			Size: 1, Body: bytes.NewReader([]byte("x")), Metadata: map[string]string{"m": "v"},
		}, paladin.UploadOptions{})
	}
	obj, err := upload()
	if err != nil {
		t.Fatal(err)
	}
	if obj.GetMetadata()["m"] != "v" {
		t.Errorf("metadata = %v, want the request's", obj.GetMetadata())
	}
	if _, err := upload(); !errors.Is(err, paladin.ErrAlreadyExists) {
		t.Fatalf("second upload: %v, want ErrAlreadyExists", err)
	}
	_, err = p.Data.MultipartUpload.InitiateMultipartUpload(ctx, connect.NewRequest(&datav1.InitiateMultipartUploadRequest{
		Parent: srv.Collection().String(), Key: "k", SizeBytes: 1,
	}))
	if !errors.Is(err, paladin.ErrAlreadyExists) {
		t.Fatalf("multipart at the key: %v, want ErrAlreadyExists", err)
	}
	del := func(permanent bool) {
		if _, err := p.Data.Object.DeleteObject(ctx, connect.NewRequest(&datav1.DeleteObjectRequest{
			Name: obj.GetName(), ResourceVersion: obj.GetResourceVersion(), Permanent: permanent,
		})); err != nil {
			t.Fatal(err)
		}
	}
	del(false)
	if _, err := upload(); !errors.Is(err, paladin.ErrAlreadyExists) {
		t.Fatalf("upload over the trash: %v, want ErrAlreadyExists", err)
	}
	del(true)
	if _, err := upload(); err != nil {
		t.Fatalf("upload after a permanent delete: %v", err)
	}
}

func TestTheFakeLooksUpAndFailsPendingObjects(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	ctx := context.Background()
	resp, err := p.Data.Object.UploadObject(ctx, connect.NewRequest(&datav1.UploadObjectRequest{
		Parent: srv.Collection().String(), Key: "k", ContentType: "text/plain", SizeHintBytes: 1,
		ChecksumValue: "LGW4m3CYc5cGdyqLzSa4tqkq2Hj1/jmn6wGAWuR+hEk=",
	}))
	if err != nil {
		t.Fatal(err)
	}
	lookup := func() datav1.ObjectState {
		obj, err := paladin.LookupObject(ctx, p.Data, paladin.ObjectURI{Collection: srv.Collection(), Key: "k"})
		if err != nil {
			t.Fatal(err)
		}
		return obj.GetState()
	}
	if got := lookup(); got != datav1.ObjectState_OBJECT_STATE_PENDING {
		t.Errorf("lookup = %s, want PENDING: the server finds an object in any state but DELETED", got)
	}
	srv.MarkFailed(resp.Msg.GetObject().GetName())
	if got := lookup(); got != datav1.ObjectState_OBJECT_STATE_FAILED {
		t.Errorf("after MarkFailed = %s, want FAILED", got)
	}
}

// getObject reads obj through GetObject, the RPC the FailRPC tests fail.
func getObject(p *paladin.Paladin, obj *datav1.Object) error {
	_, err := p.Data.Object.GetObject(context.Background(), connect.NewRequest(&datav1.GetObjectRequest{Name: obj.GetName()}))
	return err
}

// calls counts the requests for procedure the fake received.
func calls(srv *paladintest.Server, procedure string) int {
	return len(srv.Calls(procedure, nil))
}

// FailRPC answers the next calls with the code and the reason the server
// attaches to it, records each, and then serves the RPC again.
func TestFailRPC(t *testing.T) {
	const getObjectRPC = paladindatav1connect.ObjectServiceGetObjectProcedure
	cases := []struct {
		name   string
		times  int
		code   connect.Code
		kind   error
		reason commonv1.ErrorReason
	}{
		{"one Unavailable", 1, connect.CodeUnavailable, nil, commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED},
		{"one NotFound", 1, connect.CodeNotFound, paladin.ErrNotFound, commonv1.ErrorReason_ERROR_REASON_NOT_FOUND},
		{"three Unavailable", 3, connect.CodeUnavailable, nil, commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED},
		{"DataLoss", 1, connect.CodeDataLoss, nil, commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED},
		{"Aborted", 1, connect.CodeAborted, paladin.ErrVersionConflict, commonv1.ErrorReason_ERROR_REASON_VERSION_CONFLICT},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := paladintest.New(t)
			p := srv.Connect()
			obj := srv.Put(srv.Collection(), "k", "text/plain", []byte("x"))
			srv.FailRPC(getObjectRPC, tc.times, tc.code)
			for i := range tc.times {
				err := getObject(p, obj)
				if connect.CodeOf(err) != tc.code {
					t.Fatalf("call %d: err = %v, want %s", i+1, err, tc.code)
				}
				if tc.kind != nil && !errors.Is(err, tc.kind) {
					t.Errorf("call %d: err = %v, want errors.Is %v", i+1, err, tc.kind)
				}
				if got := paladin.Reason(err); got != tc.reason {
					t.Errorf("call %d: reason = %s, want %s", i+1, got, tc.reason)
				}
			}
			if err := getObject(p, obj); err != nil {
				t.Errorf("call %d, past the failures: %v", tc.times+1, err)
			}
			if got := calls(srv, getObjectRPC); got != tc.times+1 {
				t.Errorf("requests = %d, want %d: every failed call is recorded", got, tc.times+1)
			}
		})
	}
}

// A client that retries Unavailable reads through one failure, and the
// fake shows both attempts.
func TestFailRPCSeesTheRetry(t *testing.T) {
	const (
		attempts  = 3
		baseDelay = time.Millisecond
	)
	srv := paladintest.New(t)
	p := srv.Connect(paladin.WithRetries(attempts, baseDelay))
	obj := srv.Put(srv.Collection(), "k", "text/plain", []byte("x"))
	srv.FailRPC(paladindatav1connect.ObjectServiceGetObjectProcedure, 1, connect.CodeUnavailable)
	if err := getObject(p, obj); err != nil {
		t.Fatalf("the retried read: %v", err)
	}
	if got := calls(srv, paladindatav1connect.ObjectServiceGetObjectProcedure); got != 2 {
		t.Errorf("requests = %d, want 2: the failure and the retry", got)
	}
}

// The failure is the procedure's alone: another RPC of the same service is
// served.
func TestFailRPCFailsOnlyItsProcedure(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	obj := srv.Put(srv.Collection(), "k", "text/plain", []byte("x"))
	srv.FailRPC(paladindatav1connect.ObjectServiceGetObjectProcedure, 1, connect.CodeUnavailable)
	if _, err := paladin.LookupObject(context.Background(), p.Data, paladin.ObjectURI{Collection: srv.Collection(), Key: "k"}); err != nil {
		t.Errorf("LookupObject: %v, want it served", err)
	}
	if err := getObject(p, obj); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Errorf("GetObject: %v, want the failure still pending", err)
	}
}

func TestFailRPCResetRestoresTheRPC(t *testing.T) {
	const times = 5
	srv := paladintest.New(t)
	p := srv.Connect()
	obj := srv.Put(srv.Collection(), "k", "text/plain", []byte("x"))
	reset := srv.FailRPC(paladindatav1connect.ObjectServiceGetObjectProcedure, times, connect.CodeUnavailable)
	if err := getObject(p, obj); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("before reset: %v, want Unavailable", err)
	}
	reset()
	if err := getObject(p, obj); err != nil {
		t.Errorf("after reset: %v, want the object", err)
	}
	reset() // twice is harmless
}

// A reset clears its own failure, not one a later FailRPC set on the same
// procedure.
func TestFailRPCResetLeavesALaterFailure(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	obj := srv.Put(srv.Collection(), "k", "text/plain", []byte("x"))
	first := srv.FailRPC(paladindatav1connect.ObjectServiceGetObjectProcedure, 1, connect.CodeUnavailable)
	srv.FailRPC(paladindatav1connect.ObjectServiceGetObjectProcedure, 1, connect.CodeNotFound)
	first()
	if err := getObject(p, obj); !errors.Is(err, paladin.ErrNotFound) {
		t.Errorf("err = %v, want the later NotFound", err)
	}
}

// Any procedure of a served service can fail, one the fake answers
// Unimplemented included.
func TestFailRPCOnAnUnimplementedProcedure(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	srv.FailRPC(paladindatav1connect.ObjectServiceCountObjectsProcedure, 1, connect.CodeUnavailable)
	count := func() error {
		_, err := p.Data.Object.CountObjects(context.Background(), connect.NewRequest(&datav1.CountObjectsRequest{}))
		return err
	}
	if err := count(); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Errorf("err = %v, want the injected Unavailable", err)
	}
	if err := count(); !errors.Is(err, paladin.ErrContractSkew) {
		t.Errorf("err = %v, want Unimplemented once the failure is spent", err)
	}
}

func TestFailRPCRefusesAMistake(t *testing.T) {
	cases := []struct {
		name      string
		procedure string
		times     int
		code      connect.Code
	}{
		{"a procedure no service has", "/paladin.data.v1.ObjectService/NoSuchRPC", 1, connect.CodeUnavailable},
		{"a service the fake does not serve", "/paladin.admin.v1.TenantService/ListTenants", 1, connect.CodeUnavailable},
		{"not a procedure", "GetObject", 1, connect.CodeUnavailable},
		{"zero times", paladindatav1connect.ObjectServiceGetObjectProcedure, 0, connect.CodeUnavailable},
		{"no code", paladindatav1connect.ObjectServiceGetObjectProcedure, 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := paladintest.New(t)
			defer func() {
				if recover() == nil {
					t.Error("FailRPC did not panic")
				}
			}()
			srv.FailRPC(tc.procedure, tc.times, tc.code)
		})
	}
}

// presignDownload asks the fake for a download URL.
func presignDownload(p *paladin.Paladin, req *datav1.PresignDownloadRequest) (*datav1.PresignDownloadResponse, error) {
	resp, err := p.Data.Presign.PresignDownload(context.Background(), connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// fetch GETs a presigned URL with its required headers, and overrides.
func fetch(t *testing.T, signed *commonv1.PresignedUrl, override map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), signed.GetMethod(), signed.GetUrl(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range signed.GetRequiredHeaders() {
		req.Header.Set(k, v)
	}
	for k, v := range override {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// A PresignDownload URL reads the object's bytes from the fake's storage,
// expiring after the default TTL.
func TestPresignDownloadReadsTheObject(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	body := []byte("presigned body")
	obj := srv.Put(srv.Collection(), "k", "text/plain", body)
	before := time.Now()
	resp, err := presignDownload(p, &datav1.PresignDownloadRequest{Name: obj.GetName()})
	if err != nil {
		t.Fatal(err)
	}
	signed := resp.GetDownloadUrl()
	if signed.GetMethod() != http.MethodGet || len(signed.GetRequiredHeaders()) != 0 {
		t.Errorf("url = %v, want a GET with no required headers", signed)
	}
	expires, err := time.Parse(time.RFC3339, signed.GetExpiresAtRfc3339())
	if err != nil {
		t.Fatalf("expires_at %q: %v", signed.GetExpiresAtRfc3339(), err)
	}
	// RFC 3339 here has whole seconds: allow one either way.
	if lo, hi := before.Add(paladintest.DefaultDownloadTTL-time.Second), time.Now().Add(paladintest.DefaultDownloadTTL+time.Second); expires.Before(lo) || expires.After(hi) {
		t.Errorf("expires_at = %s, want about %s from now", expires, paladintest.DefaultDownloadTTL)
	}
	got := fetch(t, signed, nil)
	data, err := io.ReadAll(got.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got.StatusCode != http.StatusOK || !bytes.Equal(data, body) {
		t.Errorf("GET = %d %q, want 200 %q", got.StatusCode, data, body)
	}
}

func TestPresignDownloadHonoursTheRequest(t *testing.T) {
	const (
		ttl         = time.Hour
		disposition = `attachment; filename="a.txt"`
	)
	srv := paladintest.New(t)
	p := srv.Connect()
	obj := srv.Put(srv.Collection(), "k", "text/plain", []byte("x"))
	resp, err := presignDownload(p, &datav1.PresignDownloadRequest{
		Name: obj.GetName(), Ttl: durationpb.New(ttl), ContentDisposition: disposition, RequireEtagMatch: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	signed := resp.GetDownloadUrl()
	expires, err := time.Parse(time.RFC3339, signed.GetExpiresAtRfc3339())
	if err != nil {
		t.Fatal(err)
	}
	if left := time.Until(expires); left < ttl-time.Minute || left > ttl+time.Second {
		t.Errorf("expires in %s, want the requested %s", left, ttl)
	}
	if want := `"` + obj.GetEtag() + `"`; signed.GetRequiredHeaders()["If-Match"] != want {
		t.Errorf("If-Match = %q, want %q", signed.GetRequiredHeaders()["If-Match"], want)
	}
	got := fetch(t, signed, nil)
	if got.StatusCode != http.StatusOK || got.Header.Get("Content-Disposition") != disposition {
		t.Errorf("GET = %d, Content-Disposition %q; want 200 and %q", got.StatusCode, got.Header.Get("Content-Disposition"), disposition)
	}
	if stale := fetch(t, signed, map[string]string{"If-Match": `"stale"`}); stale.StatusCode != http.StatusPreconditionFailed {
		t.Errorf("a stale If-Match: %d, want 412", stale.StatusCode)
	}
}

// PresignDownload refuses what the server refuses, with the server's codes.
func TestPresignDownloadRefusesWhatTheServerRefuses(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	ctx := context.Background()
	available := srv.Put(srv.Collection(), "available", "text/plain", []byte("x"))
	register := func(key string) string {
		resp, err := p.Data.Object.UploadObject(ctx, connect.NewRequest(&datav1.UploadObjectRequest{
			Parent: srv.Collection().String(), Key: key, ChecksumValue: emptySHA256,
		}))
		if err != nil {
			t.Fatal(err)
		}
		return resp.Msg.GetObject().GetName()
	}
	pending := register("pending")
	failed := register("failed")
	srv.MarkFailed(failed)
	unknown := paladin.ObjectName{CollectionName: srv.Collection(), Object: "00000000-0000-0000-0000-000000000000"}.String()
	cases := []struct {
		name string
		req  *datav1.PresignDownloadRequest
		code connect.Code
	}{
		{"pending", &datav1.PresignDownloadRequest{Name: pending}, connect.CodeFailedPrecondition},
		{"failed", &datav1.PresignDownloadRequest{Name: failed}, connect.CodeFailedPrecondition},
		{"unknown", &datav1.PresignDownloadRequest{Name: unknown}, connect.CodeNotFound},
		{"not an object name", &datav1.PresignDownloadRequest{Name: "objects/x"}, connect.CodeInvalidArgument},
		{"a negative ttl", &datav1.PresignDownloadRequest{Name: available.GetName(), Ttl: durationpb.New(-time.Second)}, connect.CodeInvalidArgument},
		{"a ttl above the maximum", &datav1.PresignDownloadRequest{Name: available.GetName(), Ttl: durationpb.New(paladintest.MaxPresignTTL + time.Second)}, connect.CodeInvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := presignDownload(p, tc.req); connect.CodeOf(err) != tc.code {
				t.Errorf("err = %v, want %s", err, tc.code)
			}
		})
	}
}

// Requests carries each call's message, so a test tells its calls apart by
// what they named.
func TestRequestsCarryTheMessage(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	mine := srv.Put(srv.Collection(), "mine", "text/plain", []byte("x"))
	theirs := srv.Put(srv.Collection(), "theirs", "text/plain", []byte("y"))
	for _, obj := range []*datav1.Object{mine, theirs, mine} {
		if err := getObject(p, obj); err != nil {
			t.Fatal(err)
		}
	}
	reqs := srv.Requests()
	if len(reqs) != 3 {
		t.Fatalf("requests = %d, want 3", len(reqs))
	}
	got, ok := reqs[1].Message.(*datav1.GetObjectRequest)
	if !ok || got.GetName() != theirs.GetName() {
		t.Errorf("second message = %v, want a GetObjectRequest for %s", reqs[1].Message, theirs.GetName())
	}
	named := func(name string) func(proto.Message) bool {
		return func(m proto.Message) bool { return m.(*datav1.GetObjectRequest).GetName() == name }
	}
	if n := len(srv.Calls(paladindatav1connect.ObjectServiceGetObjectProcedure, named(mine.GetName()))); n != 2 {
		t.Errorf("calls for mine = %d, want 2", n)
	}
	if n := len(srv.Calls(paladindatav1connect.ObjectServiceGetObjectProcedure, nil)); n != 3 {
		t.Errorf("calls with no filter = %d, want 3", n)
	}
	if n := len(srv.Calls(paladindatav1connect.ObjectServiceLookupObjectProcedure, nil)); n != 0 {
		t.Errorf("calls of another procedure = %d, want 0", n)
	}
}

// The recorded message is a copy: changing what Requests returned changes
// nothing a later Requests sees.
func TestRequestsAreCopies(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	obj := srv.Put(srv.Collection(), "k", "text/plain", []byte("x"))
	if err := getObject(p, obj); err != nil {
		t.Fatal(err)
	}
	first := srv.Requests()[0]
	first.Message.(*datav1.GetObjectRequest).Name = "changed"
	first.Header.Set(paladin.HeaderUserAgent, "changed")
	again := srv.Requests()[0]
	if again.Message.(*datav1.GetObjectRequest).GetName() != obj.GetName() || again.Header.Get(paladin.HeaderUserAgent) == "changed" {
		t.Errorf("a change to a returned request reached the fake: %v", again)
	}
}
