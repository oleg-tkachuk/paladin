//go:build integration

package components

import (
	"context"
	"strings"
	"sync"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

func (s *fakeObjectStore) sizeOf(l worker.CopyLocation) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	size, ok := s.objects[storeKey(l)]
	return size, ok
}

func (f publicMigrationFixture) loc(bucket, collection, key string) worker.CopyLocation {
	return worker.CopyLocation{BackendID: f.backend, TenantID: f.tenant, Bucket: bucket, Collection: collection, Key: key}
}

// upload is a tenant write: an AVAILABLE object row, and its bytes in the
// bucket the collection is bound to now.
func (f publicMigrationFixture) upload(t *testing.T, ctx context.Context, collection, key string, size int64) {
	t.Helper()
	mustExec(t, ctx, f.pool, `INSERT INTO objects (tenant_id, collection_id, path, state, content_type, size_bytes)
		SELECT $1, c.id, $3, 'AVAILABLE', 'text/plain', $4 FROM collections c
		 WHERE c.tenant_id = $1 AND c.name = $2`, f.tenant, collection, key, size)
	f.store.put(f.loc(f.boundTo(t, ctx, collection), collection, key), size)
}

func (f publicMigrationFixture) objectState(t *testing.T, ctx context.Context, collection, key string) string {
	t.Helper()
	var state string
	if err := f.pool.QueryRow(ctx, `SELECT o.state FROM objects o JOIN collections c ON c.id = o.collection_id
		WHERE o.tenant_id = $1 AND c.name = $2 AND o.path = $3`, f.tenant, collection, key).Scan(&state); err != nil {
		t.Fatalf("read object %s/%s: %v", collection, key, err)
	}
	return state
}

func (f publicMigrationFixture) defaultBinding(t *testing.T, ctx context.Context) string {
	t.Helper()
	var bucket string
	if err := f.pool.QueryRow(ctx, `SELECT b.name FROM tenant_default_bindings d JOIN buckets b ON b.id = d.bucket_id
		WHERE d.tenant_id = $1`, f.tenant).Scan(&bucket); err != nil {
		t.Fatalf("read default binding: %v", err)
	}
	return bucket
}

// assertOnSource checks the tenant is wholly back on its source bucket.
func (f publicMigrationFixture) assertOnSource(t *testing.T, ctx context.Context, collections ...string) {
	t.Helper()
	if got := f.layout(t, ctx); got != "shared" {
		t.Errorf("storage_layout = %s, want shared", got)
	}
	for _, c := range collections {
		if got := f.boundTo(t, ctx, c); got != f.private {
			t.Errorf("collection %s bound to %s, want the source %s", c, got, f.private)
		}
	}
	if got := f.defaultBinding(t, ctx); got != f.private {
		t.Errorf("default binding is %s, want the source %s", got, f.private)
	}
}

// corruptingCopier copies through the store, except that the first copy of
// one object into the target lands short — the broken copy the physical
// verify exists to catch.
type corruptingCopier struct {
	*fakeObjectStore
	once   sync.Once
	target worker.CopyLocation
}

func (c *corruptingCopier) CopyObject(ctx context.Context, src, dst worker.CopyLocation) error {
	if err := c.fakeObjectStore.CopyObject(ctx, src, dst); err != nil {
		return err
	}
	if dst == c.target {
		c.once.Do(func() { c.put(dst, 1) })
	}
	return nil
}

// hookedHeader runs hook before the first HEAD — the start of the verify,
// with the tenant already rebound — so the test can write as the tenant does
// in that window.
type hookedHeader struct {
	*fakeObjectStore
	once sync.Once
	hook func()
}

func (h *hookedHeader) HeadObject(ctx context.Context, l worker.CopyLocation) (int64, error) {
	h.once.Do(h.hook)
	return h.fakeObjectStore.HeadObject(ctx, l)
}

