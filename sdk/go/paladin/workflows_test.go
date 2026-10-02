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

	"connectrpc.com/connect"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

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

func (p *pagedObjects) ListObjects(_ context.Context, req *connect.Request[datav1.ListObjectsRequest]) (*connect.Response[datav1.ListObjectsResponse], error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.calls == p.failAt {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("injected"))
	}
	start := 0
	if tok := req.Msg.GetPage().GetPageToken(); tok != "" {
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
	return connect.NewResponse(resp), nil
}

func objectClient(t *testing.T, h paladindatav1connect.ObjectServiceHandler) paladindatav1connect.ObjectServiceClient {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(paladindatav1connect.NewObjectServiceHandler(h))
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
	get := func(context.Context, *connect.Request[datav1.GetObjectRequest]) (*connect.Response[datav1.Object], error) {
		return connect.NewResponse(&datav1.Object{}), nil
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
// GETs return them. It can refuse a part, and records required headers.
type storage struct {
	mu          sync.Mutex
	blobs       map[string][]byte
	refusePart  string
	headersSeen map[string]string
}

const (
	requiredHeader = "X-Amz-Checksum-Sha256"
	requiredValue  = "signed"
)

func (s *storage) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
		_, _ = w.Write(b)
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
	parts      []*datav1.CompletedPart
	aborted    int
}

func (d *dataPlane) signed(path string) *commonv1.PresignedUrl {
	return &commonv1.PresignedUrl{Url: d.storageURL + path, RequiredHeaders: map[string]string{requiredHeader: requiredValue}}
}

func (d *dataPlane) UploadObject(_ context.Context, req *connect.Request[datav1.UploadObjectRequest]) (*connect.Response[datav1.UploadObjectResponse], error) {
	name := req.Msg.GetParent() + "/objects/" + req.Msg.GetKey()
	return connect.NewResponse(&datav1.UploadObjectResponse{
		Object: &datav1.Object{Name: name}, UploadUrl: d.signed("/" + req.Msg.GetKey()),
	}), nil
}

func (d *dataPlane) CompleteObject(_ context.Context, req *connect.Request[datav1.CompleteObjectRequest]) (*connect.Response[datav1.Object], error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.completed[req.Msg.GetName()] = req.Msg.GetEtag()
	return connect.NewResponse(&datav1.Object{Name: req.Msg.GetName()}), nil
}

func (d *dataPlane) DownloadObject(_ context.Context, req *connect.Request[datav1.DownloadObjectRequest]) (*connect.Response[datav1.DownloadObjectResponse], error) {
	key := req.Msg.GetName()[strings.LastIndex(req.Msg.GetName(), "/"):]
	return connect.NewResponse(&datav1.DownloadObjectResponse{
		Object: &datav1.Object{Name: req.Msg.GetName()}, DownloadUrl: d.signed(key),
	}), nil
}

func (d *dataPlane) InitiateMultipartUpload(_ context.Context, req *connect.Request[datav1.InitiateMultipartUploadRequest]) (*connect.Response[datav1.InitiateMultipartUploadResponse], error) {
	return connect.NewResponse(&datav1.InitiateMultipartUploadResponse{
		Object: &datav1.Object{Name: req.Msg.GetParent() + "/objects/" + req.Msg.GetKey()}, UploadId: "u1",
		RecommendedPartSize: d.partSize,
	}), nil
}

func (d *dataPlane) PresignPart(_ context.Context, req *connect.Request[datav1.PresignPartRequest]) (*connect.Response[datav1.PresignPartResponse], error) {
	return connect.NewResponse(&datav1.PresignPartResponse{
		UploadUrl: d.signed(fmt.Sprintf("/mp?part=%d", req.Msg.GetPartNumber())),
	}), nil
}

func (d *dataPlane) CompleteMultipartUpload(_ context.Context, req *connect.Request[datav1.CompleteMultipartUploadRequest]) (*connect.Response[datav1.Object], error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.parts = req.Msg.GetParts()
	return connect.NewResponse(&datav1.Object{Name: req.Msg.GetObjectName()}), nil
}

func (d *dataPlane) AbortMultipartUpload(context.Context, *connect.Request[datav1.AbortMultipartUploadRequest]) (*connect.Response[datav1.AbortMultipartUploadResponse], error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.aborted++
	return connect.NewResponse(&datav1.AbortMultipartUploadResponse{}), nil
}

func newTransfer(t *testing.T, partSize int64) (*paladin.DataPlane, *dataPlane, *storage) {
	t.Helper()
	st := &storage{blobs: map[string][]byte{}, headersSeen: map[string]string{}}
	stSrv := httptest.NewServer(st)
	t.Cleanup(stSrv.Close)
	dp := &dataPlane{storageURL: stSrv.URL, partSize: partSize, completed: map[string]string{}}
	mux := http.NewServeMux()
	mux.Handle(paladindatav1connect.NewObjectServiceHandler(dp))
	mux.Handle(paladindatav1connect.NewMultipartUploadServiceHandler(dp))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p, err := paladin.Connect(paladin.Endpoints{Data: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return p.Data, dp, st
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

	r, _, err := paladin.Download(context.Background(), data, obj.GetName(), nil)
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

func TestUploadRefusesAnEmptyBody(t *testing.T) {
	data, _, _ := newTransfer(t, 0)
	if _, err := paladin.Upload(context.Background(), data, paladin.UploadInput{Parent: testParent}, paladin.UploadOptions{}); !errors.Is(err, paladin.ErrUploadSize) {
		t.Fatalf("err = %v, want ErrUploadSize", err)
	}
}
