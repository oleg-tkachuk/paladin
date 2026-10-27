//go:build integration

package components

import (
	"context"
	"errors"
	"sync"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// startArgs is what MigrateTenantStorageLayout hands the repository for the
// fixture's tenant: from its private bucket to its dedicated one.
func (f publicMigrationFixture) startArgs() tenanth.StartStorageMigrationArgs {
	return tenanth.StartStorageMigrationArgs{
		TenantID:          f.tenant,
		SourceBackendName: f.backend, SourceBucketName: f.private,
		TargetBackendName: f.backend, TargetBucketName: f.dedicated,
		CleanupRetentionSeconds: 60,
	}
}

func (f publicMigrationFixture) startRaw(ctx context.Context) error {
	_, err := adapters.NewTenantRepo(sqlc.New(f.pool), f.pool).StartStorageMigration(ctx, f.startArgs())
	return err
}

type migrationRow struct {
	state, errMsg       string
	total, copied       int64
	cursorColl, cursorP string
	attempts            int
}

func (f publicMigrationFixture) migrationRows(t *testing.T, ctx context.Context) []migrationRow {
	t.Helper()
	rows, err := f.pool.Query(ctx, `SELECT state, COALESCE(error, ''), objects_total, objects_copied,
		cursor_collection, cursor_path, attempts
		FROM tenant_storage_migrations WHERE tenant_id = $1`, f.tenant)
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	defer rows.Close()
	var out []migrationRow
	for rows.Next() {
		var r migrationRow
		if err := rows.Scan(&r.state, &r.errMsg, &r.total, &r.copied, &r.cursorColl, &r.cursorP, &r.attempts); err != nil {
			t.Fatalf("scan migration: %v", err)
		}
		out = append(out, r)
	}
	return out
}

// onlyMigration is the tenant's one migration row; a second one is the bug a
// start must not reintroduce.
func (f publicMigrationFixture) onlyMigration(t *testing.T, ctx context.Context) migrationRow {
	t.Helper()
	rows := f.migrationRows(t, ctx)
	if len(rows) != 1 {
		t.Fatalf("tenant has %d migration rows, want 1: %+v", len(rows), rows)
	}
	return rows[0]
}

func (f publicMigrationFixture) dedicatedProvision(t *testing.T, ctx context.Context) (string, int) {
	t.Helper()
	var state string
	var attempts int
	if err := f.pool.QueryRow(ctx, `SELECT provision_state, provision_attempts FROM buckets WHERE name = $1`,
		f.dedicated).Scan(&state, &attempts); err != nil {
		t.Fatalf("read dedicated bucket: %v", err)
	}
	return state, attempts
}

// otherTenant creates a second tenant and returns its id.
func (f publicMigrationFixture) otherTenant(t *testing.T, ctx context.Context) uuid.UUID {
	t.Helper()
	id := uuid.New()
	hex := id.String()[:8]
	mustExec(t, ctx, f.pool, `INSERT INTO tenants (id, slug, display_name) VALUES ($1, $2, $3)`,
		id, "o-"+hex, "other-"+hex)
	return id
}

// A failed migration used to be permanent: nothing reset its row, so the
// next start was refused, and deleting the row by hand did not help either —
// the dedicated bucket the failed attempt left made the start's bucket insert
// raise a unique violation that, "tolerated", aborted the transaction, and
// the migration insert after it failed with 25P02. A start now retries a
// failed migration in place.
func TestStorageMigrationRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// The worker fails a migration whose target bucket the reconciler gave up
	// on. The retry hands the bucket back to the reconciler with a fresh
	// attempt budget and starts the migration over; it then completes.
	t.Run("after a provisioning failure, through to cleaned", func(t *testing.T) {
		t.Parallel()
		f := seedPublicMigrationFixture(t, ctx)
		f.addCollection(t, ctx, "docs", f.private, false)
		if err := f.startRaw(ctx); err != nil {
			t.Fatalf("start: %v", err)
		}
		mustExec(t, ctx, f.pool, `UPDATE buckets SET provision_state = 'failed', provision_attempts = 5,
			provision_error = 'AccessDenied' WHERE name = $1`, f.dedicated)
		if state, reason := f.drive(t, ctx); state != worker.MigStateFailed {
			t.Fatalf("migration left at %s (%s), want failed", state, reason)
		}

		if err := f.startRaw(ctx); err != nil {
			t.Fatalf("retry: %v", err)
		}
		m := f.onlyMigration(t, ctx)
		if m.state != worker.MigStateProvisioning || m.errMsg != "" {
			t.Errorf("retried migration is %s (%q), want provisioning with the error cleared", m.state, m.errMsg)
		}
		if m.attempts != 1 {
			t.Errorf("attempts = %d, want the 1 failure kept", m.attempts)
		}
		if state, attempts := f.dedicatedProvision(t, ctx); state != "pending" || attempts != 0 {
			t.Errorf("dedicated bucket is %s after %d attempts, want pending with a fresh budget", state, attempts)
		}

		mustExec(t, ctx, f.pool, `UPDATE buckets SET provision_state = 'ready' WHERE name = $1`, f.dedicated)
		if state, reason := f.drive(t, ctx); state != worker.MigStateCleaned {
			t.Fatalf("retried migration left at %s (%s), want cleaned", state, reason)
		}
		if got := f.boundTo(t, ctx, "docs"); got != f.dedicated {
			t.Errorf("collection bound to %s, want %s", got, f.dedicated)
		}
		if got := f.layout(t, ctx); got != "dedicated" {
			t.Errorf("storage_layout = %s, want dedicated", got)
		}
	})

	// A migration that failed mid-copy left some objects in the target and a
	// cursor past them. The retry copies from the top — same-key copies
	// overwrite with the same bytes — and reaches the end.
	t.Run("after a failure mid-copy, from the top", func(t *testing.T) {
		t.Parallel()
		f := seedPublicMigrationFixture(t, ctx)
		f.addCollection(t, ctx, "docs", f.private, false)
		f.addCollection(t, ctx, "more", f.private, false)
		if err := f.start(t, ctx); err != nil {
			t.Fatalf("start: %v", err)
		}
		f.store.put(worker.CopyLocation{BackendID: f.backend, TenantID: f.tenant, Bucket: f.dedicated,
			Collection: "docs", Key: "obj-1"}, 42)
		mustExec(t, ctx, f.pool, `UPDATE tenant_storage_migrations
			SET state = 'failed', error = 'copy incomplete', attempts = 1, objects_total = 2,
			    objects_copied = 1, cursor_collection = 'docs', cursor_path = 'obj-1'
			WHERE tenant_id = $1`, f.tenant)

		if err := f.startRaw(ctx); err != nil {
			t.Fatalf("retry: %v", err)
		}
		m := f.onlyMigration(t, ctx)
		if m.total != 0 || m.copied != 0 || m.cursorColl != "" || m.cursorP != "" {
			t.Errorf("retry kept the failed attempt's progress: %+v", m)
		}
		if state, reason := f.drive(t, ctx); state != worker.MigStateCleaned {
			t.Fatalf("retried migration left at %s (%s), want cleaned", state, reason)
		}
		for _, c := range []string{"docs", "more"} {
			if !f.store.has(worker.CopyLocation{BackendID: f.backend, TenantID: f.tenant, Bucket: f.dedicated,
				Collection: c, Key: "obj-1"}) {
				t.Errorf("%s/obj-1 is not in the dedicated bucket", c)
			}
		}
	})

	// Only 'failed' is retryable: a migration in flight belongs to the
	// worker, a finished one has nothing left to do.
	for _, state := range []string{
		worker.MigStateProvisioning, worker.MigStateCopying, worker.MigStateRebinding,
		worker.MigStateVerifying, worker.MigStateCompleted, worker.MigStateCleaned,
	} {
		t.Run("refused while "+state, func(t *testing.T) {
			t.Parallel()
			f := seedPublicMigrationFixture(t, ctx)
			f.addCollection(t, ctx, "docs", f.private, false)
			if err := f.startRaw(ctx); err != nil {
				t.Fatalf("start: %v", err)
			}
			mustExec(t, ctx, f.pool, `UPDATE tenant_storage_migrations
				SET state = $2, objects_total = 1, objects_copied = 1, cursor_collection = 'docs'
				WHERE tenant_id = $1`, f.tenant, state)
			if err := f.startRaw(ctx); !errors.Is(err, tenanth.ErrStorageMigrationExists) {
				t.Fatalf("start over a %s migration = %v, want ErrStorageMigrationExists", state, err)
			}
			if m := f.onlyMigration(t, ctx); m.state != state || m.copied != 1 || m.cursorColl != "docs" {
				t.Errorf("refused start changed the migration: %+v", m)
			}
		})
	}

	// Without a unique key on tenant_id, a second start naming another target
	// backend used to insert a second migration for the tenant.
	t.Run("a second start on another backend is refused", func(t *testing.T) {
		t.Parallel()
		f := seedPublicMigrationFixture(t, ctx)
		f.addCollection(t, ctx, "docs", f.private, false)
		if err := f.startRaw(ctx); err != nil {
			t.Fatalf("start: %v", err)
		}
		mustExec(t, ctx, f.pool, `INSERT INTO storage_backends (name, kind) VALUES ($1, 's3-compatible')`, f.backend+"-2")
		args := f.startArgs()
		args.TargetBackendName = f.backend + "-2"
		_, err := adapters.NewTenantRepo(sqlc.New(f.pool), f.pool).StartStorageMigration(ctx, args)
		if !errors.Is(err, tenanth.ErrStorageMigrationExists) {
			t.Fatalf("second start = %v, want ErrStorageMigrationExists", err)
		}
		f.onlyMigration(t, ctx)
	})

	// A failure after the rebind (the physical verify) leaves a tenant that
	// already serves from its dedicated bucket. Copying again would copy from
	// a bucket it no longer uses, so it is not retried.
	t.Run("refused once the tenant is dedicated", func(t *testing.T) {
		t.Parallel()
		f := seedPublicMigrationFixture(t, ctx)
		f.addCollection(t, ctx, "docs", f.private, false)
		if err := f.startRaw(ctx); err != nil {
			t.Fatalf("start: %v", err)
		}
		mustExec(t, ctx, f.pool, `UPDATE tenant_storage_migrations SET state = 'failed' WHERE tenant_id = $1`, f.tenant)
		mustExec(t, ctx, f.pool, `UPDATE tenants SET storage_layout = 'dedicated' WHERE id = $1`, f.tenant)
		if err := f.startRaw(ctx); !errors.Is(err, tenanth.ErrTenantNotShared) {
			t.Fatalf("retry of a dedicated tenant = %v, want ErrTenantNotShared", err)
		}
		if m := f.onlyMigration(t, ctx); m.state != worker.MigStateFailed {
			t.Errorf("refused retry moved the migration to %s", m.state)
		}
	})

	// Concurrent retries of one failed migration: exactly one starts it.
	t.Run("concurrent retries, one wins", func(t *testing.T) {
		t.Parallel()
		f := seedPublicMigrationFixture(t, ctx)
		f.addCollection(t, ctx, "docs", f.private, false)
		if err := f.startRaw(ctx); err != nil {
			t.Fatalf("start: %v", err)
		}
		mustExec(t, ctx, f.pool, `UPDATE tenant_storage_migrations SET state = 'failed', error = 'x'
			WHERE tenant_id = $1`, f.tenant)
		wins, refused := raceStarts(t, ctx, f, 8)
		if wins != 1 || refused != 7 {
			t.Fatalf("%d retries started the migration and %d were refused, want 1 and 7", wins, refused)
		}
		if m := f.onlyMigration(t, ctx); m.state != worker.MigStateProvisioning {
			t.Errorf("migration is %s, want provisioning", m.state)
		}
	})

	// The same for first starts, which used to be able to insert a row each.
	t.Run("concurrent first starts, one wins", func(t *testing.T) {
		t.Parallel()
		f := seedPublicMigrationFixture(t, ctx)
		f.addCollection(t, ctx, "docs", f.private, false)
		wins, refused := raceStarts(t, ctx, f, 8)
		if wins != 1 || refused != 7 {
			t.Fatalf("%d starts went through and %d were refused, want 1 and 7", wins, refused)
		}
		f.onlyMigration(t, ctx)
	})

	// The 25P02 on its own: the migration row deleted by hand, the dedicated
	// bucket the failed attempt created still there.
	t.Run("start over a leftover dedicated bucket", func(t *testing.T) {
		t.Parallel()
		f := seedPublicMigrationFixture(t, ctx)
		f.addCollection(t, ctx, "docs", f.private, false)
		if err := f.startRaw(ctx); err != nil {
			t.Fatalf("start: %v", err)
		}
		mustExec(t, ctx, f.pool, `DELETE FROM tenant_storage_migrations WHERE tenant_id = $1`, f.tenant)
		if err := f.startRaw(ctx); err != nil {
			t.Fatalf("start over the leftover bucket: %v", err)
		}
		if m := f.onlyMigration(t, ctx); m.state != worker.MigStateProvisioning {
			t.Errorf("migration is %s, want provisioning", m.state)
		}
	})
}

