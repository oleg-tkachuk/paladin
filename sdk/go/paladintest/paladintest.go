// Package paladintest is an in-memory Paladin data plane for the tests of a
// program built on the SDK: objects uploaded and downloaded through
// presigned URLs, as against the real server, with no server to run.
//
//	srv := paladintest.New(t)
//	p := srv.Connect()
//	obj, err := paladin.Upload(ctx, p.Data, paladin.UploadInput{Parent: srv.Collection().String(), …}, paladin.UploadOptions{})
//
// It serves ObjectService (upload, complete, get, lookup, list, download,
// delete), MultipartUploadService, ListParts included, PresignService
// (RegenerateUploadUrl, PresignDownload) and StorageBootstrapService; every
// other RPC of every plane answers Unimplemented, as a server that lacks it
// does. Like the server it runs protovalidate on every request, after
// authentication, and checks DeleteObject's resource_version; ListObjects
// refuses a filter or an ordering as Unimplemented rather than ignore it.
// Like the server it binds
// every upload URL to the size and checksum the upload was registered with —
// its storage refuses a PUT that does not carry exactly the signed headers,
// a body of another length or checksum, or an overwrite — records that
// checksum on the object, so Download verifies what it reads, and answers
// Range and If-Match requests. Requests lists the RPCs it received, with
// their headers and messages, and Calls those of one procedure a test picks
// out. FailRPC makes a procedure's next calls fail with a code as the server
// sends it, FailRPCIf only the calls a test picks out, and FailStorage the
// storage requests, for a test of how a program retries and maps errors.
//
// By default it serves every call, whatever credential it carries. With
// WithStrictAuth it checks credentials as the server's data plane does,
// accepting only those it issued — IssueBearerToken, IssueAPIToken,
// IssueCapability — that were not revoked, for the tenant the call names.
package paladintest

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // S3's ETag, which the fake reproduces
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

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
	// dispositionQuery carries a download's Content-Disposition, as S3's
	// presigned GET does.
	dispositionQuery = "response-content-disposition"
	// maxParts is the most parts a multipart upload has, as in S3.
	maxParts = 10000
)

// Presigned-URL lifetimes, the server's defaults (limits.presign).
const (
	// DefaultDownloadTTL is how long a PresignDownload URL lives when the
	// request names no TTL: the server's get_ttl.
	DefaultDownloadTTL = 15 * time.Minute
	// MaxPresignTTL is the longest TTL a request may name: the server's
	// max_ttl. A longer one is refused, not shortened.
	MaxPresignTTL = 168 * time.Hour
)

// Headers a bound upload URL requires, as the real presigner signs them.
const (
	headerContentLength = "Content-Length"
	headerContentType   = "Content-Type"
	headerIfNoneMatch   = "If-None-Match"
	headerIfMatch       = "If-Match"
	// headerContentDisposition is what a GET answers with when its URL
	// carries dispositionQuery.
	headerContentDisposition = "Content-Disposition"
	ifNoneMatchAny           = "*"
)

// checksumHeader is the header each algorithm's value is signed in.
var checksumHeader = map[string]string{
	paladin.ChecksumSHA256: "X-Amz-Checksum-Sha256",
	paladin.ChecksumCRC32C: "X-Amz-Checksum-Crc32c",
	paladin.ChecksumMD5:    "Content-Md5",
}

// algorithmName is the API enum as the checksum algorithm's name.
func algorithmName(a commonv1.ChecksumAlgorithm) string {
	switch a {
	case commonv1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_CRC32C:
		return paladin.ChecksumCRC32C
	case commonv1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_MD5:
		return paladin.ChecksumMD5
	}
	return paladin.ChecksumSHA256
}

// binding is what one URL accepts: the body's exact length, its checksum
// under algo, and, for a whole object, Content-Type and no overwrite.
type binding struct {
	size        int64
	algo        string
	checksum    string
	contentType string
	noOverwrite bool
	// cacheControl is bound into an upload into a public collection.
	cacheControl string
}

// headers are the binding as the required headers a presigned URL carries.
func (b binding) headers() map[string]string {
	h := map[string]string{
		headerContentLength:    strconv.FormatInt(b.size, 10),
		checksumHeader[b.algo]: b.checksum,
	}
	if b.contentType != "" {
		h[headerContentType] = b.contentType
	}
	if b.noOverwrite {
		h[headerIfNoneMatch] = ifNoneMatchAny
	}
	if b.cacheControl != "" {
		h[headerCacheControl] = b.cacheControl
	}
	return h
}