// A migration whose physical verify failed used to stay 'failed' with the
// tenant rebound: dedicated, on a target missing objects, refused a retry,
// with no way back. It now rolls the tenant back to its source bucket —
// keeping what the tenant wrote to the target after the rebind — and fails,
// so a retry runs it again from the start.
func TestStorageMigrationRollsBackAFailedVerify(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := seedPublicMigrationFixture(t, ctx)
	f.addCollection(t, ctx, "docs", f.private, false)
	f.addCollection(t, ctx, "more", f.private, false)
	f.addCollection(t, ctx, "photos", f.public, true)
	if err := f.start(t, ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	copier := &corruptingCopier{fakeObjectStore: f.store, target: f.loc(f.dedicated, "docs", "obj-1")}
	header := &hookedHeader{fakeObjectStore: f.store, hook: func() {
		// On the worker's goroutine, so no t.Fatal. Rebound: these land in
		// the dedicated bucket. post-1 is a new upload; gone-1 is uploaded
		// and deleted again; more/obj-1, older than the rebind, is deleted.
		for key, size := range map[string]int64{"post-1": 7, "gone-1": 5} {
			if _, err := f.pool.Exec(ctx, `INSERT INTO objects (tenant_id, collection_id, path, state, content_type, size_bytes)
				SELECT $1, c.id, $2, 'AVAILABLE', 'text/plain', $3 FROM collections c
				 WHERE c.tenant_id = $1 AND c.name = 'docs'`, f.tenant, key, size); err != nil {
				t.Errorf("upload %s: %v", key, err)
			}
			f.store.put(f.loc(f.dedicated, "docs", key), size)
		}
		for coll, key := range map[string]string{"docs": "gone-1", "more": "obj-1"} {
			if _, err := f.pool.Exec(ctx, `UPDATE objects o SET state = 'DELETED', terminated_at = now()
				FROM collections c WHERE c.id = o.collection_id
				  AND o.tenant_id = $1 AND c.name = $2 AND o.path = $3`, f.tenant, coll, key); err != nil {
				t.Errorf("delete %s/%s: %v", coll, key, err)
			}
		}
	}}

	state, reason := f.driveWith(t, ctx, copier, header)
	if state != worker.MigStateFailed || !strings.Contains(reason, "physical verify") {
		t.Fatalf("migration left at %s (%q), want failed with the verify error", state, reason)
	}
	f.assertOnSource(t, ctx, "docs", "more")
	if got := f.boundTo(t, ctx, "photos"); got != f.public {
		t.Errorf("public collection bound to %s, want it left in %s", got, f.public)
	}
	if size, ok := f.store.sizeOf(f.loc(f.private, "docs", "post-1")); !ok || size != 7 {
		t.Errorf("the upload made after the rebind is not in the source (size %d, present %v)", size, ok)
	}
	// The broken target copy was not copied over the good source one.
	if size, _ := f.store.sizeOf(f.loc(f.private, "docs", "obj-1")); size != 42 {
		t.Errorf("source docs/obj-1 is %d bytes, want its original 42", size)
	}
	// Deletes stay deletes: nothing copied back, nothing made visible.
	if f.store.has(f.loc(f.private, "docs", "gone-1")) {
		t.Error("an object deleted after the rebind was copied back to the source")
	}
	for _, o := range [][2]string{{"docs", "gone-1"}, {"more", "obj-1"}} {
		if got := f.objectState(t, ctx, o[0], o[1]); got != "DELETED" {
			t.Errorf("%s/%s is %s after the rollback, want DELETED", o[0], o[1], got)
		}
	}
	if m := f.onlyMigration(t, ctx); m.attempts != 1 {
		t.Errorf("attempts = %d, want the verify failure counted once", m.attempts)
	}

	// Shared and failed: the ordinary retry, through to cleaned.
	if err := f.startRaw(ctx); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if state, reason := f.drive(t, ctx); state != worker.MigStateCleaned {
		t.Fatalf("retried migration left at %s (%s), want cleaned", state, reason)
	}
	if got := f.layout(t, ctx); got != "dedicated" {
		t.Errorf("storage_layout = %s after the retry, want dedicated", got)
	}
	for _, k := range []string{"obj-1", "post-1"} {
		if !f.store.has(f.loc(f.dedicated, "docs", k)) {
			t.Errorf("docs/%s is not in the dedicated bucket after the retry", k)
		}
	}
	if size, _ := f.store.sizeOf(f.loc(f.dedicated, "docs", "obj-1")); size != 42 {
		t.Errorf("dedicated docs/obj-1 is %d bytes after the retry, want 42", size)
	}
}

// allowAll authorizes everything: the RPC's authorization is not under test.
type allowAll struct{}

func (allowAll) IsAuthorized(context.Context, *cedar.Principal, cedar.Action, *cedar.Resource, cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionAllow, nil
}

func (f publicMigrationFixture) migrateRPC(ctx context.Context) (*tenanth.StorageMigration, error) {
	h := tenanth.NewHandler(adapters.NewTenantRepo(sqlc.New(f.pool), f.pool), allowAll{})
	ctx = auth.WithPrincipal(ctx, &auth.Principal{
		Subject: "operator", TenantID: uuid.New(), Roles: []string{apiutil.RolePlatformAdmin},
	})
	return h.MigrateTenantStorageLayout(ctx, f.tenant, "", 0)
}

// stuckAfterVerify leaves f's tenant as a verify failure did before
// rollbacks: rebound to the dedicated bucket by the real rebind, the
// migration 'failed' with no rebound_at — and an upload made after the
// rebind, in the dedicated bucket only.
func (f publicMigrationFixture) stuckAfterVerify(t *testing.T, ctx context.Context) {
	t.Helper()
	if err := f.start(t, ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	f.store.put(f.loc(f.dedicated, "docs", "obj-1"), 1) // the broken copy
	mustExec(t, ctx, f.pool, `UPDATE tenant_storage_migrations
		SET state = 'rebinding', objects_total = 1, objects_copied = 1 WHERE tenant_id = $1`, f.tenant)
	repo := adapters.NewStorageMigrationRepo(sqlc.New(f.pool), f.pool)
	if err := repo.RebindTenant(ctx, worker.MigrationRead{TenantID: f.tenant, State: worker.MigStateRebinding},
		f.backend, f.dedicated); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	f.upload(t, ctx, "docs", "post-1", 7)
	mustExec(t, ctx, f.pool, `UPDATE tenant_storage_migrations
		SET state = 'failed', error = 'physical verify: docs/obj-1 size mismatch', attempts = 1, rebound_at = NULL
		WHERE tenant_id = $1`, f.tenant)
}

// A tenant stuck dedicated by such a failure is rolled back by the same RPC
// that retries a failed migration, and is then retried by it as usual.
func TestStorageMigrationRPCRollsBackAStuckTenant(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("rolled back, then retried", func(t *testing.T) {
		t.Parallel()
		f := seedPublicMigrationFixture(t, ctx)
		f.addCollection(t, ctx, "docs", f.private, false)
		f.stuckAfterVerify(t, ctx)

		m, err := f.migrateRPC(ctx)
		if err != nil {
			t.Fatalf("RPC on the stuck tenant: %v", err)
		}
		if m.State != worker.MigStateRollingBack || m.TargetBucketName != f.dedicated {
			t.Errorf("RPC returned %s for %s, want rolling_back for %s", m.State, m.TargetBucketName, f.dedicated)
		}
		if state, reason := f.drive(t, ctx); state != worker.MigStateFailed || !strings.Contains(reason, "physical verify") {
			t.Fatalf("rollback left the migration at %s (%q), want failed with the verify error", state, reason)
		}
		f.assertOnSource(t, ctx, "docs")
		if size, ok := f.store.sizeOf(f.loc(f.private, "docs", "post-1")); !ok || size != 7 {
			t.Errorf("the upload made after the rebind is not in the source (size %d, present %v)", size, ok)
		}
		if size, _ := f.store.sizeOf(f.loc(f.private, "docs", "obj-1")); size != 42 {
			t.Errorf("source docs/obj-1 is %d bytes, want its original 42", size)
		}

		m, err = f.migrateRPC(ctx)
		if err != nil {
			t.Fatalf("retry RPC: %v", err)
		}
		if m.State != worker.MigStateProvisioning {
			t.Errorf("retry RPC returned %s, want provisioning", m.State)
		}
		// The RPC names the dedicated bucket after the tenant id, not the
		// fixture's name; the reconciler's part is to make it ready.
		mustExec(t, ctx, f.pool, `UPDATE buckets SET provision_state = 'ready' WHERE name = $1`, m.TargetBucketName)
		// Inside the hour drive's clock runs ahead, so cleanup runs too.
		mustExec(t, ctx, f.pool, `UPDATE tenant_storage_migrations SET cleanup_retention_seconds = 60 WHERE tenant_id = $1`, f.tenant)
		if state, reason := f.drive(t, ctx); state != worker.MigStateCleaned {
			t.Fatalf("retried migration left at %s (%s), want cleaned", state, reason)
		}
		if got := f.boundTo(t, ctx, "docs"); got != m.TargetBucketName {
			t.Errorf("docs bound to %s after the retry, want %s", got, m.TargetBucketName)
		}
		for key, want := range map[string]int64{"obj-1": 42, "post-1": 7} {
			if size, _ := f.store.sizeOf(f.loc(m.TargetBucketName, "docs", key)); size != want {
				t.Errorf("dedicated docs/%s is %d bytes after the retry, want %d", key, size, want)
			}
		}
	})

	// Only that leftover: a tenant an operator has since pointed elsewhere
	// is refused, as is a dedicated tenant whose migration did not fail.
	for name, setup := range map[string]func(t *testing.T, f publicMigrationFixture){
		"default binding moved off the target": func(t *testing.T, f publicMigrationFixture) {
			t.Helper()
			mustExec(t, ctx, f.pool, `UPDATE tenant_default_bindings SET bucket_id = (SELECT id FROM buckets WHERE name = $2)
				WHERE tenant_id = $1`, f.tenant, f.private)
		},
		"migration completed": func(t *testing.T, f publicMigrationFixture) {
			t.Helper()
			mustExec(t, ctx, f.pool, `UPDATE tenant_storage_migrations SET state = 'completed' WHERE tenant_id = $1`, f.tenant)
		},
	} {
		t.Run("refused: "+name, func(t *testing.T) {
			t.Parallel()
			f := seedPublicMigrationFixture(t, ctx)
			f.addCollection(t, ctx, "docs", f.private, false)
			f.stuckAfterVerify(t, ctx)
			setup(t, f)
			before := f.onlyMigration(t, ctx)
			if _, err := f.migrateRPC(ctx); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("RPC = %v, want FailedPrecondition", err)
			}
			if m := f.onlyMigration(t, ctx); m != before {
				t.Errorf("refused RPC changed the migration: %+v, was %+v", m, before)
			}
			if got := f.layout(t, ctx); got != "dedicated" {
				t.Errorf("storage_layout = %s, want dedicated", got)
			}
		})
	}
}
