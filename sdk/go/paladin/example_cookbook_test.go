package paladin_test

// The cookbook: the integrations a consumer would otherwise write by hand,
// as examples `go test` compiles and runs — against paladintest, the
// in-memory data plane, where a server is needed.

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

// A storage host the presigned URLs are signed for, which only the outside
// world resolves; inside the cluster storage answers at another address.
func Example_splitHorizonPresign() {
	transfer, err := paladin.NewTransfer(
		paladin.WithSplitHorizon("https://s3.example.com", "http://seaweedfs-s3.storage.svc:8333"),
	)
	if err != nil {
		panic(err)
	}
	p, err := paladin.Connect(paladin.Endpoints{Data: "https://paladin-data.example.com"},
		paladin.WithTokens(paladin.StaticToken("paladin_pat_…")),
		paladin.WithTransfer(transfer))
	if err != nil {
		panic(err)
	}
	// Every Upload and Download through p now reaches storage in-cluster,
	// with the Host header the URL was signed for.
	_ = p
}

// Mutual TLS with a workload identity whose certificates rotate on disk, and
// the server checked by its SPIFFE ID.
func Example_mutualTLSWithSPIFFE() {
	identity := paladin.TLS{
		CAFile:   "/var/run/secrets/spiffe/bundle.pem",
		CertFile: "/var/run/secrets/spiffe/svid.pem",
		KeyFile:  "/var/run/secrets/spiffe/svid-key.pem",
		ServerID: "spiffe://cluster.local/ns/paladin/sa/paladin-core",
		// Anything more the server's certificate must satisfy.
		VerifyPeer: func(leaf *x509.Certificate) error {
			if time.Until(leaf.NotAfter) <= 0 {
				return errors.New("expired")
			}
			return nil
		},
		ReloadInterval: 30 * time.Second,
	}
	// The files are read here, so a missing one fails at start-up.
	_, err := paladin.Connect(paladin.Endpoints{Data: "https://paladin-data.paladin.svc:8443"},
		paladin.WithTLS(identity))
	fmt.Println(err != nil)
	// Output: true
}

// settingsStore stands for wherever operators rotate the token — a database
// row, a secret in a vault — read on every call.
type settingsStore struct {
	mu    sync.Mutex
	token string
}

func (s *settingsStore) Get() string { s.mu.Lock(); defer s.mu.Unlock(); return s.token }

// storeToken reads the token from the store on every call, so a rotation
// takes effect on the next call with no new client.
type storeToken struct{ store *settingsStore }

func (t storeToken) Token(context.Context, string) (string, error) {
	if token := t.store.Get(); token != "" {
		return token, nil
	}
	return "", paladin.ErrNoToken
}

// A token an operator rotates through a UI, read from its store per call.
func Example_rotatingToken() {
	store := &settingsStore{token: "paladin_pat_first"}
	p, err := paladin.Connect(paladin.Endpoints{Data: "https://paladin-data.example.com"},
		paladin.WithTokens(storeToken{store}))
	if err != nil {
		panic(err)
	}
	_ = p
	store.mu.Lock()
	store.token = "paladin_pat_second" // the next call sends this one
	store.mu.Unlock()
	fmt.Println(storeToken{store}.store.Get())
	// Output: paladin_pat_second
}

// Many documents in, a few at a time, each failure reported on its own.
func Example_bulkIngestion() {
	srv, stop := paladintest.Start()
	defer stop()
	p := srv.Connect()
	ctx := context.Background()

	var names []string
	for i := range 5 {
		body := []byte(fmt.Sprintf("document %d", i))
		obj, err := paladin.Upload(ctx, p.Data, paladin.UploadInput{
			Parent: srv.Collection().String(), Key: fmt.Sprintf("docs/%d.txt", i),
			ContentType: "text/plain", Size: int64(len(body)), Body: bytes.NewReader(body),
		}, paladin.UploadOptions{})
		if err != nil {
			panic(err)
		}
		names = append(names, obj.GetName())
	}
	var total sync.Map
	failures := paladin.DownloadMany(ctx, p.Data, names, 2, func(name string, r *paladin.ObjectReader) error {
		n, err := io.Copy(io.Discard, r) // hand r to a parser instead
		total.Store(name, n)
		return err
	})
	fmt.Println(len(failures))
	// Output: 0
}