// refuses reports why a PUT breaks the binding, "" when it keeps it: a
// signed header missing or altered, or a body that is not the signed one.
func (b binding) refuses(r *http.Request, body []byte) string {
	for k, v := range b.headers() {
		if k == headerContentLength {
			continue // net/http moves it to r.ContentLength
		}
		if r.Header.Get(k) != v {
			return fmt.Sprintf("signed header %s is %q, want %q", k, r.Header.Get(k), v)
		}
	}
	if r.ContentLength != b.size || int64(len(body)) != b.size {
		return fmt.Sprintf("body is %d bytes, signed for %d", len(body), b.size)
	}
	if got, _ := paladin.Checksum(b.algo, bytes.NewReader(body)); got != b.checksum {
		return fmt.Sprintf("body's %s is %s, signed for %s", b.algo, got, b.checksum)
	}
	return ""
}

// validChecksum refuses a value that is not a digest of algo.
func validChecksum(algo, value string) error {
	if value == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("checksum_value is required"))
	}
	want, _ := paladin.Checksum(algo, bytes.NewReader(nil))
	if len(value) != len(want) {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("checksum_value %q is not a base64 %s", value, algo))
	}
	return nil
}

// Server is the fake. Its methods are safe for concurrent use.
type Server struct {
	paladindatav1connect.UnimplementedObjectServiceHandler
	paladindatav1connect.UnimplementedMultipartUploadServiceHandler
	paladindatav1connect.UnimplementedStorageBootstrapServiceHandler
	paladindatav1connect.UnimplementedPresignServiceHandler

	// URL is the server's base URL, for every plane.
	URL    string
	tenant string

	mu       sync.Mutex
	objects  map[string]*object // by name
	uploads  map[string]*multipart
	buckets  map[string]bool // "backend/bucket"
	bound    map[string]bool // collections EnsureTenantStorage created
	public   map[string]bool // public collections, by name
	requests []Request
	// fault, when set, answers a storage request before the fake does;
	// afterStore answers a PUT the fake has already stored.
	fault      StorageFault
	afterStore StorageFault
	storeOps   []StorageOp
	// rpcFaults are the failures FailRPC and FailRPCIf set, by procedure,
	// oldest first.
	rpcFaults map[string][]*rpcFault
	// strictAuth is WithStrictAuth; credentials are what the Issue methods
	// issued, by token.
	strictAuth  bool
	credentials map[string]*credential
	// replays are the responses memoised per procedure and idempotency key,
	// with the fingerprint of the request each answered — as the server keeps.
	replays map[string]replay
}

// replay is one memoised call.
type replay struct {
	fingerprint []byte
	response    connect.AnyResponse
}

// StorageFault decides a storage request's answer before the fake does: a
// status of 0 lets the fake answer it, anything else is answered with that
// status and body — an expired URL, a busy store, a refused part.
type StorageFault func(r *http.Request) (status int, body string)

// ExpiredBody is what S3 answers, with 403, for a presigned URL past its
// expiry.
const ExpiredBody = "<Error><Code>AccessDenied</Code><Message>Request has expired</Message></Error>"

// StorageOp is one request the fake's storage received.
type StorageOp struct {
	Method string
	// Path is the presigned URL's path and query, e.g. /storage/{id}?part=2.
	Path string
}

// FailStorage sets the fault every storage request passes through first;
// nil clears it.
func (s *Server) FailStorage(f StorageFault) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fault = f
}

// FailStorageAfterStoring sets a fault applied to a PUT after the fake has
// stored its body — an upload that landed but whose answer was lost; nil
// clears it.
func (s *Server) FailStorageAfterStoring(f StorageFault) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.afterStore = f
}

// StorageOps returns the storage requests the fake has received, oldest
// first, faulted ones included.
func (s *Server) StorageOps() []StorageOp {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]StorageOp(nil), s.storeOps...)
}

// servedServices are the services Start mounts; FailRPC takes a procedure
// of any of them, an RPC the fake answers Unimplemented included.
var servedServices = []string{
	paladindatav1connect.ObjectServiceName,
	paladindatav1connect.MultipartUploadServiceName,
	paladindatav1connect.StorageBootstrapServiceName,
	paladindatav1connect.PresignServiceName,
}

// serverReason is the google.rpc.ErrorInfo reason the server attaches to a
// code, from the canonical table in backend/internal/api/apiutil/errmap.go;
// a code missing here — Unavailable, DataLoss, Internal — carries none.
var serverReason = map[connect.Code]commonv1.ErrorReason{
	connect.CodeNotFound:           commonv1.ErrorReason_ERROR_REASON_NOT_FOUND,
	connect.CodeAborted:            commonv1.ErrorReason_ERROR_REASON_VERSION_CONFLICT,
	connect.CodeAlreadyExists:      commonv1.ErrorReason_ERROR_REASON_ALREADY_EXISTS,
	connect.CodeInvalidArgument:    commonv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT,
	connect.CodePermissionDenied:   commonv1.ErrorReason_ERROR_REASON_PERMISSION_DENIED,
	connect.CodeFailedPrecondition: commonv1.ErrorReason_ERROR_REASON_FAILED_PRECONDITION,
	connect.CodeUnauthenticated:    commonv1.ErrorReason_ERROR_REASON_UNAUTHENTICATED,
}

