package paladin_test

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // a fake storage's ETag, as S3 computes it
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

// testContentType is a test upload's content type: the server refuses an
// upload that names none.
const testContentType = "application/octet-stream"

// ─── Pages ──────────────────────────────────────────────────────────────────

// pagedObjects serves ListObjects over items, pageSize at a time.
type pagedObjects struct {
	paladindatav1connect.UnimplementedObjectServiceHandler
	items    []string
	pageSize int
	failAt   int // the call that fails, 1-based; 0 never
	mu       sync.Mutex
	calls    int
}

func (p *pagedObjects) ListObjects(_ context.Context, req *datav1.ListObjectsRequest) (*datav1.ListObjectsResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.calls == p.failAt {
		return nil, connect.NewError(connect.CodeUnavailable, "injected")
	}
	start := 0
	if tok := req.GetPage().GetPageToken(); tok != "" {
		_, _ = fmt.Sscanf(tok, "%d", &start)
	}
	end := min(start+p.pageSize, len(p.items))
	resp := &datav1.ListObjectsResponse{Page: &commonv1.PageResponse{}}
	for _, name := range p.items[start:end] {
		resp.Objects = append(resp.Objects, &datav1.Object{Name: name})
	}
	if end < len(p.items) {
		resp.Page.NextPageToken = fmt.Sprint(end)
	}
	return resp, nil
}

func objectClient(t *testing.T, h paladindatav1connect.ObjectServiceHandler) paladindatav1connect.ObjectServiceClient {
	t.Helper()
	mux := http.NewServeMux()
	server := connect.NewServer()
	paladindatav1connect.RegisterObjectServiceHandler(server, h)
	connecthttp.Mount(mux, server)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p, err := paladin.Connect(paladin.Endpoints{Data: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return p.Data.Object
}

func TestPagesFollowsEveryPage(t *testing.T) {
	const pageSize = 2
	srv := &pagedObjects{items: []string{"a", "b", "c", "d", "e"}, pageSize: pageSize}
	objects := objectClient(t, srv)

	var got []string
	for obj, err := range paladin.Pages(context.Background(), objects.ListObjects,
		&datav1.ListObjectsRequest{Parent: "tenants/t/collections/c"}, (*datav1.ListObjectsResponse).GetObjects) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, obj.GetName())
	}
	if strings.Join(got, ",") != "a,b,c,d,e" {
		t.Errorf("items = %v", got)
	}
	if srv.calls != 3 {
		t.Errorf("calls = %d, want 3 pages", srv.calls)
	}
}

func TestPagesStopsWhenTheLoopDoes(t *testing.T) {
	srv := &pagedObjects{items: []string{"a", "b", "c", "d"}, pageSize: 1}
	objects := objectClient(t, srv)
	for range paladin.Pages(context.Background(), objects.ListObjects,
		&datav1.ListObjectsRequest{}, (*datav1.ListObjectsResponse).GetObjects) {
		break
	}
	if srv.calls != 1 {
		t.Errorf("calls = %d after stopping at the first item, want 1", srv.calls)
	}
}

func TestPagesYieldsTheErrorAndStops(t *testing.T) {
	srv := &pagedObjects{items: []string{"a", "b", "c"}, pageSize: 1, failAt: 2}
	objects := objectClient(t, srv)
	var items, errs int
	for _, err := range paladin.Pages(context.Background(), objects.ListObjects,
		&datav1.ListObjectsRequest{}, (*datav1.ListObjectsResponse).GetObjects) {
		if err != nil {
			errs++
			continue
		}
		items++
	}
	if items != 1 || errs != 1 || srv.calls != 2 {
		t.Errorf("items/errors/calls = %d/%d/%d, want 1/1/2", items, errs, srv.calls)
	}
}

func TestPagesRefusesAnUnpagedRPC(t *testing.T) {
	get := func(context.Context, *datav1.GetObjectRequest) (*datav1.Object, error) {
		return &datav1.Object{}, nil
	}
	for _, err := range paladin.Pages(context.Background(), get, &datav1.GetObjectRequest{},
		func(*datav1.Object) []string { return nil }) {
		if !errors.Is(err, paladin.ErrNotPaged) {
			t.Fatalf("err = %v, want ErrNotPaged", err)
		}
	}
}

// ─── Mask ───────────────────────────────────────────────────────────────────