// A multipart upload interrupted part-way, resumed from the parts storage
// already holds: ListParts says which, and only the rest are sent.
func Example_resumableMultipart() {
	srv, stop := paladintest.Start()
	defer stop()
	p := srv.Connect()
	ctx := context.Background()
	body := bytes.Repeat([]byte("x"), 2*paladintest.PartSize+10)

	init, err := p.Data.MultipartUpload.InitiateMultipartUpload(ctx, connect.NewRequest(&datav1.InitiateMultipartUploadRequest{
		Parent: srv.Collection().String(), Key: "big.bin", SizeBytes: int64(len(body)),
		ChecksumAlgorithm: commonv1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256,
	}))
	if err != nil {
		panic(err)
	}
	name, uploadID, size := init.Msg.GetObject().GetName(), init.Msg.GetUploadId(), init.Msg.GetRecommendedPartSize()
	part := func(n int32) []byte {
		start := int64(n-1) * size
		return body[start:min(start+size, int64(len(body)))]
	}
	// Each part's URL is signed for its checksum, and completion lists it
	// again; both come from the part's own bytes.
	sum := func(n int32) string {
		s, err := paladin.Checksum(paladin.ChecksumSHA256, bytes.NewReader(part(n)))
		if err != nil {
			panic(err)
		}
		return s
	}
	send := func(n int32) {
		signed, err := p.Data.MultipartUpload.PresignPart(ctx, connect.NewRequest(&datav1.PresignPartRequest{
			ObjectName: name, UploadId: uploadID, PartNumber: n, ChecksumValue: sum(n),
		}))
		if err != nil {
			panic(err)
		}
		if _, err := p.Data.Transfer().Put(ctx, signed.Msg.GetUploadUrl(), bytes.NewReader(part(n)), int64(len(part(n)))); err != nil {
			panic(err)
		}
	}
	send(1) // … and then the process died.

	// On restart, with name and uploadID kept from before:
	listed, err := p.Data.MultipartUpload.ListParts(ctx, connect.NewRequest(&datav1.ListPartsRequest{ObjectName: name, UploadId: uploadID}))
	if err != nil {
		panic(err)
	}
	have := map[int32]string{}
	for _, pt := range listed.Msg.GetParts() {
		have[pt.GetPartNumber()] = pt.GetEtag()
	}
	count := int32((int64(len(body)) + size - 1) / size) //nolint:gosec // a few parts
	var parts []*datav1.CompletedPart
	for n := int32(1); n <= count; n++ {
		if _, ok := have[n]; !ok {
			send(n)
		}
	}
	listed, err = p.Data.MultipartUpload.ListParts(ctx, connect.NewRequest(&datav1.ListPartsRequest{ObjectName: name, UploadId: uploadID}))
	if err != nil {
		panic(err)
	}
	for _, pt := range listed.Msg.GetParts() {
		parts = append(parts, &datav1.CompletedPart{PartNumber: pt.GetPartNumber(), Etag: pt.GetEtag(), ChecksumValue: sum(pt.GetPartNumber())})
	}
	done, err := p.Data.MultipartUpload.CompleteMultipartUpload(ctx, connect.NewRequest(&datav1.CompleteMultipartUploadRequest{
		ObjectName: name, UploadId: uploadID, Parts: parts,
	}))
	if err != nil {
		panic(err)
	}
	fmt.Println(len(have), len(parts), done.Msg.GetSizeBytes() == int64(len(body)))
	// Output: 1 3 true
}

// sessionStore keeps open multipart sessions where a restart finds them: a
// table in the application's database, here a map.
type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]paladin.UploadSession
}

func (s *sessionStore) Load(key string) (paladin.UploadSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[key]
	return session, ok
}

func (s *sessionStore) Save(key string, session paladin.UploadSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[key] = session
}

func (s *sessionStore) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, key)
}

// transient reports whether an upload that failed with err may succeed when
// tried again: storage busy or down, an expired URL, Paladin unavailable or
// busy, the network. Anything else — a refusal, a bad request — fails again.
func transient(err error) bool {
	var te *paladin.TransferError
	if errors.As(err, &te) {
		return te.Status >= 500 || te.Status == 429 || paladin.Expired(err)
	}
	switch connect.CodeOf(err) {
	case connect.CodeUnavailable, connect.CodeResourceExhausted, connect.CodeDeadlineExceeded:
		return true
	}
	var ne net.Error
	return errors.As(err, &ne)
}

// contentSHA256 is the metadata key uploadDurably records the content's
// SHA-256 under, so a later attempt knows its own object from another.
const contentSHA256 = "content-sha256"

var (
	// errNoKey: without a key every attempt makes a new object, so a retry
	// after a lost answer would store the content twice.
	errNoKey = errors.New("a durable upload needs a key of its own and a Body it can read again")
	// errKeyTaken: another object, not an earlier attempt at this one, holds
	// the key — or one is in the trash there. Deciding that is not a retry's.
	errKeyTaken = errors.New("the key holds another object")
)