// rpcFault is one failure FailRPC or FailRPCIf set: the code, which calls
// it takes — every one when match is nil — and how many more it answers.
type rpcFault struct {
	code      connect.Code
	match     func(proto.Message) bool
	remaining int
}

// FailRPC makes the next times calls of procedure — e.g.
// paladindatav1connect.ObjectServiceGetObjectProcedure — answer code, as the
// server answers it: with the ErrorInfo reason the server attaches to that
// code, so the SDK's typed errors and Reason read it. Calls after those are
// served as usual. Every failed call is in Requests, so a test counts the
// attempts. A later FailRPC on the same procedure replaces this one; reset
// clears this one's remaining failures, and nothing else.
//
// It panics on a procedure the fake does not serve, a times below 1, or an
// unknown code: each is a mistake in the test, not a scenario.
func (s *Server) FailRPC(procedure string, times int, code connect.Code) (reset func()) {
	return s.failRPC("FailRPC", procedure, nil, times, code)
}

// FailRPCIf is FailRPC for the calls of procedure whose request match
// accepts — an object's name, a collection — so tests sharing one fake fail
// only their own calls; every other call of the procedure is served. Its
// failures stack: each FailRPCIf adds one, and a call is taken by the most
// recent one that matches it. match gets a copy of the request message, runs
// without the fake's lock held, and must not block.
//
// It panics as FailRPC does, and on a nil match.
func (s *Server) FailRPCIf(procedure string, match func(req proto.Message) bool, times int, code connect.Code) (reset func()) {
	if match == nil {
		panic("paladintest: FailRPCIf: match is nil; FailRPC fails every call")
	}
	return s.failRPC("FailRPCIf", procedure, match, times, code)
}

func (s *Server) failRPC(caller, procedure string, match func(proto.Message) bool, times int, code connect.Code) (reset func()) {
	if !served(procedure) {
		panic(fmt.Sprintf("paladintest: %s: %q is not a procedure the fake serves", caller, procedure))
	}
	if times < 1 {
		panic(fmt.Sprintf("paladintest: %s: times is %d, want at least 1", caller, times))
	}
	if code < connect.CodeCanceled || code > connect.CodeUnauthenticated {
		panic(fmt.Sprintf("paladintest: %s: %d is not a Connect error code", caller, code))
	}
	f := &rpcFault{code: code, match: match, remaining: times}
	s.mu.Lock()
	defer s.mu.Unlock()
	faults := s.rpcFaults[procedure]
	if match == nil {
		// A later FailRPC replaces the earlier one, as it always has.
		faults = slices.DeleteFunc(slices.Clone(faults), func(g *rpcFault) bool { return g.match == nil })
	}
	s.rpcFaults[procedure] = append(faults, f)
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.dropRPCFault(procedure, f)
	}
}

// dropRPCFault removes f from procedure's failures. The caller holds mu.
func (s *Server) dropRPCFault(procedure string, f *rpcFault) {
	faults := slices.DeleteFunc(slices.Clone(s.rpcFaults[procedure]), func(g *rpcFault) bool { return g == f })
	if len(faults) == 0 {
		delete(s.rpcFaults, procedure)
		return
	}
	s.rpcFaults[procedure] = faults
}

// served reports whether procedure is an RPC of a service the fake mounts.
func served(procedure string) bool {
	service, method, ok := strings.Cut(strings.TrimPrefix(procedure, "/"), "/")
	if !ok || !slices.Contains(servedServices, service) {
		return false
	}
	_, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(service + "." + method))
	return err == nil
}

// takeRPCFault is the error FailRPC or FailRPCIf set for this call of
// procedure, nil when none takes it, counting the call against the one that
// does. The caller does not hold mu: a match runs without it.
func (s *Server) takeRPCFault(procedure string, msg proto.Message) error {
	s.mu.Lock()
	faults := slices.Clone(s.rpcFaults[procedure])
	s.mu.Unlock()
	for _, f := range slices.Backward(faults) {
		if f.match != nil && !f.match(proto.Clone(msg)) {
			continue
		}
		s.mu.Lock()
		live := slices.Contains(s.rpcFaults[procedure], f)
		if live {
			f.remaining--
			if f.remaining == 0 {
				s.dropRPCFault(procedure, f)
			}
		}
		s.mu.Unlock()
		if live { // another call may have spent it meanwhile
			return failure(procedure, f.code)
		}
	}
	return nil
}

// failure is the error an injected failure answers with: code, and the
// reason the server attaches to it.
func failure(procedure string, code connect.Code) error {
	return withReason(connect.NewError(code, fmt.Errorf("paladintest: %s failed by FailRPC", procedure)))
}

