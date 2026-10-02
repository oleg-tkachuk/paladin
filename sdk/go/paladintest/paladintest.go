// Package paladintest is an in-memory Paladin data plane for the tests of a
// program built on the SDK: objects uploaded and downloaded through
// presigned URLs, as against the real server, with no server to run.
//
//	srv := paladintest.New(t)
//	p := srv.Connect()
//	obj, err := paladin.Upload(ctx, p.Data, paladin.UploadInput{Parent: srv.Collection().String(), …}, paladin.UploadOptions{})
//
// It serves ObjectService (upload, complete, get, lookup, list, download,
// delete) and MultipartUploadService; every other RPC of every plane answers
// Unimplemented, as a server that lacks it does. Like the server it records
// the checksum an upload completes with, so Download verifies what it
// reads, and answers Range requests.
package paladintest

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // S3's ETag, which the fake reproduces
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// Defaults the fake answers with.
const (
	// DefaultCollection is the collection Collection names.
	DefaultCollection = "default"
	// PartSize is the part size InitiateMultipartUpload recommends.
	PartSize = 5 << 20
	// DefaultPageSize is the page ListObjects answers when asked for none.
	DefaultPageSize = 100
	// storagePath is where the presigned URLs point, on the same server.
	storagePath = "/storage/"
	etagQuote   = `"`
	partQuery   = "part"
	// maxParts is the most parts a multipart upload has, as in S3.
	maxParts = 10000
)

// Server is the fake. Its methods are safe for concurrent use.
type Server struct {
	paladindatav1connect.UnimplementedObjectServiceHandler
	paladindatav1connect.UnimplementedMultipartUploadServiceHandler

	// URL is the server's base URL, for every plane.
	URL    string
	tenant string

	mu      sync.Mutex
	objects map[string]*object // by name
	uploads map[string]*multipart
}

type object struct {
	msg  *datav1.Object
	body []byte
	// put is the body storage holds before CompleteObject: what a PUT sent.
	put []byte
}

type multipart struct {
	name  string
	parts map[int32][]byte
}

// New starts a fake for the test, stopped when it ends.
func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{
		tenant:  uuid.NewString(),
		objects: map[string]*object{},
		uploads: map[string]*multipart{},
	}
	mux := http.NewServeMux()
	mux.Handle(paladindatav1connect.NewObjectServiceHandler(s))
	mux.Handle(paladindatav1connect.NewMultipartUploadServiceHandler(s))
	mux.Handle(storagePath, http.HandlerFunc(s.storage))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	s.URL = srv.URL
	return s
}

// Tenant is the id of the fake's one tenant.
func (s *Server) Tenant() string { return s.tenant }

// Collection is the name of a collection in that tenant, DefaultCollection
// unless another is given; every collection exists.
func (s *Server) Collection(collection ...string) paladin.CollectionName {
	name := DefaultCollection
	if len(collection) > 0 {
		name = collection[0]
	}
	return paladin.CollectionName{Tenant: s.tenant, Collection: name}
}

// Connect returns clients for every plane, all served by the fake.
func (s *Server) Connect(opts ...paladin.Option) *paladin.Paladin {
	p, err := paladin.Connect(paladin.Endpoints{Data: s.URL, Admin: s.URL, IAM: s.URL}, opts...)
	if err != nil {
		panic(fmt.Sprintf("paladintest: %v", err)) // the endpoints are the fake's own, and valid
	}
	return p
}

// Put stores an object directly, as if uploaded and completed, and returns it.
func (s *Server) Put(collection paladin.CollectionName, key, contentType string, body []byte) *datav1.Object {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.newObject(collection.String(), key, contentType)
	s.commit(o, body, "")
	return o.msg
}

// Content returns what an object holds, and whether it exists.
func (s *Server) Content(name string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objects[name]
	if !ok || o.msg.GetState() != datav1.ObjectState_OBJECT_STATE_AVAILABLE {
		return nil, false
	}
	return bytes.Clone(o.body), true
}

func (s *Server) newObject(parent, key, contentType string) *object {
	id := uuid.NewString()
	if key == "" {
		key = id
	}
	o := &object{msg: &datav1.Object{
		Name: parent + "/objects/" + id, ObjectId: id, TenantId: s.tenant,
		Key: key, ContentType: contentType, State: datav1.ObjectState_OBJECT_STATE_PENDING,
	}}
	if c, err := paladin.ParseCollectionName(parent); err == nil {
		o.msg.Collection = c.Collection
	}
	s.objects[o.msg.GetName()] = o
	return o
}

