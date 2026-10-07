//go:build integration

package components

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/s3adapter"
)

// seaweedImage pins the SeaweedFS release the e2e compose stack uses, for a
// reproducible pull.
//
// SeaweedFS and not MinIO, since 2026-09-28: MinIO has closed every free
// registry its images were on. Docker Hub 404s both minio/minio and minio/mc,
// quay.io answers 401 even with a freshly issued anonymous pull token (its
// tags list too), and ghcr.io/minio/* answers 403. Docker reports the first as
// "pull access denied ... may require 'docker login'" and the second as
// "unauthorized", both of which read like a credentials problem and are not
// one — there is no account to add, so do not re-point this at another MinIO
// mirror. SeaweedFS already backs the compose stack, so the substitution keeps
// one S3 implementation in the repository rather than adding a second.
const seaweedImage = "chrislusf/seaweedfs:4.47"

// s3TestIdentities is the -s3.config SeaweedFS reads its credentials from.
// MinIO took them as MINIO_ROOT_USER / MINIO_ROOT_PASSWORD; SeaweedFS takes
// only a file, so the helper writes this one into the container.
//
// A named identity and deliberately NOT SeaweedFS' "anonymous" one: anonymous
// grants the actions to unsigned requests, and the adapter under test signs
// every request. A suite that passed because the server never checked a
// signature would prove nothing about the signing.
const s3TestIdentities = `{
  "identities": [
    {
      "name": "paladin-test",
      "credentials": [{"accessKey": "paladin-test-access", "secretKey": "paladin-test-secret-key"}],
      "actions": ["Read", "Write", "List", "Admin"]
    }
  ]
}`

// s3Port is SeaweedFS' S3 gateway port. MinIO served S3 on 9000; keeping the
// number in one place is what stops the wait strategy and the mapped-port
// lookup from disagreeing.
const s3Port = "8333/tcp"

// startS3 brings up a single-node SeaweedFS S3 gateway and returns its
// endpoint plus the credentials its identity file declares.
func startS3(t *testing.T) (endpoint, accessKey, secretKey string) {
	t.Helper()
	return startS3With(t, s3TestIdentities)
}

// startS3With is startS3 with the given SeaweedFS identity file, for a test
// that needs the store configured otherwise. The credentials returned are
// still the paladin-test identity's, which identities must declare.
func startS3With(t *testing.T, identities string) (endpoint, accessKey, secretKey string) {
	t.Helper()
	ctx := context.Background()
	const user, pass = "paladin-test-access", "paladin-test-secret-key"
	const configPath = "/etc/seaweedfs/s3.json"
	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: seaweedImage,
			Cmd: []string{
				"server", "-dir=/data", "-ip.bind=0.0.0.0",
				"-s3", "-s3.port=8333", "-s3.config=" + configPath,
			},
			Files: []testcontainers.ContainerFile{{
				Reader:            strings.NewReader(identities),
				ContainerFilePath: configPath,
				FileMode:          0o644,
			}},
			ExposedPorts: []string{s3Port},
			// /status is the S3 gateway's own readiness endpoint. It answers
			// only once the gateway has a filer and a master behind it, which
			// is the thing a bare port check would miss.
			WaitingFor: wait.ForHTTP("/status").WithPort(s3Port).WithStartupTimeout(90 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start seaweedfs: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(ctr) })
	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := ctr.MappedPort(ctx, s3Port)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("http://%s:%s", host, port.Port()), user, pass
}

// TestBackendRegistryRouting proves the multi-backend routing end-to-end
// (docs/backend-registry.md, ADR-0015 Phase 2): with two physically distinct
// MinIO backends configured, an object written to backend "a" is resolvable
// through the router when addressed to "a" and NOT when addressed to "b".
// Because "a" and "b" are separate MinIO instances, a positive Head on "a" and
// a negative Head on "b" can only happen if the router dispatched each call to
// a different client — not a shared default.
func TestBackendRegistryRouting(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()

	epA, ak, sk := startS3(t)
	epB, _, _ := startS3(t) // same credentials

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
	const collection, key = "docs", "obj-1"
	data := []byte("hello-from-A")
	ca, err := reg.For(ctx, "a")
	if err != nil {
		t.Fatalf("registry For(a): %v", err)
	}
	w, err := ca.Open(ctx, bucketA, tenant, collection, key, "text/plain", int64(len(data)))
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
	if _, size, _, _, err := orouter.Head(ctx, "a", bucketA, tenant, collection, key, ""); err != nil || size != int64(len(data)) {
		t.Fatalf("Head via backend a: size=%d err=%v (want the object)", size, err)
	}
	if _, _, _, _, err := orouter.Head(ctx, "b", bucketB, tenant, collection, key, ""); err == nil {
		t.Fatal("Head via backend b resolved the object — routing leaked across backends")
	}

	// An unknown backend id is refused by the registry, never silently
	// defaulted onto another tenant's store.
	if _, _, _, _, err := orouter.Head(ctx, "ghost", bucketA, tenant, collection, key, ""); err == nil {
		t.Fatal("Head via unknown backend must error")
	}
}
