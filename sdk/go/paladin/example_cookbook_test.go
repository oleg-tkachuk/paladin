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
	"strings"
	"sync"
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