// withReason attaches the ErrorInfo reason the server attaches to err's code.
func withReason(err *connect.Error) *connect.Error {
	if reason, ok := serverReason[err.Code()]; ok {
		detail, derr := connect.NewErrorDetail(&errdetails.ErrorInfo{Reason: reason.String(), Domain: paladin.ErrorDomain})
		if derr != nil {
			panic(fmt.Sprintf("paladintest: %v", derr)) // an ErrorInfo always marshals
		}
		err.AddDetail(detail)
	}
	return err
}

// Request is one RPC the fake received.
type Request struct {
	// Procedure is the RPC, /paladin.data.v1.ObjectService/GetObject.
	Procedure string
	// Header is what the client sent: credentials, the idempotency key, the
	// User-Agent.
	Header http.Header
	// Message is the request message, e.g. a *datav1.GetObjectRequest: a
	// copy, so changing it changes nothing in the fake.
	Message proto.Message
}

// clone is a deep copy of the request.
func (r Request) clone() Request {
	return Request{Procedure: r.Procedure, Header: r.Header.Clone(), Message: proto.Clone(r.Message)}
}

type object struct {
	msg  *datav1.Object
	body []byte
	// put is the body storage holds before CompleteObject: what a PUT sent.
	put []byte
	// bound is what the object's upload URL accepts.
	bound binding
	// cacheControl is what a public object is stored with; "" elsewhere.
	cacheControl string
}

type multipart struct {
	name     string
	size     int64
	algo     string
	parts    map[int32][]byte
	bindings map[int32]binding
}

// New starts a fake for the test, stopped when it ends.
func New(t testing.TB, opts ...Option) *Server {
	t.Helper()
	s, stop := Start(opts...)
	t.Cleanup(stop)
	return s
}

// Start starts a fake outside a test — an example, a local tool — and
// returns it with the function that stops it.
func Start(opts ...Option) (*Server, func()) {
	s := &Server{
		tenant:  uuid.NewString(),
		objects: map[string]*object{},
		uploads: map[string]*multipart{},
		buckets: map[string]bool{},
		bound:   map[string]bool{},
		public:  map[string]bool{},

		rpcFaults: map[string][]*rpcFault{},

		credentials: map[string]*credential{},
		replays:     map[string]replay{},
	}
	for _, opt := range opts {
		opt(s)
	}
	validator, err := protovalidate.New()
	if err != nil {
		panic(fmt.Sprintf("paladintest: protovalidate: %v", err)) // the contract's own rules always compile
	}
	record := connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			procedure := req.Spec().Procedure
			msg := cloneMessage(req)
			s.mu.Lock()
			s.requests = append(s.requests, Request{Procedure: procedure, Header: req.Header().Clone(), Message: msg})
			s.mu.Unlock()
			if s.strictAuth {
				if err := s.authenticate(req.Header(), msg); err != nil {
					return nil, err
				}
			}
			// After authentication, as the server's interceptor chain runs it.
			if err := validator.Validate(msg); err != nil {
				return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("validation failed: %w", err))
			}
			if err := s.takeRPCFault(procedure, msg); err != nil {
				return nil, err
			}
			return s.memoise(ctx, req, msg, next)
		}
	}))
	mux := http.NewServeMux()
	mux.Handle(paladindatav1connect.NewObjectServiceHandler(s, record))
	mux.Handle(paladindatav1connect.NewMultipartUploadServiceHandler(s, record))
	mux.Handle(paladindatav1connect.NewStorageBootstrapServiceHandler(s, record))
	mux.Handle(paladindatav1connect.NewPresignServiceHandler(s, record))
	mux.Handle(storagePath, http.HandlerFunc(s.storage))
	mux.Handle(publicPath, http.HandlerFunc(s.servePublic))
	srv := httptest.NewServer(mux)
	s.URL = srv.URL
	return s, srv.Close
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
	key, err := s.publicKey(collection.String(), key)
	if err != nil {
		panic(fmt.Sprintf("paladintest: Put into %s: %v (pass an empty key)", collection, err))
	}
	o := s.newObject(collection.String(), key, contentType)
	s.commit(o, body, "")
	return o.msg
}

// Requests returns the RPCs the fake has received, oldest first; an RPC it
// does not serve is not among them.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Request, len(s.requests))
	for i, r := range s.requests {
		out[i] = r.clone()
	}
	return out
}

// Calls returns the requests for procedure whose message match accepts,
// oldest first; a nil match accepts every one. A test that shares the fake
// with others counts its own calls by what they named:
//
//	srv.Calls(paladindatav1connect.ObjectServiceGetObjectProcedure, func(m proto.Message) bool {
//		return m.(*datav1.GetObjectRequest).GetName() == obj.GetName()
//	})
//
// match gets a copy of each message, and runs without the fake's lock held.
func (s *Server) Calls(procedure string, match func(proto.Message) bool) []Request {
	var out []Request
	for _, r := range s.Requests() {
		if r.Procedure == procedure && (match == nil || match(r.Message)) {
			out = append(out, r)
		}
	}
	return out
}

