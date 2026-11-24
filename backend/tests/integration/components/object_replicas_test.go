//go:build integration

package components

import (
	"context"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

const (
	// replicaTestLease is the claim lease these tests take.
	replicaTestLease = time.Minute
	// replicaTestBatch is a claim size larger than any test's queue.
	replicaTestBatch = 100
	// replicaTestRetryDelay puts a failed row off past the test's end.
	replicaTestRetryDelay = time.Hour
)

// replicaFixture is the seed fixture's bucket with replication enabled, and
// the store over a superuser pool standing in for the worker's.
type replicaFixture struct {
	fixture
	pool     *pgxpool.Pool
	bucketID pgtype.UUID
	store    *adapters.ReplicaRepo
}

func newReplicaFixture(t *testing.T, ctx context.Context) replicaFixture {
	t.Helper()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	rf := replicaFixture{fixture: f, pool: pool, store: adapters.NewReplicaRepo(sqlc.New(pool))}
	if err := pool.QueryRow(ctx, `SELECT bucket_id FROM collections WHERE id = $1`, f.collectionID).Scan(&rf.bucketID); err != nil {
		t.Fatal(err)
	}
	return rf
}

func (rf replicaFixture) enable(t *testing.T, ctx context.Context) {
	t.Helper()
	mustExec(t, ctx, rf.pool, `UPDATE buckets SET replication_enabled = TRUE, replication_destination = 'storageBackends/x/buckets/y'
		WHERE id = $1`, rf.bucketID)
}

// available inserts an AVAILABLE object at key, as a promoted upload is.
func (rf replicaFixture) available(t *testing.T, ctx context.Context, key string) uuid.UUID {
	t.Helper()
	id := uuid.NewV7()
	mustExec(t, ctx, rf.pool, `INSERT INTO objects (id, tenant_id, collection_id, path, state, content_type,
		   checksum_algorithm, size_bytes, committed_at, bytes_written_at)
		 VALUES ($1, $2, $3, $4, 'AVAILABLE', 'text/plain', 0, 1, now(), clock_timestamp())`,
		id, rf.tenantID, rf.collectionID, key)
	return id
}

// promote moves a PENDING object to AVAILABLE with a byte write, as the
// state machine's promote does.
func (rf replicaFixture) promote(t *testing.T, ctx context.Context, id uuid.UUID) {
	t.Helper()
	mustExec(t, ctx, rf.pool, `UPDATE objects SET state = 'AVAILABLE', committed_at = now(),
		bytes_written_at = clock_timestamp() WHERE id = $1`, id)
}

func (rf replicaFixture) claim(t *testing.T, ctx context.Context) []worker.ReplicaClaim {
	t.Helper()
	claims, err := rf.store.ClaimReplicas(ctx, rf.bucketID, replicaTestBatch, replicaTestLease)
	if err != nil {
		t.Fatalf("ClaimReplicas: %v", err)
	}
	return claims
}

// dueCount is how many of the bucket's rows are due.
func (rf replicaFixture) dueCount(t *testing.T, ctx context.Context) int {
	t.Helper()
	var n int
	if err := rf.pool.QueryRow(ctx, `SELECT count(*) FROM object_replicas
		WHERE source_bucket_id = $1 AND next_attempt_at IS NOT NULL`, rf.bucketID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A promote in a bucket with replication enabled queues its location in the
// same transaction; one in a bucket without it queues nothing.
func TestObjectReplicasQueueAPromote(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rf := newReplicaFixture(t, ctx)

	before := seedPendingObject(t, ctx, rf.pool, rf.fixture)
	rf.promote(t, ctx, before)
	if n := rf.dueCount(t, ctx); n != 0 {
		t.Fatalf("due = %d in a bucket without replication", n)
	}

	rf.enable(t, ctx)
	id := seedPendingObject(t, ctx, rf.pool, rf.fixture)
	if n := rf.dueCount(t, ctx); n != 0 {
		t.Fatalf("due = %d for a PENDING object", n)
	}
	rf.promote(t, ctx, id)
	claims := rf.claim(t, ctx)
	if len(claims) != 1 || claims[0].ObjectID != id || claims[0].Collection != rf.collection || claims[0].TenantID != rf.tenantID {
		t.Fatalf("claims = %+v, want the promoted object's location", claims)
	}
}

// The seed queues the objects written before replication was enabled once;
// a change to the bucket's settings has it queue them again.
func TestObjectReplicasSeedOnceUntilTheSettingsChange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rf := newReplicaFixture(t, ctx)
	rf.available(t, ctx, "a")
	rf.available(t, ctx, "b")
	rf.enable(t, ctx)

	for i, want := range []int64{2, 0} {
		n, err := rf.store.SeedReplication(ctx, rf.bucketID)
		if err != nil || n != want {
			t.Fatalf("seed %d = (%d, %v), want %d", i, n, err, want)
		}
	}
	claims := rf.claim(t, ctx)
	for _, c := range claims {
		if err := rf.store.SettleReplica(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(t, ctx, rf.pool, `UPDATE buckets SET replication_filter = 'size_bytes > 0' WHERE id = $1`, rf.bucketID)
	if n, err := rf.store.SeedReplication(ctx, rf.bucketID); err != nil || n != 2 {
		t.Fatalf("seed after a filter change = (%d, %v), want 2", n, err)
	}
	// An update that leaves the settings as they were keeps the seed.
	mustExec(t, ctx, rf.pool, `UPDATE buckets SET replication_filter = replication_filter WHERE id = $1`, rf.bucketID)
	if n, err := rf.store.SeedReplication(ctx, rf.bucketID); err != nil || n != 0 {
		t.Fatalf("seed after a no-op update = (%d, %v), want 0", n, err)
	}
}

// A collection rebound into a bucket brings no object into it: objects are
// replicated from the bucket their bytes are in (ADR-0051). One moved into
// it — a storage migration's move — is queued there by its move.
func TestObjectReplicasFollowAMoveNotARebind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rf := newReplicaFixture(t, ctx)
	rf.available(t, ctx, "moved")
	var target pgtype.UUID
	if err := rf.pool.QueryRow(ctx, `INSERT INTO buckets (backend_id, name, owner_tenant_id, replication_enabled, replication_destination)
		 SELECT backend_id, $2, $3, TRUE, 'storageBackends/x/buckets/y' FROM buckets WHERE id = $1
		 RETURNING id`, rf.bucketID, "bkt-tgt-"+uuid.New().String()[:8], rf.tenantID).Scan(&target); err != nil {
		t.Fatal(err)
	}
	if n, err := rf.store.SeedReplication(ctx, target); err != nil || n != 0 {
		t.Fatalf("seed of the empty target = (%d, %v), want 0", n, err)
	}
	mustExec(t, ctx, rf.pool, `UPDATE collections SET bucket_id = $2 WHERE id = $1`, rf.collectionID, target)
	var queued int
	if err := rf.pool.QueryRow(ctx, `SELECT count(*) FROM object_replicas WHERE source_bucket_id = $1`, target).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 0 {
		t.Fatalf("a rebind queued %d objects for the bucket, want none: their bytes did not move", queued)
	}
	mustExec(t, ctx, rf.pool, `UPDATE objects SET placement_bucket_id = $2 WHERE collection_id = $1`, rf.collectionID, target)
	if err := rf.pool.QueryRow(ctx, `SELECT count(*) FROM object_replicas WHERE source_bucket_id = $1`, target).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 1 {
		t.Errorf("queued %d objects for the bucket after the move, want the moved object", queued)
	}
}

// A claimed row is not claimed again until it is given back; a copy recorded
// at the claim's generation settles it, while a write since the claim leaves
// it due.
func TestObjectReplicasSettleOnlyAtTheClaimedGeneration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rf := newReplicaFixture(t, ctx)
	rf.enable(t, ctx)
	id := rf.available(t, ctx, "a")

	claims := rf.claim(t, ctx)
	if len(claims) != 1 {
		t.Fatalf("claims = %d, want 1", len(claims))
	}
	if again := rf.claim(t, ctx); len(again) != 0 {
		t.Fatalf("a claimed row was claimed again: %+v", again)
	}
	// A write lands while the row is claimed.
	mustExec(t, ctx, rf.pool, `UPDATE objects SET bytes_written_at = clock_timestamp() WHERE id = $1`, id)
	if again := rf.claim(t, ctx); len(again) != 0 {
		t.Fatalf("a write took a claimed row out of its lease: %+v", again)
	}
	objs, err := rf.store.ReplicaObjects(ctx, []uuid.UUID{id})
	if err != nil {
		t.Fatal(err)
	}
	if err := rf.store.RecordReplicaCopy(ctx, claims[0], worker.ReplicaCopy{
		BucketID: rf.bucketID, ObjectID: id, WrittenAt: objs[id].WrittenAt, SizeBytes: 1,
	}); err != nil {
		t.Fatal(err)
	}
	next := rf.claim(t, ctx)
	if len(next) != 1 || next[0].Generation <= claims[0].Generation || !next[0].Replica.WrittenAt.Equal(objs[id].WrittenAt) {
		t.Fatalf("after a write during the copy: claims = %+v, want the row due at a newer generation with the copy recorded", next)
	}
	if err := rf.store.RecordReplicaCopy(ctx, next[0], worker.ReplicaCopy{
		BucketID: rf.bucketID, ObjectID: id, WrittenAt: objs[id].WrittenAt, SizeBytes: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if n := rf.dueCount(t, ctx); n != 0 {
		t.Errorf("due = %d after a copy at the claimed generation", n)
	}
}

// A change to a field the filter reads queues the object; one to any other
// field does not. A move to the trash queues nothing; a restore does.
func TestObjectReplicasQueueOnlyWhatAReplicaDependsOn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rf := newReplicaFixture(t, ctx)
	rf.enable(t, ctx)
	id := rf.available(t, ctx, "a")
	settleAll := func() {
		for _, c := range rf.claim(t, ctx) {
			if err := rf.store.SettleReplica(ctx, c); err != nil {
				t.Fatal(err)
			}
		}
	}
	settleAll()
	for _, tc := range []struct {
		name, sql string
		due       bool
	}{
		{"a tag", `UPDATE objects SET tags = '{"k":"v"}'::jsonb WHERE id = $1`, true},
		{"a public url", `UPDATE objects SET public_url = 'https://x' WHERE id = $1`, false},
		{"the trash", `UPDATE objects SET state = 'DELETED', terminated_at = now() WHERE id = $1`, false},
		{"a restore", `UPDATE objects SET state = 'AVAILABLE', terminated_at = NULL WHERE id = $1`, true},
	} {
		mustExec(t, ctx, rf.pool, tc.sql, id)
		if due := rf.dueCount(t, ctx) == 1; due != tc.due {
			t.Errorf("%s: due = %v, want %v", tc.name, due, tc.due)
		}
		settleAll()
	}
}

// A purge marks the rows its object owns, and only those: once a new object
// takes over the location, the old one's purge leaves the row alone.
func TestObjectReplicasPurgeMarksOnlyItsOwnLocation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rf := newReplicaFixture(t, ctx)
	rf.enable(t, ctx)
	gone := rf.available(t, ctx, "a")
	kept := rf.available(t, ctx, "b")
	for _, c := range rf.claim(t, ctx) {
		if err := rf.store.SettleReplica(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(t, ctx, rf.pool, `DELETE FROM objects WHERE id = $1`, gone)
	claims := rf.claim(t, ctx)
	if len(claims) != 1 || claims[0].ObjectID != gone || claims[0].Key != "a" {
		t.Fatalf("claims after a purge = %+v, want the purged object's location", claims)
	}
	objs, err := rf.store.ReplicaObjects(ctx, []uuid.UUID{gone, kept})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := objs[gone]; ok || len(objs) != 1 {
		t.Fatalf("objects = %v, want only the kept one", objs)
	}
	if err := rf.store.ForgetReplica(ctx, claims[0]); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := rf.pool.QueryRow(ctx, `SELECT count(*) FROM object_replicas WHERE source_bucket_id = $1`, rf.bucketID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("rows = %d after forgetting the purged object, want the kept one", rows)
	}

	// A new object at a purged key owns the row; a forget at the old
	// generation gives the row back due instead of dropping it.
	old := rf.available(t, ctx, "c")
	stale := rf.claim(t, ctx)
	mustExec(t, ctx, rf.pool, `DELETE FROM objects WHERE id = $1`, old)
	fresh := rf.available(t, ctx, "c")
	if err := rf.store.ForgetReplica(ctx, stale[0]); err != nil {
		t.Fatal(err)
	}
	mustExec(t, ctx, rf.pool, `DELETE FROM objects WHERE id = $1`, kept) // marks b, not c
	claims = rf.claim(t, ctx)
	owners := map[string]uuid.UUID{}
	for _, c := range claims {
		owners[c.Key] = c.ObjectID
	}
	if owners["c"] != fresh || owners["b"] != kept || len(owners) != 2 {
		t.Errorf("owners = %v, want c owned by the new object and b by its purged one", owners)
	}
}

// A failed attempt puts the row off and counts it; a tenant in the trash
// waits.
func TestObjectReplicasRetryAndFrozenTenants(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rf := newReplicaFixture(t, ctx)
	rf.enable(t, ctx)
	rf.available(t, ctx, "a")
	claims := rf.claim(t, ctx)
	if err := rf.store.RetryReplica(ctx, claims[0], replicaTestRetryDelay, "access denied"); err != nil {
		t.Fatal(err)
	}
	var attempts int
	var reason string
	var putOff bool
	if err := rf.pool.QueryRow(ctx, `SELECT attempts, last_error, next_attempt_at > now() + interval '30 minutes'
		FROM object_replicas WHERE source_bucket_id = $1`, rf.bucketID).Scan(&attempts, &reason, &putOff); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || reason != "access denied" || !putOff {
		t.Errorf("after a retry: attempts=%d reason=%q put off=%v", attempts, reason, putOff)
	}
	if again := rf.claim(t, ctx); len(again) != 0 {
		t.Errorf("a put-off row was claimed: %+v", again)
	}

	// A write during a failed attempt is tried afresh, not put off.
	written := rf.available(t, ctx, "w")
	claims = rf.claim(t, ctx)
	mustExec(t, ctx, rf.pool, `UPDATE objects SET bytes_written_at = clock_timestamp() WHERE id = $1`, written)
	if err := rf.store.RetryReplica(ctx, claims[0], replicaTestRetryDelay, "timeout"); err != nil {
		t.Fatal(err)
	}
	if again := rf.claim(t, ctx); len(again) != 1 || again[0].Attempts != 0 {
		t.Fatalf("after a write during a failed attempt: claims = %+v, want the row due afresh", again)
	}

	rf.available(t, ctx, "b")
	mustExec(t, ctx, rf.pool, `UPDATE tenants SET deleted_at = now() WHERE id = $1`, rf.tenantID)
	if frozen := rf.claim(t, ctx); len(frozen) != 0 {
		t.Errorf("a trashed tenant's row was claimed: %+v", frozen)
	}
}

// The trigger writes under the application role, whose policy pins writes to
// the session's tenant: a tenant-scoped promote queues its own object.
func TestObjectReplicasQueueUnderTheApplicationRole(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rf := newReplicaFixture(t, ctx)
	rf.enable(t, ctx)
	id := seedPendingObject(t, ctx, rf.pool, rf.fixture)

	tx, err := rf.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE paladin_app`); err != nil {
		t.Fatalf("set role: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('paladin.tenant_id', $1, true)`, rf.tenantID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE objects SET state = 'AVAILABLE', bytes_written_at = clock_timestamp() WHERE id = $1`, id); err != nil {
		t.Fatalf("promote as the application: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if claims := rf.claim(t, ctx); len(claims) != 1 || claims[0].ObjectID != id {
		t.Errorf("claims = %+v, want the object the application promoted", claims)
	}
}