func TestMask(t *testing.T) {
	cases := []struct {
		name  string
		paths []string
		ok    bool
	}{
		{"top-level fields", []string{"filter", "sink", "disabled"}, true},
		{"a nested field", []string{"sink.http.url"}, true},
		{"a misspelt field", []string{"filtr"}, false},
		{"a path through a scalar", []string{"filter.x"}, false},
		{"a nested misspelling", []string{"sink.http.uri"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := paladin.Mask[*adminv1.EventSubscription](tc.paths...)
			if tc.ok {
				if err != nil || strings.Join(m.GetPaths(), ",") != strings.Join(tc.paths, ",") {
					t.Fatalf("Mask = %v, %v", m, err)
				}
				return
			}
			if !errors.Is(err, paladin.ErrUnknownMaskPath) {
				t.Fatalf("err = %v, want ErrUnknownMaskPath", err)
			}
		})
	}
}

// ─── Upload and Download ────────────────────────────────────────────────────

// storage is a fake S3: presigned PUTs store bytes and answer an MD5 ETag,
// GETs return them and honour Range. It can refuse a part, ignore ranges or
// redirect, and records required headers and the Host each request named.
type storage struct {
	mu          sync.Mutex
	blobs       map[string][]byte
	refusePart  string
	ignoreRange bool
	redirectTo  string
	headersSeen map[string]string
	hosts       []string
	traced      int // requests that carried a traceparent
	refuse      int // status every request is refused with; 0 none
}

func (s *storage) refuseWith(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refuse = status
}

func (s *storage) traceparents() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.traced
}

const (
	requiredHeader = "X-Amz-Checksum-Sha256"
	requiredValue  = "signed"
)

func (s *storage) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hosts = append(s.hosts, r.Host)
	if r.Header.Get("Traceparent") != "" {
		s.traced++
	}
	if s.refuse != 0 {
		http.Error(w, "refused", s.refuse)
		return
	}
	if s.redirectTo != "" {
		http.Redirect(w, r, s.redirectTo, http.StatusTemporaryRedirect)
		return
	}
	key := r.URL.Path + "?" + r.URL.RawQuery
	switch r.Method {
	case http.MethodPut:
		s.headersSeen[key] = r.Header.Get(requiredHeader)
		if r.URL.Query().Get("part") != "" && r.URL.Query().Get("part") == s.refusePart {
			http.Error(w, "SlowDown", http.StatusServiceUnavailable)
			return
		}
		body, _ := io.ReadAll(r.Body)
		s.blobs[key] = body
		sum := md5.Sum(body) //nolint:gosec // S3's ETag
		w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
	case http.MethodGet:
		b, ok := s.blobs[r.URL.Path+"?"]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if s.ignoreRange {
			r.Header.Del("Range")
		}
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(b))
	}
}

func etagOf(b []byte) string {
	sum := md5.Sum(b) //nolint:gosec // S3's ETag
	return hex.EncodeToString(sum[:])
}

// dataPlane is a fake data plane in front of storage.
type dataPlane struct {
	paladindatav1connect.UnimplementedObjectServiceHandler
	paladindatav1connect.UnimplementedMultipartUploadServiceHandler
	storageURL string
	partSize   int64
	mu         sync.Mutex
	completed  map[string]string // object → etag
	checksums  map[string]string // object → checksum value CompleteObject got
	parts      []*datav1.CompletedPart
	aborted    int
	// described is what DownloadObject says of the object: size, checksum.
	described *datav1.Object
	noURL     bool
	lookedUp  []string // parent|key of every LookupObject
}

func (d *dataPlane) signed(path string) *commonv1.PresignedUrl {
	return &commonv1.PresignedUrl{Url: d.storageURL + path, RequiredHeaders: map[string]string{requiredHeader: requiredValue}}
}

func (d *dataPlane) UploadObject(_ context.Context, req *datav1.UploadObjectRequest) (*datav1.UploadObjectResponse, error) {
	name := req.GetParent() + "/objects/" + req.GetKey()
	return &datav1.UploadObjectResponse{
		Object: &datav1.Object{Name: name}, UploadUrl: d.signed("/" + req.GetKey()),
	}, nil
}

func (d *dataPlane) CompleteObject(_ context.Context, req *datav1.CompleteObjectRequest) (*datav1.Object, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.completed[req.GetName()] = req.GetEtag()
	d.checksums[req.GetName()] = req.GetChecksumValue()
	return &datav1.Object{Name: req.GetName()}, nil
}

func (d *dataPlane) DownloadObject(_ context.Context, req *datav1.DownloadObjectRequest) (*datav1.DownloadObjectResponse, error) {
	key := req.GetName()[strings.LastIndex(req.GetName(), "/"):]
	object := &datav1.Object{Name: req.GetName()}
	if d.described != nil {
		object = d.described
	}
	resp := &datav1.DownloadObjectResponse{Object: object, DownloadUrl: d.signed(key)}
	if d.noURL {
		resp.DownloadUrl = nil
	}
	return resp, nil
}

