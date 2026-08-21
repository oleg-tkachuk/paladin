//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Index-usage guards.
//
// An index nobody's plan picks is pure cost: it slows every write, occupies
// disk, and reads as coverage it does not provide. These tests seed enough
// rows for the planner to have a real choice, ANALYZE so it has statistics
// to choose with, then EXPLAIN the query the index was added for and assert
// the plan actually uses it.
//
// Deliberately NOT run with enable_seqscan=off — forcing the plan would
// prove only that the index *can* be used, which is never in question. The
// claim under test is that the planner *prefers* it on realistic data.
//
// These also work as regression tests in the other direction: drop one of
// these indexes and the corresponding test fails with the plan that
// replaced it, so a future "index cleanup" cannot silently reintroduce a
// sequential scan on a hot path.

// explain (audit_action_index_test.go) runs EXPLAIN (ANALYZE, BUFFERS) and
// flattens the plan — reused here rather than duplicated.

// assertPlanUses fails with the whole plan when `index` is absent — the plan
// is the useful part of the failure, so it always gets printed.
func assertPlanUses(t *testing.T, plan, index, what string) {
	t.Helper()
	if !strings.Contains(plan, index) {
		t.Errorf("%s does not use %s.\n\nPlan:\n%s", what, index, plan)
	}
}

func assertPlanAvoidsSeqScan(t *testing.T, plan, table, what string) {
	t.Helper()
	if strings.Contains(plan, "Seq Scan on "+table) {
		t.Errorf("%s falls back to a sequential scan on %s.\n\nPlan:\n%s", what, table, plan)
	}
}

// TestIndexUsage_ObjectsKeysetPagination covers migration 064. ListObjects is
// the console's object browser: equality on (tenant_id, collection), a keyset
// cursor on id, ordered by object_id.
//
// Seeded across MANY Collections on purpose. With a single Collection the
// primary key is an adequate fallback — every row matches the filter, so a
// PK scan short-circuits on the LIMIT immediately — and the test would pass
// without proving anything. Spread the same rows over 20 Collections and the
// PK scan has to skip ~19 rows for every one it keeps, which is the shape a
// real tenant has.
func TestIndexUsage_ObjectsKeysetPagination(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	keys := seedCollections(t, ctx, pool, f, 20)
	for _, k := range keys {
		seedObjectsUnder(t, ctx, pool, f, k, 500, "AVAILABLE")
	}
	analyze(t, ctx, pool, "objects")

	var probeCollectionID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM collections WHERE tenant_id = $1 AND name = $2`,
		f.tenantID, keys[0]).Scan(&probeCollectionID); err != nil {
		t.Fatalf("resolve probe collection: %v", err)
	}
	plan := explain(t, ctx, pool, `
		SELECT id FROM objects
		 WHERE tenant_id = $1 AND collection_id = $2 AND id > $3
		 ORDER BY id
		 LIMIT 50`, f.tenantID, probeCollectionID, uuid.Nil)

	assertPlanUses(t, plan, "idx_objects_keyset", "ListObjects keyset page")
	assertPlanAvoidsSeqScan(t, plan, "objects", "ListObjects keyset page")
	if strings.Contains(plan, "Sort") {
		t.Errorf("ListObjects still sorts — the index should supply the ordering.\n\nPlan:\n%s", plan)
	}

	// An empty Collection is the size-independent case: there is nothing for a
	// PK scan's LIMIT to stop on, so it reads to the end of the table.
	emptyName := seedCollections(t, ctx, pool, f, 1)[0]
	var empty uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM collections WHERE tenant_id = $1 AND name = $2`,
		f.tenantID, emptyName).Scan(&empty); err != nil {
		t.Fatalf("resolve empty collection: %v", err)
	}
	plan = explain(t, ctx, pool, `
		SELECT id FROM objects
		 WHERE tenant_id = $1 AND collection_id = $2 AND id > $3
		 ORDER BY id
		 LIMIT 50`, f.tenantID, empty, uuid.Nil)

	assertPlanUses(t, plan, "idx_objects_keyset", "ListObjects on an empty Collection")
	assertPlanAvoidsSeqScan(t, plan, "objects", "ListObjects on an empty Collection")
}

// TestIndexUsage_ObjectsHardDeletable covers migration 065. The hard-deleter
// looks for a small set of soft-deleted rows inside a large live table — the
// case a partial index is for. Before it, this was a full scan of `objects`.
func TestIndexUsage_ObjectsHardDeletable(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	seedObjects(t, ctx, pool, f, 3000, "AVAILABLE")
	// A realistic ratio: reclaim backlog is a small fraction of live data.
	mustExec(t, ctx, pool, `
		UPDATE objects SET state = 'DELETED', terminated_at = now() - interval '30 days'
		 WHERE tenant_id = $1
		   AND id IN (SELECT id FROM objects WHERE tenant_id = $1 LIMIT 100)`,
		f.tenantID)
	analyze(t, ctx, pool, "objects")

	plan := explain(t, ctx, pool, `
		SELECT id FROM objects
		 WHERE state = 'DELETED' AND terminated_at IS NOT NULL AND terminated_at < now()
		 ORDER BY terminated_at
		 LIMIT 100`)

	assertPlanUses(t, plan, "idx_objects_hard_deletable", "ListHardDeletable")
	assertPlanAvoidsSeqScan(t, plan, "objects", "ListHardDeletable")
}