// cloneMessage is a copy of req's message, nil for one that is not a
// protobuf message.
func cloneMessage(req connect.AnyRequest) proto.Message {
	m, ok := req.Any().(proto.Message)
	if !ok {
		return nil
	}
	return proto.Clone(m)
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
		ResourceVersion: "1",
	}}
	if c, err := paladin.ParseCollectionName(parent); err == nil {
		o.msg.Collection = c.Collection
	}
	s.publish(o, parent)
	s.objects[o.msg.GetName()] = o
	return o
}

// bump advances the object's resource_version, as the server's trigger does
// on every change to its row.
func bump(o *object) {
	n, err := strconv.ParseInt(o.msg.GetResourceVersion(), 10, 64)
	if err != nil {
		panic(fmt.Sprintf("paladintest: resource_version %q", o.msg.GetResourceVersion())) // the fake only writes integers
	}
	o.msg.ResourceVersion = strconv.FormatInt(n+1, 10)
}

// checkVersion refuses a resource_version that is not the object's, as the
// server does: not a number is InvalidArgument, another one Aborted.
func checkVersion(o *object, version string) error {
	if _, err := strconv.ParseInt(version, 10, 64); err != nil {
		return withReason(connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid resource_version: %w", err)))
	}
	if version != o.msg.GetResourceVersion() {
		return withReason(connect.NewError(connect.CodeAborted, errors.New("resource_version mismatch")))
	}
	return nil
}

// claim makes the object an upload registers, refusing a key another object
// holds — in any state, the trash included — as the server's unique path
// does.
func (s *Server) claim(parent, key, contentType string, metadata, tags map[string]string) (*object, error) {
	key, err := s.publicKey(parent, key)
	if err != nil {
		return nil, err
	}
	if key != "" {
		for _, o := range s.objects {
			if strings.HasPrefix(o.msg.GetName(), parent+"/objects/") && o.msg.GetKey() == key {
				return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("an object is already at %q", key))
			}
		}
	}
	o := s.newObject(parent, key, contentType)
	o.msg.Metadata, o.msg.Tags = maps.Clone(metadata), maps.Clone(tags)
	return o, nil
}

// MarkFailed fails a PENDING object, as the server's reconciler does when its
// upload URL expired with no bytes stored.
func (s *Server) MarkFailed(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o, ok := s.objects[name]; ok && o.msg.GetState() == datav1.ObjectState_OBJECT_STATE_PENDING {
		o.msg.State = datav1.ObjectState_OBJECT_STATE_FAILED
		bump(o)
	}
}

func (s *Server) commit(o *object, body []byte, checksum string) {
	s.commitAs(o, body, paladin.ChecksumSHA256, checksum)
}

func (s *Server) commitAs(o *object, body []byte, algo, checksum string) {
	o.body = body
	o.msg.Etag = etagOf(body)
	o.msg.SizeBytes = int64(len(body))
	o.msg.State = datav1.ObjectState_OBJECT_STATE_AVAILABLE
	bump(o)
	if checksum != "" {
		o.msg.Checksum = &datav1.ChecksumDigest{Algorithm: algo, Value: checksum}
	}
}

// partDigests is each algorithm's digest, as the server computes a composite.
var partDigests = map[string]func() hash.Hash{
	paladin.ChecksumSHA256: sha256.New,
	paladin.ChecksumCRC32C: func() hash.Hash { return crc32.New(crc32.MakeTable(crc32.Castagnoli)) },
	paladin.ChecksumMD5:    md5.New,
}

// compositeChecksum is the server's composite of part checksums: the base64
// of algo's digest of their raw digests, in part order, then "-" and the
// count. The parts were checked when they were presigned.
func compositeChecksum(algo string, parts []string) string {
	h := partDigests[algo]()
	for _, part := range parts {
		raw, err := base64.StdEncoding.DecodeString(part)
		if err != nil {
			panic(fmt.Sprintf("paladintest: part checksum %q: %v", part, err)) // validChecksum admitted it
		}
		h.Write(raw)
	}
	return base64.StdEncoding.EncodeToString(h.Sum(nil)) + "-" + strconv.Itoa(len(parts))
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
	algo := algorithmName(req.Msg.GetChecksumAlgorithm())
	if err := validChecksum(algo, req.Msg.GetChecksumValue()); err != nil {
		return nil, err
	}
	if req.Msg.GetSizeHintBytes() < 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("size must not be negative"))
	}
	o, err := s.claim(req.Msg.GetParent(), req.Msg.GetKey(), req.Msg.GetContentType(), req.Msg.GetMetadata(), req.Msg.GetTags())
	if err != nil {
		return nil, err
	}
	o.bound = binding{
		size: req.Msg.GetSizeHintBytes(), algo: algo, checksum: req.Msg.GetChecksumValue(),
		contentType: req.Msg.GetContentType(), noOverwrite: true, cacheControl: o.cacheControl,
	}
	url := s.signed(o.msg.GetObjectId(), http.MethodPut)
	url.RequiredHeaders = o.bound.headers()
	return connect.NewResponse(&datav1.UploadObjectResponse{Object: o.msg, UploadUrl: url}), nil
}