func (d *dataPlane) LookupObject(_ context.Context, req *datav1.LookupObjectRequest) (*datav1.Object, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lookedUp = append(d.lookedUp, req.GetParent()+"|"+req.GetKey())
	return &datav1.Object{Name: req.GetParent() + "/objects/" + req.GetKey()}, nil
}

func (d *dataPlane) InitiateMultipartUpload(_ context.Context, req *datav1.InitiateMultipartUploadRequest) (*datav1.InitiateMultipartUploadResponse, error) {
	return &datav1.InitiateMultipartUploadResponse{
		Object: &datav1.Object{Name: req.GetParent() + "/objects/" + req.GetKey()}, UploadId: "u1",
		RecommendedPartSize: d.partSize,
	}, nil
}

func (d *dataPlane) PresignPart(_ context.Context, req *datav1.PresignPartRequest) (*datav1.PresignPartResponse, error) {
	return &datav1.PresignPartResponse{
		UploadUrl: d.signed(fmt.Sprintf("/mp?part=%d", req.GetPartNumber())),
	}, nil
}

func (d *dataPlane) CompleteMultipartUpload(_ context.Context, req *datav1.CompleteMultipartUploadRequest) (*datav1.Object, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.parts = req.GetParts()
	return &datav1.Object{Name: req.GetObjectName()}, nil
}

func (d *dataPlane) AbortMultipartUpload(context.Context, *datav1.AbortMultipartUploadRequest) (*datav1.AbortMultipartUploadResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.aborted++
	return &datav1.AbortMultipartUploadResponse{}, nil
}

func newTransfer(t *testing.T, partSize int64, opts ...paladin.Option) (*paladin.DataPlane, *dataPlane, *storage) {
	t.Helper()
	st := &storage{blobs: map[string][]byte{}, headersSeen: map[string]string{}}
	stSrv := httptest.NewServer(st)
	t.Cleanup(stSrv.Close)
	dp := &dataPlane{storageURL: stSrv.URL, partSize: partSize, completed: map[string]string{}, checksums: map[string]string{}}
	return connectData(t, dp, opts...), dp, st
}

