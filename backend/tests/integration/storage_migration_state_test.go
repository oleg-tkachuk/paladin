//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

// migrationTestBackend hosts the source and target buckets.
const migrationTestBackend = "be-migration-state"

// cleanupRetentionSeconds is the retention the test migration is created
// with; the transitions under test do not read it.
const cleanupRetentionSeconds = 3600

// The migration worker's writes used to match the tenant alone. These pin the
// SQL that now refuses a transition from a state the migration has left, and
// a copy count that goes backwards.
func TestStorageMigrationTransitionsApplyOnlyFromTheStateRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := pgharness.Setup(t)
	q := sqlc.New(h.PoolMigrate)
	repo := adapters.NewStorageMigrationRepo(q, h.PoolMigrate)
	seedBackend(t, h.PoolMigrate, migrationTestBackend)
	tenant := mustCreateTenant(t, h.PoolMigrate, "migration-state")
	mustSeedSharedBucket(t, h.PoolMigrate, migrationTestBackend, "mig-shared", tenant)
	mustCreateBucket(t, h.PoolMigrate, tenant, migrationTestBackend, "mig-dedicated")
	if _, err := q.CreateStorageMigration(ctx, pgtype.UUID{Bytes: tenant, Valid: true},
		migrationTestBackend, "mig-shared", migrationTestBackend, "mig-dedicated", cleanupRetentionSeconds); err != nil {
		t.Fatalf("create migration: %v", err)
	}
	read := func() (string, int64) {
		t.Helper()
		var state string
		var copied int64
		if err := h.PoolMigrate.QueryRow(ctx,
			`SELECT state, objects_copied FROM tenant_storage_migrations WHERE tenant_id = $1`, tenant).
			Scan(&state, &copied); err != nil {
			t.Fatalf("read migration: %v", err)
		}
		return state, copied
	}
	moved := func(what string, err error) {
		t.Helper()
		if !errors.Is(err, worker.ErrMigrationMoved) {
			t.Errorf("%s returned %v, want ErrMigrationMoved", what, err)
		}
	}

	if err := repo.SetCopying(ctx, worker.MigrationRead{TenantID: tenant, State: worker.MigStateProvisioning}, 3); err != nil {
		t.Fatalf("provisioning -> copying: %v", err)
	}
	if err := repo.AdvanceCopy(ctx, worker.MigrationRead{TenantID: tenant, State: worker.MigStateCopying}, 3, "c", "d"); err != nil {
		t.Fatalf("advance: %v", err)
	}
	moved("a smaller copy count", repo.AdvanceCopy(ctx, worker.MigrationRead{TenantID: tenant, State: worker.MigStateCopying}, 1, "c", "a"))
	if err := repo.SetState(ctx, worker.MigrationRead{TenantID: tenant, State: worker.MigStateCopying}, worker.MigStateRebinding); err != nil {
		t.Fatalf("copying -> rebinding: %v", err)
	}
	if err := repo.SetState(ctx, worker.MigrationRead{TenantID: tenant, State: worker.MigStateRebinding}, worker.MigStateVerifying); err != nil {
		t.Fatalf("rebinding -> verifying: %v", err)
	}

	// A worker that still believes the migration is copying.
	moved("AdvanceCopy from copying", repo.AdvanceCopy(ctx, worker.MigrationRead{TenantID: tenant, State: worker.MigStateCopying}, 3, "c", "d"))
	moved("SetState from copying", repo.SetState(ctx, worker.MigrationRead{TenantID: tenant, State: worker.MigStateCopying}, worker.MigStateRebinding))
	moved("Fail from copying", repo.Fail(ctx, worker.MigrationRead{TenantID: tenant, State: worker.MigStateCopying}, "stale"))
	moved("SetCopying from provisioning", repo.SetCopying(ctx, worker.MigrationRead{TenantID: tenant, State: worker.MigStateProvisioning}, 0))
	if state, copied := read(); state != worker.MigStateVerifying || copied != 3 {
		t.Fatalf("stale writes left the migration at %s with %d copied, want %s with 3",
			state, copied, worker.MigStateVerifying)
	}

	if err := repo.Complete(ctx, worker.MigrationRead{TenantID: tenant, State: worker.MigStateVerifying}); err != nil {
		t.Fatalf("verifying -> completed: %v", err)
	}
	moved("Fail after completion", repo.Fail(ctx, worker.MigrationRead{TenantID: tenant, State: worker.MigStateVerifying}, "late verify"))
	if err := repo.MarkCleaned(ctx, worker.MigrationRead{TenantID: tenant, State: worker.MigStateCompleted}); err != nil {
		t.Fatalf("completed -> cleaned: %v", err)
	}
	if state, _ := read(); state != worker.MigStateCleaned {
		t.Errorf("state = %s, want %s", state, worker.MigStateCleaned)
	}
}

