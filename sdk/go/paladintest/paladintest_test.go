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

	"connectrpc.com/connect"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
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