func (s *Server) commit(o *object, body []byte, checksum string) {
	o.body = body
	o.msg.Etag = etagOf(body)
	o.msg.SizeBytes = int64(len(body))
	o.msg.State = datav1.ObjectState_OBJECT_STATE_AVAILABLE
	if checksum != "" {
		o.msg.Checksum = &datav1.ChecksumDigest{Algorithm: paladin.ChecksumSHA256, Value: checksum}
	}
}

func etagOf(body []byte) string {
	sum := md5.Sum(body) //nolint:gosec // S3's ETag
	return hex.EncodeToString(sum[:])
}

// signed is a presigned URL on the fake's own storage.
func (s *Server) signed(path, method string) *commonv1.PresignedUrl {
	return &commonv1.PresignedUrl{Url: s.URL + storagePath + path, Method: method}
}

func notFound(name string) error {
	return connect.NewError(connect.CodeNotFound, fmt.Errorf("%s not found", name))
}

// lookup is the object called name. The caller holds mu.
func (s *Server) lookup(name string) (*object, error) {
	o, ok := s.objects[name]
	if !ok || o.msg.GetState() == datav1.ObjectState_OBJECT_STATE_DELETED {
		return nil, notFound(name)
	}
	return o, nil
}

// ─── ObjectService ──────────────────────────────────────────────────────────

func (s *Server) UploadObject(_ context.Context, req *connect.Request[datav1.UploadObjectRequest]) (*connect.Response[datav1.UploadObjectResponse], error) {
	if _, err := paladin.ParseCollectionName(req.Msg.GetParent()); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.newObject(req.Msg.GetParent(), req.Msg.GetKey(), req.Msg.GetContentType())
	return connect.NewResponse(&datav1.UploadObjectResponse{
		Object: o.msg, UploadUrl: s.signed(o.msg.GetObjectId(), http.MethodPut),
	}), nil
}

func (s *Server) CompleteObject(_ context.Context, req *connect.Request[datav1.CompleteObjectRequest]) (*connect.Response[datav1.Object], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objects[req.Msg.GetName()]
	if !ok {
		return nil, notFound(req.Msg.GetName())
	}
	if o.put == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("nothing was uploaded"))
	}
	if req.Msg.GetEtag() != etagOf(o.put) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("the ETag is not the uploaded content's"))
	}
	s.commit(o, o.put, req.Msg.GetChecksumValue())
	return connect.NewResponse(o.msg), nil
}

func (s *Server) GetObject(_ context.Context, req *connect.Request[datav1.GetObjectRequest]) (*connect.Response[datav1.Object], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.lookup(req.Msg.GetName())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(o.msg), nil
}

func (s *Server) LookupObject(_ context.Context, req *connect.Request[datav1.LookupObjectRequest]) (*connect.Response[datav1.Object], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, o := range s.objects {
		if strings.HasPrefix(o.msg.GetName(), req.Msg.GetParent()+"/objects/") && o.msg.GetKey() == req.Msg.GetKey() &&
			o.msg.GetState() == datav1.ObjectState_OBJECT_STATE_AVAILABLE {
			return connect.NewResponse(o.msg), nil
		}
	}
	return nil, notFound(req.Msg.GetParent() + " key " + req.Msg.GetKey())
}

func (s *Server) ListObjects(_ context.Context, req *connect.Request[datav1.ListObjectsRequest]) (*connect.Response[datav1.ListObjectsResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var all []*datav1.Object
	for _, o := range s.objects {
		if strings.HasPrefix(o.msg.GetName(), req.Msg.GetParent()+"/objects/") && o.msg.GetState() == datav1.ObjectState_OBJECT_STATE_AVAILABLE {
			all = append(all, o.msg)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].GetKey() < all[j].GetKey() })
	start := 0
	if token := req.Msg.GetPage().GetPageToken(); token != "" {
		n, err := strconv.Atoi(token)
		if err != nil || n < 0 || n > len(all) {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("bad page token"))
		}
		start = n
	}
	size := int(req.Msg.GetPage().GetPageSize())
	if size <= 0 {
		size = DefaultPageSize
	}
	end := min(start+size, len(all))
	resp := &datav1.ListObjectsResponse{Objects: all[start:end], Page: &commonv1.PageResponse{}}
	if end < len(all) {
		resp.Page.NextPageToken = strconv.Itoa(end)
	}
	return connect.NewResponse(resp), nil
}

func (s *Server) DownloadObject(_ context.Context, req *connect.Request[datav1.DownloadObjectRequest]) (*connect.Response[datav1.DownloadObjectResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.lookup(req.Msg.GetName())
	if err != nil {
		return nil, err
	}
	if o.msg.GetState() != datav1.ObjectState_OBJECT_STATE_AVAILABLE {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("the object is not complete"))
	}
	return connect.NewResponse(&datav1.DownloadObjectResponse{
		Object: o.msg, DownloadUrl: s.signed(o.msg.GetObjectId(), http.MethodGet),
	}), nil
}