// The state repeats across attempts: a retry walks the same states again.
// A worker that read the first attempt and resumed after it failed and was
// retried must change nothing — not the retry's progress, and above all not
// the tenant's binding, which a stale rebind would point at a bucket the
// retry has not filled.
func TestStorageMigrationTransitionsApplyOnlyToTheAttemptRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := pgharness.Setup(t)
	q := sqlc.New(h.PoolMigrate)
	repo := adapters.NewStorageMigrationRepo(q, h.PoolMigrate)
	const backend = "be-migration-attempt"
	seedBackend(t, h.PoolMigrate, backend)
	tenant := mustCreateTenant(t, h.PoolMigrate, "migration-attempt")
	mustSeedSharedBucket(t, h.PoolMigrate, backend, "att-shared", tenant)
	mustCreateBucket(t, h.PoolMigrate, tenant, backend, "att-dedicated")
	if _, err := q.CreateStorageMigration(ctx, pgtype.UUID{Bytes: tenant, Valid: true},
		backend, "att-shared", backend, "att-dedicated", cleanupRetentionSeconds); err != nil {
		t.Fatalf("create migration: %v", err)
	}

	// The first attempt reaches rebinding with a worker holding that read,
	// then fails; the retry starts over and is back in each state in turn.
	first := worker.MigrationRead{TenantID: tenant, State: worker.MigStateProvisioning}
	if err := repo.SetCopying(ctx, first, 2); err != nil {
		t.Fatalf("provisioning -> copying: %v", err)
	}
	first.State = worker.MigStateCopying
	if err := repo.SetState(ctx, first, worker.MigStateRebinding); err != nil {
		t.Fatalf("copying -> rebinding: %v", err)
	}
	staleRebinding := worker.MigrationRead{TenantID: tenant, State: worker.MigStateRebinding}
	staleCopying := worker.MigrationRead{TenantID: tenant, State: worker.MigStateCopying}
	if err := repo.Fail(ctx, staleRebinding, "first attempt"); err != nil {
		t.Fatalf("fail the first attempt: %v", err)
	}
	retry := func(state string) {
		t.Helper()
		if _, err := h.PoolMigrate.Exec(ctx, `
			UPDATE tenant_storage_migrations
			   SET state = $2, objects_copied = 0, cursor_collection = '', cursor_path = '', error = ''
			 WHERE tenant_id = $1`, tenant, state); err != nil {
			t.Fatalf("retry into %s: %v", state, err)
		}
	}
	moved := func(what string, err error) {
		t.Helper()
		if !errors.Is(err, worker.ErrMigrationMoved) {
			t.Errorf("%s returned %v, want ErrMigrationMoved", what, err)
		}
	}

	retry(worker.MigStateCopying)
	moved("stale AdvanceCopy", repo.AdvanceCopy(ctx, staleCopying, 2, "c", "z"))
	moved("stale SetState", repo.SetState(ctx, staleCopying, worker.MigStateRebinding))
	moved("stale Fail", repo.Fail(ctx, staleCopying, "stale"))
	var state string
	var copied int64
	var attempts int32
	if err := h.PoolMigrate.QueryRow(ctx,
		`SELECT state, objects_copied, attempts FROM tenant_storage_migrations WHERE tenant_id = $1`, tenant).
		Scan(&state, &copied, &attempts); err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if state != worker.MigStateCopying || copied != 0 || attempts != 1 {
		t.Fatalf("stale writes left the retry at %s, %d copied, attempt %d; want %s, 0, 1",
			state, copied, attempts, worker.MigStateCopying)
	}

	retry(worker.MigStateRebinding)
	moved("stale RebindTenant", repo.RebindTenant(ctx, staleRebinding, backend, "att-dedicated"))
	var layout string
	if err := h.PoolMigrate.QueryRow(ctx, `SELECT storage_layout FROM tenants WHERE id = $1`, tenant).
		Scan(&layout); err != nil {
		t.Fatalf("read layout: %v", err)
	}
	if layout != "shared" {
		t.Errorf("storage_layout = %s after a stale rebind, want shared", layout)
	}

	// The retry's own worker, reading attempt 1, still moves it on.
	current := worker.MigrationRead{TenantID: tenant, State: worker.MigStateRebinding, Attempts: 1}
	if err := repo.SetState(ctx, current, worker.MigStateVerifying); err != nil {
		t.Errorf("the current attempt's transition: %v", err)
	}
}