func (s *Server) CompleteObject(_ context.Context, req *connect.Request[datav1.CompleteObjectRequest]) (*connect.Response[datav1.Object], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objects[req.Msg.GetName()]
	if !ok {
		return nil, notFound(req.Msg.GetName())
	}
	// As the server: completing a completed object returns it.
	if o.msg.GetState() == datav1.ObjectState_OBJECT_STATE_AVAILABLE {
		return connect.NewResponse(o.msg), nil
	}
	if o.put == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("nothing was uploaded"))
	}
	// The ETag is optional; one that is given must be the content's.
	if etag := req.Msg.GetEtag(); etag != "" && etag != etagOf(o.put) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("the ETag is not the uploaded content's"))
	}
	if sum := req.Msg.GetChecksumValue(); sum != "" && sum != o.bound.checksum {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("checksum_value differs from the registered checksum"))
	}
	s.commitAs(o, o.put, o.bound.algo, o.bound.checksum)
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
			o.msg.GetState() != datav1.ObjectState_OBJECT_STATE_DELETED {
			return connect.NewResponse(o.msg), nil
		}
	}
	return nil, notFound(req.Msg.GetParent() + " key " + req.Msg.GetKey())
}

func (s *Server) ListObjects(_ context.Context, req *connect.Request[datav1.ListObjectsRequest]) (*connect.Response[datav1.ListObjectsResponse], error) {
	if field := unhonouredListField(req.Msg); field != "" {
		return nil, connect.NewError(connect.CodeUnimplemented,
			fmt.Errorf("paladintest: ListObjects does not apply %s; the server does, so a test would pass on what it would not", field))
	}
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

// unhonouredListField names a ListObjects parameter the fake does not apply,
// or "" when the request sets none.
func unhonouredListField(req *datav1.ListObjectsRequest) string {
	switch {
	case req.GetFilter() != "":
		return "filter"
	case req.GetOrderBy() != "":
		return "order_by"
	case req.GetSortOrder() != commonv1.SortOrder_SORT_ORDER_UNSPECIFIED:
		return "sort_order"
	}
	return ""
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
	url := s.signed(o.msg.GetObjectId(), http.MethodGet)
	if req.Msg.GetRequireEtagMatch() {
		url.RequiredHeaders = map[string]string{headerIfMatch: etagQuote + o.msg.GetEtag() + etagQuote}
	}
	return connect.NewResponse(&datav1.DownloadObjectResponse{Object: o.msg, DownloadUrl: url}), nil
}

func (s *Server) DeleteObject(_ context.Context, req *connect.Request[datav1.DeleteObjectRequest]) (*connect.Response[datav1.DeleteObjectResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.Msg.GetPermanent() {
		// As the server: one in the trash is purged too, and the path is free.
		o, ok := s.objects[req.Msg.GetName()]
		if !ok {
			return nil, notFound(req.Msg.GetName())
		}
		if err := checkVersion(o, req.Msg.GetResourceVersion()); err != nil {
			return nil, err
		}
		delete(s.objects, req.Msg.GetName())
		return connect.NewResponse(&datav1.DeleteObjectResponse{}), nil
	}
	o, err := s.lookup(req.Msg.GetName())
	if err != nil {
		return nil, err
	}
	if err := checkVersion(o, req.Msg.GetResourceVersion()); err != nil {
		return nil, err
	}
	if err := refuseTrash(o); err != nil {
		return nil, err
	}
	o.msg.State = datav1.ObjectState_OBJECT_STATE_DELETED // in the trash, still holding its key
	bump(o)
	o.body = nil
	return connect.NewResponse(&datav1.DeleteObjectResponse{}), nil
}

// ─── PresignService ─────────────────────────────────────────────────────────

// RegenerateUploadUrl presigns a PENDING object's upload URL again, bound as
// the first was.
func (s *Server) RegenerateUploadUrl(_ context.Context, req *connect.Request[datav1.RegenerateUploadUrlRequest]) (*connect.Response[datav1.RegenerateUploadUrlResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.lookup(req.Msg.GetName())
	if err != nil {
		return nil, err
	}
	if o.msg.GetState() != datav1.ObjectState_OBJECT_STATE_PENDING {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("the object is not pending"))
	}
	url := s.signed(o.msg.GetObjectId(), http.MethodPut)
	url.RequiredHeaders = o.bound.headers()
	return connect.NewResponse(&datav1.RegenerateUploadUrlResponse{UploadUrl: url}), nil
}

// PresignDownload presigns a GET of an AVAILABLE object on the fake's
// storage, as the server does: an absent TTL is DefaultDownloadTTL, one that
// is negative or above MaxPresignTTL is InvalidArgument, a name that is not
// an object's is InvalidArgument, an unknown object NotFound, and one that
// is not AVAILABLE FailedPrecondition.
func (s *Server) PresignDownload(_ context.Context, req *connect.Request[datav1.PresignDownloadRequest]) (*connect.Response[datav1.PresignDownloadResponse], error) {
	if _, err := paladin.ParseObjectName(req.Msg.GetName()); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	ttl := req.Msg.GetTtl().AsDuration()
	if ttl < 0 || ttl > MaxPresignTTL {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("ttl %s is outside 0..%s", ttl, MaxPresignTTL))
	}
	if ttl == 0 {
		ttl = DefaultDownloadTTL
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o, err := s.lookup(req.Msg.GetName())
	if err != nil {
		return nil, err
	}
	if state := o.msg.GetState(); state != datav1.ObjectState_OBJECT_STATE_AVAILABLE {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("object state %s does not allow GET", state))
	}
	path := o.msg.GetObjectId()
	if d := req.Msg.GetContentDisposition(); d != "" {
		path += "?" + url.Values{dispositionQuery: {d}}.Encode()
	}
	signed := s.signed(path, http.MethodGet)
	signed.ExpiresAtRfc3339 = time.Now().Add(ttl).UTC().Format(time.RFC3339)
	if req.Msg.GetRequireEtagMatch() {
		signed.RequiredHeaders = map[string]string{headerIfMatch: etagQuote + o.msg.GetEtag() + etagQuote}
	}
	return connect.NewResponse(&datav1.PresignDownloadResponse{DownloadUrl: signed}), nil
}

// ─── MultipartUploadService ────────────────────────────────────────────────

func (s *Server) InitiateMultipartUpload(_ context.Context, req *connect.Request[datav1.InitiateMultipartUploadRequest]) (*connect.Response[datav1.InitiateMultipartUploadResponse], error) {
	if _, err := paladin.ParseCollectionName(req.Msg.GetParent()); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.Msg.GetSizeBytes() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("size_bytes must be positive"))
	}
	o, err := s.claim(req.Msg.GetParent(), req.Msg.GetKey(), req.Msg.GetContentType(), req.Msg.GetMetadata(), req.Msg.GetTags())
	if err != nil {
		return nil, err
	}
	id := uuid.NewString()
	s.uploads[id] = &multipart{
		name: o.msg.GetName(), size: req.Msg.GetSizeBytes(), algo: algorithmName(req.Msg.GetChecksumAlgorithm()),
		parts: map[int32][]byte{}, bindings: map[int32]binding{},
	}
	total := (req.Msg.GetSizeBytes() + PartSize - 1) / PartSize
	return connect.NewResponse(&datav1.InitiateMultipartUploadResponse{
		Object: o.msg, UploadId: id, RecommendedPartSize: PartSize, TotalParts: int32(total), //nolint:gosec // bounded by the size
	}), nil
}