// uploadDurably runs Upload until it completes, at most attempts times. A
// multipart session is saved as soon as it opens, so the next attempt — in
// this process or after a restart — resumes it and sends only the parts
// storage does not hold; it is dropped once the object is complete. Upload
// itself retries each request and completes the object; this adds what it
// leaves to the application: keeping the session, and trying the whole
// upload again.
//
// The key is what keeps a retry from storing the content twice: the server
// holds one object per key, so an attempt after an earlier one registered
// its object is refused with ErrAlreadyExists, and settle finishes or clears
// what that attempt left. It takes one writer per key at a time — a job
// holding a lock on it, say: a PENDING object there is taken for this
// upload's own.
func uploadDurably(ctx context.Context, data *paladin.DataPlane, in paladin.UploadInput,
	store *sessionStore, attempts int,
) (*datav1.Object, int, error) {
	if in.Key == "" || in.Body == nil {
		return nil, 0, errNoKey
	}
	sum, err := paladin.Checksum(paladin.ChecksumSHA256, io.NewSectionReader(in.Body, 0, in.Size))
	if err != nil {
		return nil, 0, err
	}
	in.Metadata = maps.Clone(in.Metadata)
	if in.Metadata == nil {
		in.Metadata = map[string]string{}
	}
	in.Metadata[contentSHA256] = sum
	key := in.Parent + "/" + in.Key
	for attempt := 1; attempt <= attempts; attempt++ {
		opts := paladin.UploadOptions{OnSession: func(s paladin.UploadSession) { store.Save(key, s) }}
		if s, ok := store.Load(key); ok {
			opts.Resume = &s
		}
		var obj *datav1.Object
		if obj, err = paladin.Upload(ctx, data, in, opts); err == nil {
			store.Delete(key)
			return obj, attempt, nil
		}
		switch {
		case errors.Is(err, paladin.ErrAlreadyExists):
			settled, serr := settle(ctx, data, in, sum)
			if settled != nil {
				store.Delete(key)
				return settled, attempt, nil
			}
			if serr != nil {
				if !transient(serr) {
					return nil, attempt, serr
				}
				err = serr
			}
		case opts.Resume != nil && errors.Is(err, paladin.ErrNotFound):
			store.Delete(key) // the server swept the session: start over
		case !transient(err):
			return nil, attempt, err
		}
		select {
		case <-time.After(time.Duration(attempt) * 10 * time.Millisecond): // longer in production
		case <-ctx.Done():
			return nil, attempt, ctx.Err()
		}
	}
	return nil, attempts, err
}

// settle deals with the object at in's key: returned when it is this
// content, complete or completed now; permanently deleted, so the next
// attempt can register the key again, when it is an attempt that never got
// its bytes or failed; errKeyTaken when it is another object.
func settle(ctx context.Context, data *paladin.DataPlane, in paladin.UploadInput, sum string) (*datav1.Object, error) {
	collection, err := paladin.ParseCollectionName(in.Parent)
	if err != nil {
		return nil, err
	}
	existing, err := paladin.LookupObject(ctx, data, paladin.ObjectURI{Collection: collection, Key: in.Key})
	if errors.Is(err, paladin.ErrNotFound) {
		// The key is held, yet no live object answers: one in the trash.
		return nil, fmt.Errorf("%w: one is in the trash at %q", errKeyTaken, in.Key)
	}
	if err != nil {
		return nil, err
	}
	if existing.GetMetadata()[contentSHA256] != sum {
		return nil, fmt.Errorf("%w: %s", errKeyTaken, existing.GetName())
	}
	switch existing.GetState() {
	case datav1.ObjectState_OBJECT_STATE_AVAILABLE:
		return existing, nil // the earlier attempt completed; only its answer was lost
	case datav1.ObjectState_OBJECT_STATE_PENDING:
		// Its bytes may have landed with the answer lost: the server HEADs
		// storage and completes it if so.
		done, err := data.Object.CompleteObject(ctx, connect.NewRequest(&datav1.CompleteObjectRequest{
			Name: existing.GetName(), ChecksumValue: sum,
		}))
		if err == nil {
			return done.Msg, nil
		}
		if !errors.Is(err, paladin.ErrFailedPrecondition) {
			return nil, err
		}
	case datav1.ObjectState_OBJECT_STATE_FAILED:
	default:
		return nil, fmt.Errorf("%w: %s is %s", errKeyTaken, existing.GetName(), existing.GetState())
	}
	// Permanently: an object in the trash keeps its key.
	_, err = data.Object.DeleteObject(ctx, connect.NewRequest(&datav1.DeleteObjectRequest{
		Name: existing.GetName(), ResourceVersion: existing.GetResourceVersion(), Permanent: true,
	}))
	return nil, err
}

