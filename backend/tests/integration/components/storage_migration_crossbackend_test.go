//go:build integration

package components

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"io"
	"testing"

	"github.com/google/uuid"

	objpkg "github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/s3adapter"
)

// TestStorageMigration_CrossBackendStreamThrough is the ADR-0015 Phase 3 slice-3
// live proof: the shared→dedicated migration's cross-backend copy path
// (ObjectRouter.CopyObject → GetStream(source) piped into the destination's
// multipart writer) actually moves an object between two physically distinct S3
// backends, for an object large enough to force multipart streaming (so the
// "arbitrarily large objects without buffering" claim is exercised, not just
// the single-part happy path). Same-backend server-side copy is covered by the
// unit tests; this closes the one gap that needed two real backends.
func TestStorageMigration_CrossBackendStreamThrough(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()

	epA, ak, sk := startS3(t) // shared backend
	epB, _, _ := startS3(t)   // dedicated target backend

	mkBackend := func(ep, bucket string) config.StorageBackend {
		return config.StorageBackend{
			Bucket:         bucket,
			Region:         "us-east-1",
			Endpoint:       ep,
			ForcePathStyle: true,
			PartSizeRaw:    "5MB", // small part size; the 12MiB object below spans multiple parts
			Auth: config.StorageBackendAuth{
				Mode:      config.AuthModeStaticKeys,
				AccessKey: ak,
				SecretKey: sk,
			},
		}
	}
	const bucketShared, bucketDedicated = "paladin-shared", "paladin-tenant-dedicated"
	reg := s3adapter.NewBackendRegistry(config.Storage{
		Backends: map[string]config.StorageBackend{
			"shared":    mkBackend(epA, bucketShared),
			"dedicated": mkBackend(epB, bucketDedicated),
		},
	})

	prov := s3adapter.NewProvisionerRouter(reg)
	if err := prov.CreateBucket(ctx, "shared", bucketShared, "us-east-1"); err != nil {
		t.Fatalf("create shared bucket: %v", err)
	}
	if err := prov.CreateBucket(ctx, "dedicated", bucketDedicated, "us-east-1"); err != nil {
		t.Fatalf("create dedicated bucket: %v", err)
	}

	// 12 MiB of random data — large enough to force the destination's multipart
	// writer to span multiple parts, exercising the streaming copy (not just a
	// single-part PUT).
	data := make([]byte, 12<<20)
	if _, err := rand.Read(data); err != nil {
		t.Fatalf("rand: %v", err)
	}
	want := sha256.Sum256(data)

	tenant := uuid.New()
	const collection, key = "invoices", "2026/big.bin"

	srcClient, err := reg.For(ctx, "shared")
	if err != nil {
		t.Fatalf("registry For(shared): %v", err)
	}
	w, err := srcClient.Open(ctx, bucketShared, tenant, collection, key, "application/octet-stream", int64(len(data)))
	if err != nil {
		t.Fatalf("open on shared backend: %v", err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatalf("write on shared backend: %v", err)
	}
	if _, size, _, err := w.Close(); err != nil || size != int64(len(data)) {
		t.Fatalf("close on shared backend: size=%d err=%v", size, err)
	}

	// The migration copy: cross-backend, so the router stream-throughs it.
	router := s3adapter.NewObjectRouter(reg)
	src := objpkg.Location{BackendID: "shared", TenantID: tenant, Bucket: bucketShared, Collection: collection, Key: key}
	dst := objpkg.Location{BackendID: "dedicated", TenantID: tenant, Bucket: bucketDedicated, Collection: collection, Key: key}
	if err := router.CopyObject(ctx, src, dst); err != nil {
		t.Fatalf("cross-backend CopyObject: %v", err)
	}

	// The object landed on the dedicated backend with the right size...
	if _, size, _, _, err := router.Head(ctx, "dedicated", bucketDedicated, tenant, collection, key, ""); err != nil || size != int64(len(data)) {
		t.Fatalf("Head on dedicated backend: size=%d err=%v (want the 12MiB copy)", size, err)
	}
	// ...and byte-for-byte identical content (sha256 over the streamed body).
	dstClient, err := reg.For(ctx, "dedicated")
	if err != nil {
		t.Fatalf("registry For(dedicated): %v", err)
	}
	rc, _, err := dstClient.GetStream(ctx, bucketDedicated, tenant, collection, key)
	if err != nil {
		t.Fatalf("GetStream on dedicated backend: %v", err)
	}
	defer func() { _ = rc.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, rc); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got := h.Sum(nil); !bytes.Equal(got, want[:]) {
		t.Errorf("copied content hash mismatch: got %x want %x", got, want)
	}

	// The source copy is RETAINED (cleanup is a separate, retention-gated slice).
	if _, _, _, _, err := router.Head(ctx, "shared", bucketShared, tenant, collection, key, ""); err != nil {
		t.Errorf("source object missing after copy: %v (Phase 3 retains the source until cleanup)", err)
	}
}