func (s *Server) PresignPart(_ context.Context, req *connect.Request[datav1.PresignPartRequest]) (*connect.Response[datav1.PresignPartResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	up, ok := s.uploads[req.Msg.GetUploadId()]
	if !ok {
		return nil, notFound("upload " + req.Msg.GetUploadId())
	}
	if err := validChecksum(up.algo, req.Msg.GetChecksumValue()); err != nil {
		return nil, err
	}
	n := req.Msg.GetPartNumber()
	total := int32((up.size + PartSize - 1) / PartSize) //nolint:gosec // bounded by the size
	if n < 1 || n > total {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("part %d out of range 1..%d", n, total))
	}
	length := int64(PartSize)
	if n == total {
		length = up.size - int64(total-1)*PartSize
	}
	b := binding{size: length, algo: up.algo, checksum: req.Msg.GetChecksumValue()}
	up.bindings[n] = b
	url := s.signed(fmt.Sprintf("%s?%s=%d", req.Msg.GetUploadId(), partQuery, n), http.MethodPut)
	url.RequiredHeaders = b.headers()
	return connect.NewResponse(&datav1.PresignPartResponse{UploadUrl: url}), nil
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
		if !ok || p.GetPartNumber() != int32(i+1) || p.GetEtag() != etagOf(data) || //nolint:gosec // parts ≤ 10000
			p.GetChecksumValue() != up.bindings[p.GetPartNumber()].checksum {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("part %d is not the uploaded one", p.GetPartNumber()))
		}
		body = append(body, data...)
	}
	o := s.objects[up.name]
	// As the server: the composite of the parts' checksums, over the part
	// size the upload was told to use.
	checksums := make([]string, 0, len(req.Msg.GetParts()))
	for _, p := range req.Msg.GetParts() {
		checksums = append(checksums, p.GetChecksumValue())
	}
	s.commitAs(o, body, up.algo, compositeChecksum(up.algo, checksums))
	o.msg.Checksum.PartSizeBytes = PartSize
	delete(s.uploads, req.Msg.GetUploadId())
	return connect.NewResponse(o.msg), nil
}