// The rollback's rebind back is RebindTenant's inverse and held to the same
// guard: only the migration as read (rolling_back, same attempt) under its
// row lock, all of it or none. It also refuses while an object written past
// the rollback cursor has not been copied back, and leaves the public
// collections where they are.
func TestStorageMigrationRebindBackAppliesOnlyToTheAttemptRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := pgharness.Setup(t)
	pool := h.PoolMigrate
	q := sqlc.New(pool)
	repo := adapters.NewStorageMigrationRepo(q, pool)
	const backend = "be-migration-rollback"
	seedBackend(t, pool, backend)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	tenant := mustCreateTenant(t, pool, "migration-rollback")
	// The tenant owns its dedicated bucket and is granted the two it shares
	// with others (ADR-0032), which the rebind back has to be allowed into.
	platform := mustCreateTenant(t, pool, "migration-rollback-owner")
	for _, b := range []struct {
		name   string
		owner  uuid.UUID
		public bool
	}{{"rb-shared", platform, false}, {"rb-dedicated", tenant, false}, {"rb-public", platform, true}} {
		exec(`INSERT INTO buckets (backend_id, name, public_read, owner_tenant_id)
			SELECT sb.id, $2, $3, $4 FROM storage_backends sb WHERE sb.name = $1`, backend, b.name, b.public, b.owner)
		if b.owner != tenant {
			mustGrantBucket(t, pool, tenant, backend, b.name)
		}
	}
	// Rebound: private collection on the dedicated bucket, layout dedicated,
	// default binding there; the public collection never moved.
	exec(`INSERT INTO collections (tenant_id, name, bucket_id) SELECT $1, 'docs', id FROM buckets WHERE name = 'rb-dedicated'`, tenant)
	exec(`INSERT INTO collections (tenant_id, name, bucket_id, public_read, cache_control)
		SELECT $1, 'photos', id, true, 'public, max-age=60' FROM buckets WHERE name = 'rb-public'`, tenant)
	exec(`UPDATE tenants SET storage_layout = 'dedicated' WHERE id = $1`, tenant)
	exec(`INSERT INTO tenant_default_bindings (tenant_id, bucket_id, set_by)
		SELECT $1, id, 'storage-migration' FROM buckets WHERE name = 'rb-dedicated'`, tenant)
	if _, err := q.CreateStorageMigration(ctx, pgtype.UUID{Bytes: tenant, Valid: true},
		backend, "rb-shared", backend, "rb-dedicated", cleanupRetentionSeconds); err != nil {
		t.Fatalf("create migration: %v", err)
	}
	exec(`UPDATE tenant_storage_migrations SET state = 'rolling_back', attempts = 2,
		error = 'physical verify: docs/a size mismatch', rebound_at = now() - interval '1 hour'
		WHERE tenant_id = $1`, tenant)
	// Written after the rebind: only in the dedicated bucket.
	exec(`INSERT INTO objects (tenant_id, collection_id, path, state, content_type, size_bytes)
		SELECT $1, id, 'post', 'AVAILABLE', 'text/plain', 3 FROM collections WHERE tenant_id = $1 AND name = 'docs'`, tenant)

	var reboundAt time.Time
	if err := pool.QueryRow(ctx, `SELECT rebound_at FROM tenant_storage_migrations WHERE tenant_id = $1`, tenant).
		Scan(&reboundAt); err != nil {
		t.Fatalf("read rebound_at: %v", err)
	}
	type after struct {
		state, errMsg, layout, docs, photos, binding string
		attempts                                     int32
	}
	read := func() after {
		t.Helper()
		var a after
		if err := pool.QueryRow(ctx, `
			SELECT m.state, COALESCE(m.error, ''), m.attempts, t.storage_layout,
			       (SELECT b.name FROM collections c JOIN buckets b ON b.id = c.bucket_id WHERE c.tenant_id = $1 AND c.name = 'docs'),
			       (SELECT b.name FROM collections c JOIN buckets b ON b.id = c.bucket_id WHERE c.tenant_id = $1 AND c.name = 'photos'),
			       (SELECT b.name FROM tenant_default_bindings d JOIN buckets b ON b.id = d.bucket_id WHERE d.tenant_id = $1)
			FROM tenant_storage_migrations m JOIN tenants t ON t.id = m.tenant_id WHERE m.tenant_id = $1`, tenant).
			Scan(&a.state, &a.errMsg, &a.attempts, &a.layout, &a.docs, &a.photos, &a.binding); err != nil {
			t.Fatalf("read: %v", err)
		}
		return a
	}
	untouched := read()
	if untouched.layout != "dedicated" || untouched.docs != "rb-dedicated" {
		t.Fatalf("fixture is not rebound: %+v", untouched)
	}
	current := worker.MigrationRead{TenantID: tenant, State: worker.MigStateRollingBack, Attempts: 2}
	stale := current
	stale.Attempts = 1

	// A worker that read an earlier attempt, and one that read another state.
	for what, at := range map[string]worker.MigrationRead{
		"an earlier attempt": stale,
		"verifying":          {TenantID: tenant, State: worker.MigStateVerifying, Attempts: 2},
	} {
		err := repo.RebindBack(ctx, at, reboundAt, uuid.Nil(), backend, "rb-shared")
		if !errors.Is(err, worker.ErrMigrationMoved) {
			t.Errorf("RebindBack read as %s = %v, want ErrMigrationMoved", what, err)
		}
		if got := read(); got != untouched {
			t.Errorf("RebindBack read as %s changed things: %+v, want %+v", what, got, untouched)
		}
	}

	// The current attempt, with 'post' not yet copied back.
	if err := repo.RebindBack(ctx, current, reboundAt, uuid.Nil(), backend, "rb-shared"); !errors.Is(err, worker.ErrRollbackBehind) {
		t.Fatalf("RebindBack with an object past the cursor = %v, want ErrRollbackBehind", err)
	}
	if got := read(); got != untouched {
		t.Errorf("a refused RebindBack changed things: %+v", got)
	}

	written, err := repo.ListWrittenSince(ctx, tenant, reboundAt, uuid.Nil(), 10)
	if err != nil || len(written) != 1 || written[0].Key != "post" || written[0].SizeBytes != 3 {
		t.Fatalf("ListWrittenSince = %+v, %v; want just docs/post", written, err)
	}
	cur := written[0]
	if err := repo.AdvanceRollback(ctx, stale, cur.UpdatedAt, cur.ID); !errors.Is(err, worker.ErrMigrationMoved) {
		t.Errorf("stale AdvanceRollback = %v, want ErrMigrationMoved", err)
	}
	if err := repo.AdvanceRollback(ctx, current, cur.UpdatedAt, cur.ID); err != nil {
		t.Fatalf("AdvanceRollback: %v", err)
	}
	if err := repo.AdvanceRollback(ctx, current, reboundAt, uuid.Nil()); !errors.Is(err, worker.ErrMigrationMoved) {
		t.Errorf("AdvanceRollback backwards = %v, want ErrMigrationMoved", err)
	}
	if more, err := repo.ListWrittenSince(ctx, tenant, cur.UpdatedAt, cur.ID, 10); err != nil || len(more) != 0 {
		t.Errorf("ListWrittenSince past the cursor = %+v, %v; want nothing", more, err)
	}

	if err := repo.RebindBack(ctx, current, cur.UpdatedAt, cur.ID, backend, "rb-shared"); err != nil {
		t.Fatalf("RebindBack: %v", err)
	}
	want := after{
		state: worker.MigStateFailed, errMsg: untouched.errMsg, attempts: 2,
		layout: "shared", docs: "rb-shared", photos: "rb-public", binding: "rb-shared",
	}
	if got := read(); got != want {
		t.Errorf("after RebindBack: %+v, want %+v", got, want)
	}
	if err := repo.RebindBack(ctx, current, cur.UpdatedAt, cur.ID, backend, "rb-shared"); !errors.Is(err, worker.ErrMigrationMoved) {
		t.Errorf("a second RebindBack = %v, want ErrMigrationMoved", err)
	}
}

