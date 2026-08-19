//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/oleg-tkachuk/paladin-private/internal/config"
	"github.com/oleg-tkachuk/paladin-private/internal/storage/s3adapter"
)

// minioImage pins the same MinIO release the e2e workflow uses, for a
// reproducible pull.
const minioImage = "minio/minio:RELEASE.2025-04-22T22-12-26Z"

// startMinio brings up a single-node MinIO and returns its S3 endpoint plus
// the root credentials.
func startMinio(t *testing.T) (endpoint, accessKey, secretKey string) {
	t.Helper()
	ctx := context.Background()
	const user, pass = "minioadmin", "minioadmin"
	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        minioImage,
			Cmd:          []string{"server", "/data"},
			Env:          map[string]string{"MINIO_ROOT_USER": user, "MINIO_ROOT_PASSWORD": pass},
			ExposedPorts: []string{"9000/tcp"},
			WaitingFor:   wait.ForHTTP("/minio/health/ready").WithPort("9000/tcp").WithStartupTimeout(90 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start minio: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(ctr) })
	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := ctr.MappedPort(ctx, "9000/tcp")
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("http://%s:%s", host, port.Port()), user, pass
}

// TestBackendRegistryRouting proves the multi-backend routing end-to-end
// (docs/backend-registry.md, ADR-0011 Phase 2): with two physically distinct
// MinIO backends configured, an object written to backend "a" is resolvable
// through the router when addressed to "a" and NOT when addressed to "b".
// Because "a" and "b" are separate MinIO instances, a positive Head on "a" and
// a negative Head on "b" can only happen if the router dispatched each call to
// a different client — not a shared default.
func TestBackendRegistryRouting(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()

	epA, ak, sk := startMinio(t)
	epB, _, _ := startMinio(t) // same root creds

	mkBackend := func(ep, bucket string) config.StorageBackend {
		return config.StorageBackend{
			Bucket:         bucket,
			Region:         "us-east-1",
			Endpoint:       ep,
			ForcePathStyle: true,
			Auth: config.StorageBackendAuth{
				Mode:      config.AuthModeStaticKeys,
				AccessKey: ak,
				SecretKey: sk,
			},
		}
	}
	const bucketA, bucketB = "paladin-a", "paladin-b"
	reg := s3adapter.NewBackendRegistry(config.Storage{
		Backends: map[string]config.StorageBackend{
			"a": mkBackend(epA, bucketA),
			"b": mkBackend(epB, bucketB),
		},
	})

	// Provision a bucket on each backend through the provisioner router (also
	// exercises per-backend provisioner routing).
	prov := s3adapter.NewProvisionerRouter(reg)
	if err := prov.CreateBucket(ctx, "a", bucketA, "us-east-1"); err != nil {
		t.Fatalf("create bucket on backend a: %v", err)
	}
	if err := prov.CreateBucket(ctx, "b", bucketB, "us-east-1"); err != nil {
		t.Fatalf("create bucket on backend b: %v", err)
	}

	// Write an object ONLY to backend "a", through backend a's own client so
	// the physical key composition matches what Head will look up.
	tenant := uuid.New()
	const objectKey, key = "docs", "obj-1"
	data := []byte("hello-from-A")
	ca, err := reg.For(ctx, "a")
	if err != nil {
		t.Fatalf("registry For(a): %v", err)
	}
	w, err := ca.Open(ctx, bucketA, tenant, objectKey, key, "text/plain", int64(len(data)))
	if err != nil {
		t.Fatalf("open on backend a: %v", err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatalf("write on backend a: %v", err)
	}
	if _, size, _, err := w.Close(); err != nil || size != int64(len(data)) {
		t.Fatalf("close on backend a: size=%d err=%v", size, err)
	}

	// Route Head through the object router: backend "a" resolves the object,
	// backend "b" (a distinct MinIO with the same bucket name) does not.
	orouter := s3adapter.NewObjectRouter(reg)
	if _, size, _, _, err := orouter.Head(ctx, "a", bucketA, tenant, objectKey, key); err != nil || size != int64(len(data)) {
		t.Fatalf("Head via backend a: size=%d err=%v (want the object)", size, err)
	}
	if _, _, _, _, err := orouter.Head(ctx, "b", bucketB, tenant, objectKey, key); err == nil {
		t.Fatal("Head via backend b resolved the object — routing leaked across backends")
	}

	// An unknown backend id is refused by the registry, never silently
	// defaulted onto another tenant's store.
	if _, _, _, _, err := orouter.Head(ctx, "ghost", bucketA, tenant, objectKey, key); err == nil {
		t.Fatal("Head via unknown backend must error")
	}
}
