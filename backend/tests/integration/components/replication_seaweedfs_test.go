//go:build integration

package components

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"testing"
	"uuid"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/s3adapter"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/storagepath"
)

// replicationPartSize is the destination's multipart part size: S3's minimum
// for every part but the last, so a body a few parts long stays small.
const replicationPartSize int64 = 5 << 20

// replicationBodyParts is how many whole parts the large body spans; it gets
// a short trailing part on top, so the stream ends on a partial part.
const replicationBodyParts = 2

// replicationFixture is two SeaweedFS instances behind one registry, with a
// source bucket and a second bucket on backend "a" and a bucket on "b".
type replicationFixture struct {
	reg    *s3adapter.BackendRegistry
	router *s3adapter.ObjectRouter
	// sdk is each backend's own SDK client, for reading stored headers.
	sdk map[string]*s3.Client
}

const (
	replSrcBucket  = "paladin-repl-src"
	replSameBucket = "paladin-repl-same"
	replDstBucket  = "paladin-repl-dst"
)

func newReplicationFixture(t *testing.T) replicationFixture {
	t.Helper()
	ctx := context.Background()
	epA, ak, sk := startS3(t)
	epB, _, _ := startS3(t)
	mk := func(ep, bucket string) config.StorageBackend {
		return config.StorageBackend{
			Bucket: bucket, Region: "us-east-1", Endpoint: ep, ForcePathStyle: true,
			PartSizeBytes: replicationPartSize,
			Auth: config.StorageBackendAuth{
				Mode: config.AuthModeStaticKeys, AccessKey: ak, SecretKey: sk,
			},
		}
	}
	reg := s3adapter.NewBackendRegistry(config.Storage{Backends: map[string]config.StorageBackend{
		"a": mk(epA, replSrcBucket),
		"b": mk(epB, replDstBucket),
	}})
	prov := s3adapter.NewProvisionerRouter(reg)
	for _, b := range []struct{ backend, bucket string }{
		{"a", replSrcBucket}, {"a", replSameBucket}, {"b", replDstBucket},
	} {
		if err := prov.CreateBucket(ctx, b.backend, b.bucket, "us-east-1"); err != nil {
			t.Fatalf("create bucket %s on %s: %v", b.bucket, b.backend, err)
		}
	}
	sdkA, _ := seaweedClients(t, epA, ak, sk)
	sdkB, _ := seaweedClients(t, epB, ak, sk)
	return replicationFixture{
		reg: reg, router: s3adapter.NewObjectRouter(reg),
		sdk: map[string]*s3.Client{"a": sdkA, "b": sdkB},
	}
}

// head reads the stored headers at loc.
func (f replicationFixture) head(t *testing.T, loc objecth.Location) *s3.HeadObjectOutput {
	t.Helper()
	key, err := storagepath.Compose(loc.TenantID, loc.Collection, loc.Key)
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.sdk[loc.BackendID].HeadObject(context.Background(), &s3.HeadObjectInput{
		Bucket: aws.String(loc.Bucket), Key: aws.String(key),
	})
	if err != nil {
		t.Fatalf("head %s/%s: %v", loc.BackendID, loc.Bucket, err)
	}
	return out
}

// The headers a replica must keep: a gzip-encoded body, so a transparent
// decompression on the way through would fail the size and digest checks.
var replicaSourceHeaders = s3adapter.ObjectHeaders{
	ContentType:        "application/json",
	CacheControl:       "public, max-age=60",
	ContentDisposition: `attachment; filename="report.json.gz"`,
	ContentEncoding:    "gzip",
	ContentLanguage:    "uk",
	Metadata:           map[string]string{"owner": "finance"},
}