// raceStarts runs n starts of f's migration at once and counts the ones that
// went through and the ones refused as already started. Anything else fails.
func raceStarts(t *testing.T, ctx context.Context, f publicMigrationFixture, n int) (wins, refused int) {
	t.Helper()
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		gate = make(chan struct{})
	)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			err := f.startRaw(ctx)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, tenanth.ErrStorageMigrationExists):
				refused++
			default:
				t.Errorf("start: %v", err)
			}
		}()
	}
	close(gate)
	wg.Wait()
	return wins, refused
}

// The dedicated bucket's name is derived from the tenant id, so anyone who
// can create a bucket can create one by that name first. A start takes an
// existing bucket only if this tenant alone owns and uses it; anything else
// is refused and nothing is recorded, rather than the tenant's objects being
// copied into a bucket someone else reads.
func TestStorageMigrationRefusesATargetBucketItMayNotUse(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	cases := []struct {
		name  string
		setup func(t *testing.T, f publicMigrationFixture)
	}{
		{"owned by another tenant", func(t *testing.T, f publicMigrationFixture) {
			t.Helper()
			other := f.otherTenant(t, ctx)
			mustExec(t, ctx, f.pool, `INSERT INTO buckets (backend_id, name, owner_tenant_id)
				SELECT sb.id, $2, $3 FROM storage_backends sb WHERE sb.name = $1`, f.backend, f.dedicated, other)
		}},
		// A grant lets the tenant bind there; it does not make the bucket its own.
		{"only granted to this tenant", func(t *testing.T, f publicMigrationFixture) {
			t.Helper()
			other := f.otherTenant(t, ctx)
			mustExec(t, ctx, f.pool, `INSERT INTO buckets (backend_id, name, owner_tenant_id)
				SELECT sb.id, $2, $3 FROM storage_backends sb WHERE sb.name = $1`, f.backend, f.dedicated, other)
			grantBucket(t, ctx, f.pool, f.tenant, f.backend, f.dedicated)
		}},
		// Granted to another tenant, which has bound nothing yet: it may at any
		// moment, so the bucket is not this tenant's alone.
		{"this tenant's, granted to another", func(t *testing.T, f publicMigrationFixture) {
			t.Helper()
			other := f.otherTenant(t, ctx)
			mustExec(t, ctx, f.pool, `INSERT INTO buckets (backend_id, name, owner_tenant_id)
				SELECT sb.id, $2, $3 FROM storage_backends sb WHERE sb.name = $1`, f.backend, f.dedicated, f.tenant)
			grantBucket(t, ctx, f.pool, other, f.backend, f.dedicated)
		}},
		// Owned by this tenant, but another tenant was granted it and bound a
		// collection there (ADR-0032): owning a bucket is not using it alone.
		{"this tenant's, but another one uses it", func(t *testing.T, f publicMigrationFixture) {
			t.Helper()
			other := f.otherTenant(t, ctx)
			mustExec(t, ctx, f.pool, `INSERT INTO buckets (backend_id, name, owner_tenant_id)
				SELECT sb.id, $2, $3 FROM storage_backends sb WHERE sb.name = $1`, f.backend, f.dedicated, f.tenant)
			grantBucket(t, ctx, f.pool, other, f.backend, f.dedicated)
			mustExec(t, ctx, f.pool, `INSERT INTO collections (tenant_id, name, bucket_id)
				SELECT $1, 'theirs', b.id FROM buckets b WHERE b.name = $2`, other, f.dedicated)
		}},
		{"another tenant's default binding", func(t *testing.T, f publicMigrationFixture) {
			t.Helper()
			other := f.otherTenant(t, ctx)
			mustExec(t, ctx, f.pool, `INSERT INTO buckets (backend_id, name, owner_tenant_id)
				SELECT sb.id, $2, $3 FROM storage_backends sb WHERE sb.name = $1`, f.backend, f.dedicated, f.tenant)
			mustExec(t, ctx, f.pool, `INSERT INTO tenant_default_bindings (tenant_id, bucket_id, set_by)
				SELECT $1, b.id, 'test' FROM buckets b WHERE b.name = $2`, other, f.dedicated)
		}},
		{"public", func(t *testing.T, f publicMigrationFixture) {
			t.Helper()
			mustExec(t, ctx, f.pool, `INSERT INTO buckets (backend_id, name, owner_tenant_id, public_read)
				SELECT sb.id, $2, $3, true FROM storage_backends sb WHERE sb.name = $1`, f.backend, f.dedicated, f.tenant)
		}},
		{"being deleted", func(t *testing.T, f publicMigrationFixture) {
			t.Helper()
			mustExec(t, ctx, f.pool, `INSERT INTO buckets (backend_id, name, owner_tenant_id, provision_state)
				SELECT sb.id, $2, $3, 'deleting' FROM storage_backends sb WHERE sb.name = $1`, f.backend, f.dedicated, f.tenant)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := seedPublicMigrationFixture(t, ctx)
			f.addCollection(t, ctx, "docs", f.private, false)
			tc.setup(t, f)
			if err := f.startRaw(ctx); !errors.Is(err, tenanth.ErrTargetBucketUnusable) {
				t.Fatalf("start = %v, want ErrTargetBucketUnusable", err)
			}
			if rows := f.migrationRows(t, ctx); len(rows) != 0 {
				t.Errorf("a refused start recorded a migration: %+v", rows)
			}
		})
	}

	// The bucket an earlier attempt of this tenant left is its own: taken.
	t.Run("this tenant's own", func(t *testing.T) {
		t.Parallel()
		f := seedPublicMigrationFixture(t, ctx)
		f.addCollection(t, ctx, "docs", f.private, false)
		mustExec(t, ctx, f.pool, `INSERT INTO buckets (backend_id, name, owner_tenant_id, provision_state)
			SELECT sb.id, $2, $3, 'pending' FROM storage_backends sb WHERE sb.name = $1`, f.backend, f.dedicated, f.tenant)
		if err := f.startRaw(ctx); err != nil {
			t.Fatalf("start: %v", err)
		}
		f.onlyMigration(t, ctx)
	})
}