// An upload kept until it completes: a failure part-way is tried again,
// resuming the multipart session where it stopped.
func Example_durableUpload() {
	srv, stop := paladintest.Start()
	defer stop()
	transfer, err := paladin.NewTransfer(paladin.WithTransferAttempts(1)) // so one refusal fails the upload
	if err != nil {
		panic(err)
	}
	p := srv.Connect(paladin.WithTransfer(transfer))
	ctx := context.Background()
	body := bytes.Repeat([]byte("x"), 2*paladintest.PartSize+10)

	// Storage is busy for one part of the first attempt.
	var puts atomic.Int32
	srv.FailStorage(func(r *http.Request) (int, string) {
		if r.Method == http.MethodPut && puts.Add(1) == 2 {
			return http.StatusServiceUnavailable, "busy"
		}
		return 0, ""
	})
	store := &sessionStore{sessions: map[string]paladin.UploadSession{}}
	obj, attempts, err := uploadDurably(ctx, p.Data, paladin.UploadInput{
		Parent: srv.Collection().String(), Key: "big.bin", ContentType: "application/octet-stream",
		Size: int64(len(body)), Body: bytes.NewReader(body),
	}, store, 5)
	_, open := store.Load(srv.Collection().String() + "/big.bin")
	fmt.Println(attempts, obj.GetSizeBytes() == int64(len(body)), open, err)
	// Output: 2 true false <nil>
}

// A large object streamed into a consumer, never held whole, and verified
// against its recorded checksum at the end.
func Example_streamingLargeObjects() {
	srv, stop := paladintest.Start()
	defer stop()
	p := srv.Connect()
	ctx := context.Background()
	big := strings.NewReader(strings.Repeat("a large PDF ", 1<<16))
	obj, err := paladin.Upload(ctx, p.Data, paladin.UploadInput{
		Parent: srv.Collection().String(), Key: "big.pdf", ContentType: "application/pdf",
		Size: big.Size(), Stream: big, // read once, front to back
	}, paladin.UploadOptions{})
	if err != nil {
		panic(err)
	}
	r, err := paladin.DownloadURI(ctx, p.Data,
		paladin.ObjectURI{Collection: srv.Collection(), Key: "big.pdf"}.String(), paladin.DownloadOptions{})
	if err != nil {
		panic(err)
	}
	defer func() { _ = r.Close() }()
	n, err := io.Copy(io.Discard, r) // a parser reading r; an IntegrityError at the end means corrupt content
	fmt.Println(n == obj.GetSizeBytes(), err)
	// Output: true <nil>
}

// From a hand-written Connect-JSON client: a POST of JSON to
// /<package>.<Service>/<Method> becomes the generated client's method; the
// headers it set by hand — Authorization, Idempotency-Key, User-Agent — are
// the SDK's; the JSON body becomes the request message; errors parsed from
// the response body become *paladin.Error.
func Example_migratingFromConnectJSON() {
	srv, stop := paladintest.Start()
	defer stop()
	p := srv.Connect()

	// Before:
	//   POST {data}/paladin.data.v1.ObjectService/GetObject
	//   Content-Type: application/json
	//   Authorization: Bearer …
	//   {"name": "tenants/…/collections/default/objects/…"}
	//   → if status == 404 and body.code == "not_found": …
	// After:
	missing := srv.Collection().String() + "/objects/00000000-0000-4000-8000-000000000000"
	_, err := p.Data.Object.GetObject(context.Background(), connect.NewRequest(&datav1.GetObjectRequest{Name: missing}))
	fmt.Println(errors.Is(err, paladin.ErrNotFound))
	// Output: true
}

// Waiting on a long-running operation — a batch, a storage migration — by name.
// The getter returns before touching the response when the call failed: a
// Connect client returns a nil response with its error.
func ExampleWait() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	p, err := paladin.Connect(paladin.Endpoints{Data: "https://paladin.example.com"},
		paladin.WithTokenSource(paladin.StaticToken("paladin_pat_…"), paladin.AudienceData))
	if err != nil {
		panic(err)
	}
	name := "tenants/7f3c…/operations/01a…"
	op, err := paladin.Wait(ctx, func(ctx context.Context) (*datav1.Operation, error) {
		r, err := p.Data.Operation.GetOperation(ctx, connect.NewRequest(&datav1.GetOperationRequest{Name: name}))
		if err != nil {
			return nil, err
		}
		return r.Msg, nil
	})
	var failed *paladin.OperationError
	switch {
	case errors.As(err, &failed):
		fmt.Println("operation failed:", failed.Code())
	case err != nil:
		fmt.Println("could not wait:", err)
	default:
		fmt.Println("done:", op.GetName())
	}
}
