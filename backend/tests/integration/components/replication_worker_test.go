//go:build integration

package components

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"testing"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/app"
	"github.com/oleg-tkachuk/paladin/backend/internal/checksum"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/s3adapter"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

const (
	// replicationTickInterval keeps the worker's passes close together.
	replicationTickInterval = 200 * time.Millisecond
	// replicationWaitFor bounds how long the test waits for the replicas.
	replicationWaitFor = time.Minute
	// replicationPollEvery is how often it looks.
	replicationPollEvery = 250 * time.Millisecond
	// replicationCommittedAgo puts the objects' commits behind the worker's
	// settle delay, so the first pass selects them.
	replicationCommittedAgo = "5 minutes"
	// replicationPresignTTL is the life of the URL the single-PUT source is
	// uploaded through.
	replicationPresignTTL = time.Minute
)

// TestReplicationWorkerCopiesAcrossBackends runs the replication worker as
// build_jobs wires it — the Postgres source, iterator, watermark store and
// clock, and the routed storage replicator — over a bucket on one SeaweedFS
// whose destination is a bucket on another. One source object is a single
// PUT, whose ETag is its MD5 and so is checked against the bytes streamed; the
// other is a multipart upload. Both must arrive at the row's key, and the
// watermark must reach their commits.
func TestReplicationWorkerCopiesAcrossBackends(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	srcBackend, srcBucket := fixtureBackend(t, ctx, pool, f), fixtureBucket(t, ctx, pool, f)
	hex := uuid.New().String()[:8]
	dstBackend, dstBucket := "dst-"+hex, "bkt-dst-"+hex
	mustExec(t, ctx, pool, `INSERT INTO storage_backends (name, kind) VALUES ($1, 's3-compatible')`, dstBackend)
	mustExec(t, ctx, pool, `INSERT INTO buckets (backend_id, name, owner_tenant_id)
		 SELECT sb.id, $2, $3 FROM storage_backends sb WHERE sb.name = $1`, dstBackend, dstBucket, f.tenantID)
	mustExec(t, ctx, pool, `UPDATE buckets SET replication_enabled = TRUE, replication_destination = $2 WHERE name = $1`,
		srcBucket, "storageBackends/"+dstBackend+"/buckets/"+dstBucket)

	epA, ak, sk := startS3(t)
	epB, _, _ := startS3(t)
	mk := func(ep, bucket string) config.StorageBackend {
		return config.StorageBackend{
			Bucket: bucket, Region: "us-east-1", Endpoint: ep, ForcePathStyle: true,
			PartSizeBytes: replicationPartSize,
			Auth:          config.StorageBackendAuth{Mode: config.AuthModeStaticKeys, AccessKey: ak, SecretKey: sk},
		}
	}
	reg := s3adapter.NewBackendRegistry(config.Storage{Backends: map[string]config.StorageBackend{
		srcBackend: mk(epA, srcBucket), dstBackend: mk(epB, dstBucket),
	}})
	prov := s3adapter.NewProvisionerRouter(reg)
	if err := prov.CreateBucket(ctx, srcBackend, srcBucket, "us-east-1"); err != nil {
		t.Fatal(err)
	}
	if err := prov.CreateBucket(ctx, dstBackend, dstBucket, "us-east-1"); err != nil {
		t.Fatal(err)
	}
	fx := replicationFixture{reg: reg, router: s3adapter.NewObjectRouter(reg)}

	single := []byte("one request, one md5")
	multi := bytes.Repeat([]byte("paladin-replica-"), int(replicationPartSize)/len("paladin-replica-")+1)
	putSingle(t, reg, srcBackend, srcBucket, f.tenantID, f.collection, "docs/single.txt", single)
	fx.put(t, srcBackend, srcBucket, f.tenantID, f.collection, "docs/multi.bin", multi)
	for key, body := range map[string][]byte{"docs/single.txt": single, "docs/multi.bin": multi} {
		mustExec(t, ctx, pool, `INSERT INTO objects (id, tenant_id, collection_id, path, state, content_type,
			   checksum_algorithm, size_bytes, committed_at)
			 VALUES ($1, $2, $3, $4, 'AVAILABLE', 'application/octet-stream', 0, $5,
			   now() - $6::interval)`,
			uuid.NewV7(), f.tenantID, f.collectionID, key, int64(len(body)), replicationCommittedAgo)
	}

	q := sqlc.New(pool)
	watermarks := adapters.NewReplicationWatermarkRepo(q)
	w := &worker.ReplicationWorker{
		Buckets:        adapters.NewLifecycleSource(q),
		Objects:        adapters.NewLifecycleObjectIter(q),
		Replicator:     app.NewStorageReplicator(reg),
		Watermarks:     watermarks,
		Clock:          watermarks,
		Interval:       replicationTickInterval,
		LookbackWindow: time.Hour,
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(runCtx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("Run = %v", err)
		}
	}()

	var newest time.Time
	if err := pool.QueryRow(ctx, `SELECT max(committed_at) FROM objects WHERE collection_id = $1`,
		f.collectionID).Scan(&newest); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(replicationWaitFor)
	for {
		mark, err := watermarks.Get(ctx, srcBackend, srcBucket)
		if err != nil {
			t.Fatal(err)
		}
		if !mark.Before(newest.Truncate(time.Microsecond)) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("watermark %v did not reach the newest commit %v", mark, newest)
		}
		time.Sleep(replicationPollEvery)
	}
	for key, body := range map[string][]byte{"docs/single.txt": single, "docs/multi.bin": multi} {
		dst := objecth.Location{BackendID: dstBackend, Bucket: dstBucket, TenantID: f.tenantID, Collection: f.collection, Key: key}
		if got := fx.read(t, dst); !bytes.Equal(got, body) {
			t.Errorf("replica of %s holds %d bytes, want %d", key, len(got), len(body))
		}
	}
}

// putSingle uploads body in one PUT through a presigned URL, as a client
// does, so the stored ETag is the body's MD5.
func putSingle(t *testing.T, reg *s3adapter.BackendRegistry, backend, bucket string, tenant uuid.UUID, collection, key string, body []byte) {
	t.Helper()
	ctx := context.Background()
	c, err := reg.For(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	url, headers, _, err := c.PresignPut(ctx, objecth.PresignPutArgs{
		BackendID: backend, TenantID: tenant, Bucket: bucket, Collection: collection, Key: key,
		ContentType: "text/plain", SizeBytes: int64(len(body)), TTL: replicationPresignTTL,
		ChecksumAlgo: checksum.SHA256, ChecksumValue: base64.StdEncoding.EncodeToString(sum[:]),
	})
	if err != nil {
		t.Fatalf("presign: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.ContentLength = int64(len(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put answered %d", resp.StatusCode)
	}
}