// TestReplicaKeepsTheSourceHeaders writes a source with every stored header
// and user metadata, replicates it both ways — streamed across two stores,
// server-side within one — and reads each replica's headers back.
func TestReplicaKeepsTheSourceHeaders(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	f := newReplicationFixture(t)
	ctx := context.Background()
	tenant := uuid.New()
	const collection, key = "docs", "report.json.gz"

	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write([]byte(`{"total": 42}`)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	body := gz.Bytes()
	c, err := f.reg.For(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	w, err := c.OpenWithHeaders(ctx, replSrcBucket, tenant, collection, key, replicaSourceHeaders, int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(body); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := w.Close(); err != nil {
		t.Fatal(err)
	}
	src := objecth.Location{BackendID: "a", Bucket: replSrcBucket, TenantID: tenant, Collection: collection, Key: key}
	for name, dst := range map[string]objecth.Location{
		"streamed":    {BackendID: "b", Bucket: replDstBucket, TenantID: tenant, Collection: collection, Key: key},
		"server-side": {BackendID: "a", Bucket: replSameBucket, TenantID: tenant, Collection: collection, Key: key},
	} {
		t.Run(name, func(t *testing.T) {
			if err := f.router.Replicate(ctx, src, dst, int64(len(body))); err != nil {
				t.Fatalf("Replicate: %v", err)
			}
			if got := f.read(t, dst); !bytes.Equal(got, body) {
				t.Errorf("replica holds %d bytes, want the encoded %d", len(got), len(body))
			}
			want, got := f.head(t, src), f.head(t, dst)
			for _, h := range []struct {
				name      string
				want, got *string
			}{
				{"Content-Type", want.ContentType, got.ContentType},
				{"Cache-Control", want.CacheControl, got.CacheControl},
				{"Content-Disposition", want.ContentDisposition, got.ContentDisposition},
				{"Content-Encoding", want.ContentEncoding, got.ContentEncoding},
				{"Content-Language", want.ContentLanguage, got.ContentLanguage},
			} {
				if aws.ToString(h.got) != aws.ToString(h.want) || aws.ToString(h.want) == "" {
					t.Errorf("%s = %q, want the source's %q", h.name, aws.ToString(h.got), aws.ToString(h.want))
				}
			}
			if got.Metadata["owner"] != replicaSourceHeaders.Metadata["owner"] {
				t.Errorf("user metadata = %v, want %v", got.Metadata, replicaSourceHeaders.Metadata)
			}
		})
	}
}

// put writes body to (backend, bucket) through that backend's own client.
func (f replicationFixture) put(t *testing.T, backend, bucket string, tenant uuid.UUID, collection, key string, body []byte) {
	t.Helper()
	ctx := context.Background()
	c, err := f.reg.For(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}
	w, err := c.Open(ctx, bucket, tenant, collection, key, "application/octet-stream", int64(len(body)))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := w.Write(body); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, _, err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// read returns the bytes stored at loc.
func (f replicationFixture) read(t *testing.T, loc objecth.Location) []byte {
	t.Helper()
	ctx := context.Background()
	c, err := f.reg.For(ctx, loc.BackendID)
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := c.GetStream(ctx, loc.Bucket, loc.TenantID, loc.Collection, loc.Key)
	if err != nil {
		t.Fatalf("read %s/%s: %v", loc.BackendID, loc.Bucket, err)
	}
	defer func() { _ = r.Close() }()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// TestReplicateBetweenTwoSeaweedFSBackends copies objects with the replicator
// the worker uses: across two separate stores (a body several parts long, and
// an empty one), between two buckets of one store, and refuses a source that
// is not there with the error the worker passes over.
func TestReplicateBetweenTwoSeaweedFSBackends(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	f := newReplicationFixture(t)
	ctx := context.Background()
	tenant := uuid.New()
	const collection = "docs"

	large := bytes.Repeat([]byte("paladin-replica-"), int(replicationPartSize)*replicationBodyParts/len("paladin-replica-")+1)
	cases := []struct {
		name       string
		key        string
		body       []byte
		dstBackend string
		dstBucket  string
	}{
		{"across backends, several parts", "large.bin", large, "b", replDstBucket},
		{"across backends, empty", "empty.bin", []byte{}, "b", replDstBucket},
		{"one backend, two buckets", "small.txt", []byte("same-store"), "a", replSameBucket},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f.put(t, "a", replSrcBucket, tenant, collection, tc.key, tc.body)
			src := objecth.Location{BackendID: "a", Bucket: replSrcBucket, TenantID: tenant, Collection: collection, Key: tc.key}
			dst := objecth.Location{BackendID: tc.dstBackend, Bucket: tc.dstBucket, TenantID: tenant, Collection: collection, Key: tc.key}
			if err := f.router.Replicate(ctx, src, dst, int64(len(tc.body))); err != nil {
				t.Fatalf("Replicate: %v", err)
			}
			if got := f.read(t, dst); !bytes.Equal(got, tc.body) {
				t.Errorf("replica holds %d bytes, want the source's %d", len(got), len(tc.body))
			}
		})
	}

	t.Run("source missing", func(t *testing.T) {
		src := objecth.Location{BackendID: "a", Bucket: replSrcBucket, TenantID: tenant, Collection: collection, Key: "absent"}
		dst := objecth.Location{BackendID: "b", Bucket: replDstBucket, TenantID: tenant, Collection: collection, Key: "absent"}
		err := f.router.Replicate(ctx, src, dst, int64(len("x")))
		if !errors.Is(err, s3adapter.ErrObjectNotFound) {
			t.Errorf("Replicate of a missing source = %v, want ErrObjectNotFound", err)
		}
	})
}