// TestIndexUsage_MultipartReaper covers migration 066 — an age predicate that
// had no index at all on multipart_uploads.
func TestIndexUsage_MultipartReaper(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	ids := seedObjects(t, ctx, pool, f, 2000, "PENDING")
	for i, oid := range ids {
		mustExec(t, ctx, pool,
			`INSERT INTO multipart_uploads
			   (id, tenant_id, object_id, storage_upload_id, part_size_bytes,
			    total_parts, bucket_id, initiated_by_subject, initiated_by_kind, created_at)
			 SELECT $1, o.tenant_id, $2, 's3-upload', 5242880, 4, c.bucket_id,
			        'test', 'user', now() - make_interval(hours => $3)
			   FROM objects o JOIN collections c ON c.id = o.collection_id
			  WHERE o.id = $2`,
			uuid.Must(uuid.NewV7()), oid, i%200)
	}
	analyze(t, ctx, pool, "multipart_uploads")

	plan := explain(t, ctx, pool, `
		SELECT id FROM multipart_uploads
		 WHERE created_at < now() - interval '72 hours'
		 ORDER BY created_at
		 LIMIT 100`)

	assertPlanUses(t, plan, "idx_multipart_uploads_created", "ListStaleMultipartUploads")
	assertPlanAvoidsSeqScan(t, plan, "multipart_uploads", "ListStaleMultipartUploads")
}

// TestIndexUsage_OperationsKeysetPagination covers migration 067. The
// pre-existing idx_operations_tenant_state cannot serve this ordering because
// `state` sits between the equality column and the sort column.
//
// Scale matters here in a way it did not for the objects indexes, and the
// number below was measured rather than guessed. `operations` is keyed by a
// UUIDv7 primary key, so the PK already returns rows in creation order — on a
// small table the planner correctly prefers scanning it and filtering by
// tenant, because 50 random heap fetches through a secondary index cost more
// than reading a couple of thousand rows sequentially. The crossover is real:
// at 8k rows over 40 tenants the planner picks the PK; at 60k over 200 it
// picks this index, and as an Index ONLY Scan.
//
// The second case below is the one that justifies the index independently of
// table size: a tenant with no recent operations. Filtering the PK scan for it
// finds nothing to stop on, so the LIMIT never short-circuits and the query
// reads the table to the end. That is a quiet tenant paying for everyone
// else's volume, and it gets worse as the fleet grows.
func TestIndexUsage_OperationsKeysetPagination(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)

	const tenantCount = 200
	quiet := seedTenants(t, ctx, pool, tenantCount-1)
	// 300 operations per tenant, interleaved by the UUIDv7 ordering.
	mustExec(t, ctx, pool, `
		INSERT INTO operations (id, tenant_id, type, state)
		SELECT gen_random_uuid(), t.id, 'BatchDelete', 'PENDING'
		  FROM generate_series(1, 300) g, tenants t`)
	analyze(t, ctx, pool, "operations")

	plan := explain(t, ctx, pool, `
		SELECT id FROM operations
		 WHERE tenant_id = $1 AND id > $2
		 ORDER BY id
		 LIMIT 50`, f.tenantID, uuid.Nil)

	assertPlanUses(t, plan, "idx_operations_tenant_keyset", "ListOperations keyset page")
	assertPlanAvoidsSeqScan(t, plan, "operations", "ListOperations keyset page")

	// The quiet-tenant case: no rows to find, so nothing bounds a PK scan.
	mustExec(t, ctx, pool, `DELETE FROM operations WHERE tenant_id = $1`, quiet)
	analyze(t, ctx, pool, "operations")

	plan = explain(t, ctx, pool, `
		SELECT id FROM operations
		 WHERE tenant_id = $1 AND id > $2
		 ORDER BY id
		 LIMIT 50`, quiet, uuid.Nil)

	assertPlanUses(t, plan, "idx_operations_tenant_keyset", "ListOperations for a quiet tenant")
	assertPlanAvoidsSeqScan(t, plan, "operations", "ListOperations for a quiet tenant")
}