func (s *Server) DeleteObject(_ context.Context, req *connect.Request[datav1.DeleteObjectRequest]) (*connect.Response[datav1.DeleteObjectResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.lookup(req.Msg.GetName())
	if err != nil {
		return nil, err
	}
	o.msg.State = datav1.ObjectState_OBJECT_STATE_DELETED
	o.body = nil
	return connect.NewResponse(&datav1.DeleteObjectResponse{}), nil
}

// ─── MultipartUploadService ────────────────────────────────────────────────

func (s *Server) InitiateMultipartUpload(_ context.Context, req *connect.Request[datav1.InitiateMultipartUploadRequest]) (*connect.Response[datav1.InitiateMultipartUploadResponse], error) {
	if _, err := paladin.ParseCollectionName(req.Msg.GetParent()); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.newObject(req.Msg.GetParent(), req.Msg.GetKey(), req.Msg.GetContentType())
	id := uuid.NewString()
	s.uploads[id] = &multipart{name: o.msg.GetName(), parts: map[int32][]byte{}}
	return connect.NewResponse(&datav1.InitiateMultipartUploadResponse{
		Object: o.msg, UploadId: id, RecommendedPartSize: PartSize,
	}), nil
}

func (s *Server) PresignPart(_ context.Context, req *connect.Request[datav1.PresignPartRequest]) (*connect.Response[datav1.PresignPartResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.uploads[req.Msg.GetUploadId()]; !ok {
		return nil, notFound("upload " + req.Msg.GetUploadId())
	}
	path := fmt.Sprintf("%s?%s=%d", req.Msg.GetUploadId(), partQuery, req.Msg.GetPartNumber())
	return connect.NewResponse(&datav1.PresignPartResponse{UploadUrl: s.signed(path, http.MethodPut)}), nil
}

func (s *Server) CompleteMultipartUpload(_ context.Context, req *connect.Request[datav1.CompleteMultipartUploadRequest]) (*connect.Response[datav1.Object], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	up, ok := s.uploads[req.Msg.GetUploadId()]
	if !ok {
		return nil, notFound("upload " + req.Msg.GetUploadId())
	}
	var body []byte
	for i, p := range req.Msg.GetParts() {
		data, ok := up.parts[p.GetPartNumber()]
		if !ok || p.GetPartNumber() != int32(i+1) || p.GetEtag() != etagOf(data) { //nolint:gosec // parts ≤ 10000
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("part %d is not the uploaded one", p.GetPartNumber()))
		}
		body = append(body, data...)
	}
	o := s.objects[up.name]
	s.commit(o, body, "")
	delete(s.uploads, req.Msg.GetUploadId())
	return connect.NewResponse(o.msg), nil
}

func (s *Server) AbortMultipartUpload(_ context.Context, req *connect.Request[datav1.AbortMultipartUploadRequest]) (*connect.Response[datav1.AbortMultipartUploadResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if up, ok := s.uploads[req.Msg.GetUploadId()]; ok {
		delete(s.objects, up.name)
		delete(s.uploads, req.Msg.GetUploadId())
	}
	return connect.NewResponse(&datav1.AbortMultipartUploadResponse{}), nil
}

// ─── Storage ────────────────────────────────────────────────────────────────

// storage serves the presigned URLs: PUT /storage/{object-id} and
// PUT /storage/{upload-id}?part=N store, GET /storage/{object-id} reads,
// with Range.
func (s *Server) storage(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, storagePath)
	switch r.Method {
	case http.MethodPut:
		body := new(bytes.Buffer)
		if _, err := body.ReadFrom(r.Body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if part := r.URL.Query().Get(partQuery); part != "" {
			up, ok := s.uploads[id]
			n, err := strconv.ParseInt(part, 10, 32)
			if !ok || err != nil || n < 1 || n > maxParts {
				http.NotFound(w, r)
				return
			}
			up.parts[int32(n)] = body.Bytes()
		} else {
			o, ok := s.objectByID(id)
			if !ok {
				http.NotFound(w, r)
				return
			}
			o.put = body.Bytes()
		}
		w.Header().Set("ETag", etagQuote+etagOf(body.Bytes())+etagQuote)
	case http.MethodGet:
		s.mu.Lock()
		o, ok := s.objectByID(id)
		var content []byte
		if ok {
			content = o.body
		}
		s.mu.Unlock()
		if !ok || content == nil {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(content))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// objectByID finds an object by its id. The caller holds mu.
func (s *Server) objectByID(id string) (*object, bool) {
	for _, o := range s.objects {
		if o.msg.GetObjectId() == id {
			return o, true
		}
	}
	return nil, false
}
