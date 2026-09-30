//go:build integration

package components

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// A multipart session is reachable by ON DELETE CASCADE from objects and from
// tenants. A cascade runs inside the database and cannot call S3, so before
// the abort-debt trigger those deletions left the upload open forever — parts
// accruing storage charges, invisible to the reaper that exists to close
// exactly that leak. The bucket FK was RESTRICT, so the hazard was understood
// on one path and missed on the other two.
func TestMultipartAbortDebt(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)

	seed := func(t *testing.T) (tenant, object, upload uuid.UUID, storageID string) {
		t.Helper()
		tenant, _ = mkTenant(t, ctx, pool, "shared")
		backend := "be-" + uuid.NewString()[:8]
		bucket := "bk-" + uuid.NewString()[:8]
		mustExec(t, ctx, pool,
			`INSERT INTO storage_backends (name, kind, provider, endpoint, region)
			 VALUES ($1,'s3-compatible','garage','http://x.invalid:3900','us-east-1')`, backend)
		mustExec(t, ctx, pool,
			`INSERT INTO buckets (backend_id, name)
			 SELECT id, $2 FROM storage_backends WHERE name = $1`, backend, bucket)
		collection := "col-" + uuid.NewString()[:8]
		mustExec(t, ctx, pool,
			`INSERT INTO collections (tenant_id, name, bucket_id)
			 SELECT $1, $2, id FROM buckets WHERE name = $3`, tenant, collection, bucket)
		object = uuid.New()
		mustExec(t, ctx, pool,
			`INSERT INTO objects (id, tenant_id, collection_id, path, state, content_type)
			 SELECT $1, $2, id, $4, 'PENDING', 'application/octet-stream' FROM collections WHERE tenant_id = $2 AND name = $3`,
			object, tenant, collection, "k-"+uuid.NewString()[:8])
		upload = uuid.New()
		storageID = "s3-upload-" + uuid.NewString()[:8]
		mustExec(t, ctx, pool,
			`INSERT INTO multipart_uploads
			   (id, tenant_id, object_id, bucket_id, storage_upload_id, part_size_bytes,
			    total_parts, initiated_by_subject, initiated_by_kind, collection_name, path)
			 SELECT $1, $2, $3, b.id, $4, 5242880, 2, 'e2e', 'user', $5, 'k'
			 FROM buckets b WHERE b.name = $6`,
			upload, tenant, object, storageID, collection, bucket)
		return
	}

	debtFor := func(t *testing.T, storageID string) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM pending_multipart_aborts WHERE storage_upload_id = $1`,
			storageID).Scan(&n); err != nil {
			t.Fatalf("count debt: %v", err)
		}
		return n
	}

	t.Run("a cascade from the object files a debt", func(t *testing.T) {
		_, object, _, storageID := seed(t)
		mustExec(t, ctx, pool, `DELETE FROM objects WHERE id = $1`, object)
		if got := debtFor(t, storageID); got != 1 {
			t.Fatalf("debt rows = %d, want 1 — the S3 session would leak", got)
		}
	})

	// The tenant cascade is covered by the same trigger, but it cannot be
	// reached on its own: object_id is NOT NULL and cascades too, so deleting
	// the objects that block the tenant always fires the trigger first. What
	// matters for the tenant path is that the debt SURVIVES it — the debt row
	// carries no tenant FK, deliberately, for exactly this reason.
	t.Run("the debt outlives the tenant whose deletion created it", func(t *testing.T) {
		tenant, object, _, storageID := seed(t)
		mustExec(t, ctx, pool, `DELETE FROM objects WHERE id = $1`, object)
		if got := debtFor(t, storageID); got != 1 {
			t.Fatalf("debt rows = %d, want 1", got)
		}
		mustExec(t, ctx, pool, `DELETE FROM collections WHERE tenant_id = $1`, tenant)
		mustExec(t, ctx, pool, `DELETE FROM users WHERE tenant_id = $1`, tenant)
		mustExec(t, ctx, pool, `DELETE FROM tenants WHERE id = $1`, tenant)
		if got := debtFor(t, storageID); got != 1 {
			t.Errorf("debt rows = %d after the tenant went, want 1 — the obligation "+
				"was wiped and those parts are unreachable", got)
		}
	})

	// The Abort RPC and the reaper call S3 first and set the flag; filing a
	// debt against an upload that was just aborted would have the drainer
	// abort it a second time forever.
	t.Run("a signalled delete files nothing", func(t *testing.T) {
		_, _, upload, storageID := seed(t)
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if _, err := tx.Exec(ctx, "SET LOCAL paladin.multipart_aborted = 'on'"); err != nil {
			t.Fatalf("set flag: %v", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM multipart_uploads WHERE id = $1`, upload); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
		if got := debtFor(t, storageID); got != 0 {
			t.Errorf("debt rows = %d, want 0 — this delete had already aborted on S3", got)
		}
	})
}
