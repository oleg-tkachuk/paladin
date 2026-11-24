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

	"github.com/jackc/pgx/v5/pgxpool"

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
	// replicationPresignTTL is the life of the URL the single-PUT source is
	// uploaded through.
	replicationPresignTTL = time.Minute
	// replicationRegion is both stores' region.
	replicationRegion = "us-east-1"
)

// workerReplication is a replicated bucket on one SeaweedFS whose destination
// is a bucket on another, with the worker wired as build_jobs wires it: the
// Postgres source and replica store, and the routed storage replicator.
type workerReplication struct {
	replicationFixture
	f                     fixture
	pool                  *pgxpool.Pool
	srcBackend, srcBucket string
	dstBackend, dstBucket string
}

func newWorkerReplication(t *testing.T, ctx context.Context) workerReplication {
	t.Helper()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	wr := workerReplication{
		f: f, pool: pool,
		srcBackend: fixtureBackend(t, ctx, pool, f), srcBucket: fixtureBucket(t, ctx, pool, f),
	}
	hex := uuid.New().String()[:8]
	wr.dstBackend, wr.dstBucket = "dst-"+hex, "bkt-dst-"+hex
	mustExec(t, ctx, pool, `INSERT INTO storage_backends (name, kind) VALUES ($1, 's3-compatible')`, wr.dstBackend)
	mustExec(t, ctx, pool, `INSERT INTO buckets (backend_id, name, owner_tenant_id)
		 SELECT sb.id, $2, $3 FROM storage_backends sb WHERE sb.name = $1`, wr.dstBackend, wr.dstBucket, f.tenantID)

	epA, ak, sk := startS3(t)
	epB, _, _ := startS3(t)
	mk := func(ep, bucket string) config.StorageBackend {
		return config.StorageBackend{
			Bucket: bucket, Region: replicationRegion, Endpoint: ep, ForcePathStyle: true,
			PartSizeBytes: replicationPartSize,
			Auth:          config.StorageBackendAuth{Mode: config.AuthModeStaticKeys, AccessKey: ak, SecretKey: sk},
		}
	}
	reg := s3adapter.NewBackendRegistry(config.Storage{Backends: map[string]config.StorageBackend{
		wr.srcBackend: mk(epA, wr.srcBucket), wr.dstBackend: mk(epB, wr.dstBucket),
	}})
	prov := s3adapter.NewProvisionerRouter(reg)
	if err := prov.CreateBucket(ctx, wr.srcBackend, wr.srcBucket, replicationRegion); err != nil {
		t.Fatal(err)
	}
	if err := prov.CreateBucket(ctx, wr.dstBackend, wr.dstBucket, replicationRegion); err != nil {
		t.Fatal(err)
	}
	wr.replicationFixture = replicationFixture{reg: reg, router: s3adapter.NewObjectRouter(reg)}
	return wr
}

// enable turns replication on for the source bucket.
func (wr workerReplication) enable(t *testing.T, ctx context.Context) {
	t.Helper()
	mustExec(t, ctx, wr.pool, `UPDATE buckets SET replication_enabled = TRUE, replication_destination = $2 WHERE name = $1`,
		wr.srcBucket, "storageBackends/"+wr.dstBackend+"/buckets/"+wr.dstBucket)
}

// promoted inserts an AVAILABLE row for bytes already at key, as a promote
// leaves it.
func (wr workerReplication) promoted(t *testing.T, ctx context.Context, key string, size int) uuid.UUID {
	t.Helper()
	id := uuid.NewV7()
	mustExec(t, ctx, wr.pool, `INSERT INTO objects (id, tenant_id, collection_id, path, state, content_type,
		   checksum_algorithm, size_bytes, committed_at, bytes_written_at)
		 VALUES ($1, $2, $3, $4, 'AVAILABLE', 'application/octet-stream', 0, $5, now(), clock_timestamp())`,
		id, wr.f.tenantID, wr.f.collectionID, key, int64(size))
	return id
}

// run starts the worker and stops it when the test ends.
func (wr workerReplication) run(t *testing.T, ctx context.Context) {
	t.Helper()
	q := sqlc.New(wr.pool)
	w := &worker.ReplicationWorker{
		Buckets:    adapters.NewLifecycleSource(q),
		Replicas:   adapters.NewReplicaRepo(q),
		Replicator: app.NewStorageReplicator(wr.reg),
		Interval:   replicationTickInterval,
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(runCtx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("Run = %v", err)
		}
	})
}

// settled waits until the source bucket has rows and none of them is due.
func (wr workerReplication) settled(t *testing.T, ctx context.Context) {
	t.Helper()
	deadline := time.Now().Add(replicationWaitFor)
	for {
		var rows, due int
		if err := wr.pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE r.next_attempt_at IS NOT NULL)
			  FROM object_replicas r JOIN buckets b ON b.id = r.source_bucket_id
			 WHERE b.name = $1`, wr.srcBucket).Scan(&rows, &due); err != nil {
			t.Fatal(err)
		}
		if due == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d replicas still due after %v", due, rows, replicationWaitFor)
		}
		time.Sleep(replicationPollEvery)
	}
}

func (wr workerReplication) dst(key string) objecth.Location {
	return objecth.Location{
		BackendID: wr.dstBackend, Bucket: wr.dstBucket, TenantID: wr.f.tenantID, Collection: wr.f.collection, Key: key,
	}
}

// TestReplicationWorkerCopiesAcrossBackends runs the wired worker over a
// bucket replicated to another SeaweedFS. One object exists before
// replication is enabled and is copied by the seed; the other is written
// after it, and queued by its promote. One is a single PUT, whose ETag is its
// MD5 and so is checked against the bytes streamed; the other a multipart
// upload. Then the single one is overwritten in place — a later promote with
// new bytes under the AVAILABLE row — and its replica follows.
func TestReplicationWorkerCopiesAcrossBackends(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	wr := newWorkerReplication(t, ctx)
	f := wr.f

	single := []byte("one request, one md5")
	multi := bytes.Repeat([]byte("paladin-replica-"), int(replicationPartSize)/len("paladin-replica-")+1)
	putSingle(t, wr.reg, wr.srcBackend, wr.srcBucket, f.tenantID, f.collection, "docs/single.txt", single)
	singleID := wr.promoted(t, ctx, "docs/single.txt", len(single))
	wr.enable(t, ctx)
	wr.put(t, wr.srcBackend, wr.srcBucket, f.tenantID, f.collection, "docs/multi.bin", multi)
	wr.promoted(t, ctx, "docs/multi.bin", len(multi))

	wr.run(t, ctx)
	wr.settled(t, ctx)
	for key, body := range map[string][]byte{"docs/single.txt": single, "docs/multi.bin": multi} {
		if got := wr.read(t, wr.dst(key)); !bytes.Equal(got, body) {
			t.Errorf("replica of %s holds %d bytes, want %d", key, len(got), len(body))
		}
	}

	rewritten := []byte("the same key, written again")
	// A presigned PUT refuses an existing key, so the overwrite goes through
	// the backend's own writer, as a storage event's newer write would land.
	wr.put(t, wr.srcBackend, wr.srcBucket, f.tenantID, f.collection, "docs/single.txt", rewritten)
	mustExec(t, ctx, wr.pool, `UPDATE objects SET size_bytes = $2, bytes_written_at = clock_timestamp() WHERE id = $1`,
		singleID, int64(len(rewritten)))
	wr.settled(t, ctx)
	if got := wr.read(t, wr.dst("docs/single.txt")); !bytes.Equal(got, rewritten) {
		t.Errorf("replica after the overwrite holds %q, want %q", got, rewritten)
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