// The rebind records rebound_at in its own transaction, and a failed verify
// moves only a migration still verifying on the attempt read.
func TestStorageMigrationRebindRecordsReboundAt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := pgharness.Setup(t)
	pool := h.PoolMigrate
	q := sqlc.New(pool)
	repo := adapters.NewStorageMigrationRepo(q, pool)
	const backend = "be-migration-rebound"
	seedBackend(t, pool, backend)
	tenant := mustCreateTenant(t, pool, "migration-rebound")
	mustSeedSharedBucket(t, pool, backend, "rba-shared", tenant)
	mustCreateBucket(t, pool, tenant, backend, "rba-dedicated")
	if _, err := pool.Exec(ctx, `INSERT INTO collections (tenant_id, name, bucket_id)
		SELECT $1, 'docs', id FROM buckets WHERE name = 'rba-shared'`, tenant); err != nil {
		t.Fatalf("seed collection: %v", err)
	}
	if _, err := q.CreateStorageMigration(ctx, pgtype.UUID{Bytes: tenant, Valid: true},
		backend, "rba-shared", backend, "rba-dedicated", cleanupRetentionSeconds); err != nil {
		t.Fatalf("create migration: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE tenant_storage_migrations SET state = 'rebinding' WHERE tenant_id = $1`, tenant); err != nil {
		t.Fatalf("to rebinding: %v", err)
	}
	var before time.Time
	if err := pool.QueryRow(ctx, `SELECT now()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := repo.RebindTenant(ctx, worker.MigrationRead{TenantID: tenant, State: worker.MigStateRebinding},
		backend, "rba-dedicated"); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	var reboundAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT rebound_at FROM tenant_storage_migrations WHERE tenant_id = $1`, tenant).
		Scan(&reboundAt); err != nil {
		t.Fatalf("read rebound_at: %v", err)
	}
	if reboundAt == nil || reboundAt.Before(before) {
		t.Fatalf("rebound_at = %v, want it set by the rebind (after %v)", reboundAt, before)
	}

	if err := repo.SetState(ctx, worker.MigrationRead{TenantID: tenant, State: worker.MigStateRebinding},
		worker.MigStateVerifying); err != nil {
		t.Fatalf("to verifying: %v", err)
	}
	stale := worker.MigrationRead{TenantID: tenant, State: worker.MigStateRebinding}
	if err := repo.StartRollback(ctx, stale, "x"); !errors.Is(err, worker.ErrMigrationMoved) {
		t.Errorf("StartRollback from rebinding = %v, want ErrMigrationMoved", err)
	}
	if err := repo.StartRollback(ctx, worker.MigrationRead{TenantID: tenant, State: worker.MigStateVerifying},
		"physical verify: boom"); err != nil {
		t.Fatalf("StartRollback: %v", err)
	}
	var state, msg string
	var attempts int32
	if err := pool.QueryRow(ctx, `SELECT state, error, attempts FROM tenant_storage_migrations WHERE tenant_id = $1`, tenant).
		Scan(&state, &msg, &attempts); err != nil {
		t.Fatal(err)
	}
	if state != worker.MigStateRollingBack || msg != "physical verify: boom" || attempts != 1 {
		t.Errorf("after StartRollback: %s %q attempt %d; want rolling_back, the verify error, 1", state, msg, attempts)
	}
}