// connectData serves dp and returns a data plane connected to it with opts.
func connectData(t *testing.T, dp *dataPlane, opts ...paladin.Option) *paladin.DataPlane {
	t.Helper()
	mux := http.NewServeMux()
	server := connect.NewServer()
	paladindatav1connect.RegisterObjectServiceHandler(server, dp)
	paladindatav1connect.RegisterMultipartUploadServiceHandler(server, dp)
	connecthttp.Mount(mux, server)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p, err := paladin.Connect(paladin.Endpoints{Data: srv.URL}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return p.Data
}

const testParent = "tenants/t/collections/c"

func TestUploadAndDownloadASmallObject(t *testing.T) {
	data, dp, st := newTransfer(t, 0)
	body := []byte("hello, paladin")
	obj, err := paladin.Upload(context.Background(), data, paladin.UploadInput{
		Parent: testParent, Key: "k.txt", ContentType: "text/plain", Size: int64(len(body)), Body: bytes.NewReader(body),
	}, paladin.UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := dp.completed[obj.GetName()]; got != etagOf(body) {
		t.Errorf("CompleteObject got ETag %q, want %q", got, etagOf(body))
	}
	if got := st.headersSeen["/k.txt?"]; got != requiredValue {
		t.Errorf("required header = %q, want %q: storage refuses a request without it", got, requiredValue)
	}

	r, err := paladin.Download(context.Background(), data, obj.GetName(), paladin.DownloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	got, _ := io.ReadAll(r)
	if !bytes.Equal(got, body) {
		t.Errorf("downloaded %q, want %q", got, body)
	}
}

func TestUploadSplitsALargeObjectIntoParts(t *testing.T) {
	const (
		partSize  = 4
		threshold = 8
	)
	data, dp, st := newTransfer(t, partSize)
	body := []byte("0123456789") // three parts: 4, 4, 2
	if _, err := paladin.Upload(context.Background(), data, paladin.UploadInput{
		Parent: testParent, Key: "big.bin", ContentType: "application/octet-stream", Size: int64(len(body)), Body: bytes.NewReader(body),
	}, paladin.UploadOptions{MultipartThreshold: threshold}); err != nil {
		t.Fatal(err)
	}
	want := []string{"0123", "4567", "89"}
	if len(dp.parts) != len(want) {
		t.Fatalf("completed %d parts, want %d", len(dp.parts), len(want))
	}
	for i, chunk := range want {
		p := dp.parts[i]
		if p.GetPartNumber() != int32(i+1) || p.GetEtag() != etagOf([]byte(chunk)) {
			t.Errorf("part %d = #%d %q, want #%d %q", i, p.GetPartNumber(), p.GetEtag(), i+1, etagOf([]byte(chunk)))
		}
		if got := string(st.blobs[fmt.Sprintf("/mp?part=%d", i+1)]); got != chunk {
			t.Errorf("part %d stored %q, want %q", i+1, got, chunk)
		}
	}
	if dp.aborted != 0 {
		t.Error("a successful upload was aborted")
	}
}

func TestUploadAbortsWhenAPartIsRefused(t *testing.T) {
	data, dp, st := newTransfer(t, 4)
	st.refusePart = "2"
	body := []byte("0123456789")
	_, err := paladin.Upload(context.Background(), data, paladin.UploadInput{
		Parent: testParent, Key: "big.bin", ContentType: "application/octet-stream", Size: int64(len(body)), Body: bytes.NewReader(body),
	}, paladin.UploadOptions{MultipartThreshold: 8})
	var transfer *paladin.TransferError
	if !errors.As(err, &transfer) || transfer.Status != http.StatusServiceUnavailable {
		t.Fatalf("err = %v, want a TransferError with 503", err)
	}
	if dp.aborted != 1 || dp.parts != nil {
		t.Errorf("aborted %d times, completed %v; want the session aborted and not completed", dp.aborted, dp.parts)
	}
}

func TestUploadRefusesANegativeSize(t *testing.T) {
	data, _, _ := newTransfer(t, 0)
	if _, err := paladin.Upload(context.Background(), data, paladin.UploadInput{ContentType: testContentType, Parent: testParent, Size: -1, Body: bytes.NewReader(nil)}, paladin.UploadOptions{}); !errors.Is(err, paladin.ErrUploadSize) {
		t.Fatalf("err = %v, want ErrUploadSize", err)
	}
}

// Every upload URL is now signed for its body's size and SHA-256. An empty
// object is a size of zero — it used to be refused outright — and a stream
// is hashed before it is presigned.
func TestUploadBindsEveryBody(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	ctx := context.Background()
	for name, in := range map[string]paladin.UploadInput{
		"an empty object": {Key: "empty", ContentType: "text/plain", Size: 0, Body: bytes.NewReader(nil)},
		"a stream":        {Key: "stream", ContentType: "text/plain", Size: 5, Stream: strings.NewReader("hello")},
		"a multipart stream": {Key: "big", ContentType: "application/octet-stream", Size: 2*paladintest.PartSize + 7,
			Stream: bytes.NewReader(bytes.Repeat([]byte("z"), 2*paladintest.PartSize+7))},
	} {
		t.Run(name, func(t *testing.T) {
			in.Parent = srv.Collection().String()
			obj, err := paladin.Upload(ctx, p.Data, in, paladin.UploadOptions{MultipartThreshold: paladintest.PartSize})
			if err != nil {
				t.Fatalf("Upload: %v", err)
			}
			if obj.GetSizeBytes() != in.Size {
				t.Fatalf("stored %d bytes, want %d", obj.GetSizeBytes(), in.Size)
			}
		})
	}
}

// Download binds its URL to the object's ETag and sends the If-Match that
// binding requires; the fake's storage answers 412 to a wrong one.
func TestDownloadIsBoundToTheETag(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	ctx := context.Background()
	obj := srv.Put(srv.Collection(), "k", "text/plain", []byte("etag-bound"))

	r, err := paladin.Download(ctx, p.Data, obj.GetName(), paladin.DownloadOptions{Offset: 5})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	b, _ := io.ReadAll(r)
	_ = r.Close()
	if string(b) != "bound" {
		t.Fatalf("read %q, want the range", b)
	}
	var sawDownload bool
	for _, req := range srv.Requests() {
		if strings.HasSuffix(req.Procedure, "/DownloadObject") {
			sawDownload = true
		}
	}
	if !sawDownload {
		t.Fatal("no DownloadObject call recorded")
	}
}

// Every part URL is signed for the size the server recommended, so a sender
// cannot pick a split of its own: an answer naming none is refused, and the
// upload it opened is aborted rather than left for the sweep.
func TestBeginMultipartRefusesAnAnswerWithNoSplit(t *testing.T) {
	data, dp, _ := newTransfer(t, 0) // the fake recommends no part size
	_, err := paladin.BeginMultipart(context.Background(), data, paladin.MultipartInput{
		Parent: testParent, Key: "k", ContentType: "application/octet-stream", Size: 10,
	})
	if !errors.Is(err, paladin.ErrNoPartSplit) {
		t.Fatalf("err = %v, want ErrNoPartSplit", err)
	}
	if dp.aborted != 1 {
		t.Errorf("aborted %d uploads, want the one opened", dp.aborted)
	}
}