// TestIndexUsage_OutboxDepthGauge covers migration 068. The gauge groups
// pending deliveries by tenant; without tenant_id in a partial index the
// grouping had to visit the heap for every pending row.
func TestIndexUsage_OutboxDepthGauge(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	// event_deliveries.subscription_id is a real FK, so the parent row has to
	// exist before the outbox rows do.
	subID := uuid.New()
	mustExec(t, ctx, pool,
		`INSERT INTO event_subscriptions (id, tenant_id, sink_kind, sink_config)
		 VALUES ($1, $2, 'http', '{"url": "https://example.invalid/hook"}'::jsonb)`,
		subID, f.tenantID)
	for i := 0; i < 3000; i++ {
		status := "delivered"
		if i%4 == 0 { // a realistic minority still pending
			status = "pending"
		}
		mustExec(t, ctx, pool,
			`INSERT INTO event_deliveries
			   (id, tenant_id, subscription_id, event_type, event_at, event_payload, status)
			 VALUES ($1, $2, $3, 'object.created', now(), '{}'::jsonb, $4)`,
			uuid.New(), f.tenantID, subID, status)
	}
	analyze(t, ctx, pool, "event_deliveries")

	plan := explain(t, ctx, pool, `
		SELECT count(*) FROM event_deliveries WHERE status = 'pending' GROUP BY tenant_id`)

	assertPlanUses(t, plan, "event_deliveries_pending_tenant_idx", "OutboxDepth gauge")
	assertPlanAvoidsSeqScan(t, plan, "event_deliveries", "OutboxDepth gauge")
}

// seedTenants inserts n extra tenants and returns one of them, so a test can
// single out a tenant whose data it later removes.
func seedTenants(t *testing.T, ctx context.Context, pool *pgxpool.Pool, n int) uuid.UUID {
	t.Helper()
	var picked uuid.UUID
	for i := 0; i < n; i++ {
		id := uuid.New()
		hex := uuid.NewString()[:8]
		mustExec(t, ctx, pool,
			`INSERT INTO tenants (id, slug, display_name) VALUES ($1, $2, $3)`,
			id, "t-"+hex, "tn-"+hex)
		if i == n/2 {
			picked = id
		}
	}
	return picked
}

// uuidMax is the largest UUID — the starting cursor for a descending
// keyset walk, where uuid.Nil is the ascending one.
var uuidMax = uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")

// ─── helpers ────────────────────────────────────────────────────────────────

// seedObjects bulk-inserts n objects under the fixture's Collection and returns
// their ids. generate_series keeps the seed a single round-trip — at these row
// counts a per-row insert loop dominates the test's runtime.
func seedObjects(t *testing.T, ctx context.Context, pool *pgxpool.Pool, f fixture, n int, state string) []uuid.UUID {
	t.Helper()
	seedObjectsUnder(t, ctx, pool, f, f.collection, n, state)

	rows, err := pool.Query(ctx,
		`SELECT o.id FROM objects o
		   JOIN collections c ON c.id = o.collection_id
		  WHERE o.tenant_id = $1 AND c.name = $2`,
		f.tenantID, f.collection)
	if err != nil {
		t.Fatalf("read seeded ids: %v", err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan id: %v", err)
		}
		out = append(out, id)
	}
	return out
}

// seedObjectsUnder bulk-inserts n objects under an explicit Collection.
// generate_series keeps the seed a single round-trip — at these row counts a
// per-row insert loop dominates the test's runtime.
func seedObjectsUnder(t *testing.T, ctx context.Context, pool *pgxpool.Pool, f fixture, collection string, n int, state string) {
	t.Helper()
	mustExec(t, ctx, pool, `
		INSERT INTO objects (id, tenant_id, collection_id, path, state,
		                     content_type, checksum_algorithm, size_bytes)
		SELECT gen_random_uuid(), $1, c.id, 'k-' || g, $3::object_state,
		       'application/octet-stream', 0, 100
		  FROM generate_series(1, $4) AS g
		 CROSS JOIN collections c
		 WHERE c.tenant_id = $1 AND c.name = $2`,
		f.tenantID, collection, state, n)
}

// seedCollections creates n additional Collections bound to the fixture's
// bucket and returns their names.
func seedCollections(t *testing.T, ctx context.Context, pool *pgxpool.Pool, f fixture, n int) []string {
	t.Helper()
	// One lookup of the fixture's bucket, reused for every collection below.
	var bucketID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT bucket_id FROM collections WHERE tenant_id = $1 AND name = $2`,
		f.tenantID, f.collection).Scan(&bucketID); err != nil {
		t.Fatalf("lookup fixture binding: %v", err)
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		name := "ok-" + uuid.NewString()[:8]
		mustExec(t, ctx, pool,
			`INSERT INTO collections (tenant_id, name, bucket_id) VALUES ($1, $2, $3)`,
			f.tenantID, name, bucketID)
		out = append(out, name)
	}
	return out
}

// analyze refreshes planner statistics. Without it the planner works from
// defaults and its choice says nothing about the real shape of the data.
func analyze(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string) {
	t.Helper()
	mustExec(t, ctx, pool, "ANALYZE "+table)
}