func (s *Server) ListParts(_ context.Context, req *connect.Request[datav1.ListPartsRequest]) (*connect.Response[datav1.ListPartsResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	up, ok := s.uploads[req.Msg.GetUploadId()]
	if !ok {
		return nil, notFound("upload " + req.Msg.GetUploadId())
	}
	resp := &datav1.ListPartsResponse{Page: &commonv1.PageResponse{}}
	for n, data := range up.parts {
		resp.Parts = append(resp.Parts, &datav1.PartInfo{PartNumber: n, SizeBytes: int64(len(data)), Etag: etagOf(data)})
	}
	sort.Slice(resp.Parts, func(i, j int) bool { return resp.Parts[i].GetPartNumber() < resp.Parts[j].GetPartNumber() })
	return connect.NewResponse(resp), nil
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

// ─── StorageBootstrapService ────────────────────────────────────────────────

// Bounds of a bucket name, as the contract sets them.
const (
	minBucketLen = 3
	maxBucketLen = 63
)

// EnsureTenantStorage records the bucket and the collections, and reports
// which this call created. Any backend id is taken to exist. Every collection
// already works for objects, bootstrapped or not.
func (s *Server) EnsureTenantStorage(_ context.Context, req *connect.Request[datav1.EnsureTenantStorageRequest]) (*connect.Response[datav1.EnsureTenantStorageResponse], error) {
	backend, bucket := req.Msg.GetBackendId(), req.Msg.GetBucket()
	if backend == "" || len(bucket) < minBucketLen || len(bucket) > maxBucketLen {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("a backend id and a bucket of 3 to 63 characters are required"))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	resp := &datav1.EnsureTenantStorageResponse{}
	if key := backend + "/" + bucket; !s.buckets[key] {
		s.buckets[key] = true
		resp.BucketCreated = true
	}
	for _, c := range req.Msg.GetCollections() {
		if s.bound[c] {
			resp.CollectionsExisting = append(resp.CollectionsExisting, c)
			continue
		}
		s.bound[c] = true
		resp.CollectionsCreated = append(resp.CollectionsCreated, c)
	}
	return connect.NewResponse(resp), nil
}

// ─── Storage ────────────────────────────────────────────────────────────────

// storage serves the presigned URLs: PUT /storage/{object-id} and
// PUT /storage/{upload-id}?part=N store, GET /storage/{object-id} reads,
// with Range, and answers the Content-Disposition its URL carries.
func (s *Server) storage(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, storagePath)
	s.mu.Lock()
	s.storeOps = append(s.storeOps, StorageOp{Method: r.Method, Path: r.URL.RequestURI()})
	fault := s.fault
	s.mu.Unlock()
	if fault != nil {
		if status, body := fault(r); status != 0 {
			http.Error(w, body, status)
			return
		}
	}
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
			b, signed := up.bindings[int32(n)]
			if !signed {
				http.Error(w, "part was never presigned", http.StatusForbidden)
				return
			}
			if why := b.refuses(r, body.Bytes()); why != "" {
				http.Error(w, why, http.StatusBadRequest)
				return
			}
			up.parts[int32(n)] = body.Bytes()
		} else {
			o, ok := s.objectByID(id)
			if !ok {
				http.NotFound(w, r)
				return
			}
			// If-None-Match: * — the key already holds a stored object.
			if o.put != nil || o.msg.GetState() == datav1.ObjectState_OBJECT_STATE_AVAILABLE {
				http.Error(w, "an object is already at the key", http.StatusPreconditionFailed)
				return
			}
			if why := o.bound.refuses(r, body.Bytes()); why != "" {
				http.Error(w, why, http.StatusBadRequest)
				return
			}
			o.put = body.Bytes()
		}
		if s.afterStore != nil {
			if status, msg := s.afterStore(r); status != 0 {
				http.Error(w, msg, status)
				return
			}
		}
		w.Header().Set("ETag", etagQuote+etagOf(body.Bytes())+etagQuote)
	case http.MethodGet:
		s.mu.Lock()
		o, ok := s.objectByID(id)
		var content []byte
		var etag string
		if ok {
			content, etag = o.body, o.msg.GetEtag()
		}
		s.mu.Unlock()
		if !ok || content == nil {
			http.NotFound(w, r)
			return
		}
		// http.ServeContent compares If-Match with the ETag header it is given.
		w.Header().Set("ETag", etagQuote+etag+etagQuote)
		if d := r.URL.Query().Get(dispositionQuery); d != "" {
			w.Header().Set(headerContentDisposition, d)
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
