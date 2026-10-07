//go:build integration

package integration

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// trashFixture is two tenants with the same rows in one bucket — one live,
// one in the trash.
type trashFixture struct {
	pool          *pgxpool.Pool
	live, trashed uuid.UUID
}

const (
	trashBackend = "trash-be"
	trashBucket  = "trash-bucket"
	// oldEnough is how long ago the rows a worker selects by age happened.
	oldEnough = time.Hour
)

func setupTrashFixture(t *testing.T, pool *pgxpool.Pool) trashFixture {
	t.Helper()
	ctx := context.Background()
	f := trashFixture{pool: pool, live: mustCreateTenant(t, pool, "trash-live"), trashed: mustCreateTenant(t, pool, "trash-gone")}
	seedBackend(t, pool, trashBackend)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	exec(`INSERT INTO buckets (backend_id, name) SELECT id, $2 FROM storage_backends WHERE name = $1`, trashBackend, trashBucket)
	for _, tenant := range []uuid.UUID{f.live, f.trashed} {
		exec(`INSERT INTO collections (tenant_id, name, bucket_id)
		      SELECT $1, 'docs', b.id FROM buckets b WHERE b.name = $2`, tenant, trashBucket)
		for _, state := range []string{"DELETED", "PENDING"} {
			exec(`INSERT INTO objects (tenant_id, collection_id, path, state, content_type, terminated_at, presign_expires_at)
			      SELECT $1, c.id, $2, $3::object_state, 'text/plain', now() - $4::interval, now() - $4::interval
			        FROM collections c WHERE c.tenant_id = $1`, tenant, "a-"+state, state, oldEnough.String())
		}
		exec(`INSERT INTO operations (tenant_id, type) VALUES ($1, 'batch_delete')`, tenant)
		exec(`INSERT INTO tenant_storage_migrations (tenant_id, source_bucket_id, target_bucket_id)
		      SELECT $1, b.id, b.id FROM buckets b WHERE b.name = $2`, tenant, trashBucket)
	}
	exec(`UPDATE tenants SET deleted_at = now() WHERE id = $1`, f.trashed)
	return f
}

// Every worker that changes a tenant's data leaves a tenant in the trash
// alone: restoring it must return what was trashed.
func TestWorkersLeaveATrashedTenantAlone(t *testing.T) {
	h := setupDispatcher(t)
	f := setupTrashFixture(t, h.h.PoolMigrate)
	ctx := context.Background()
	q := sqlc.New(f.pool)
	only := func(t *testing.T, what string, got []uuid.UUID) {
		t.Helper()
		if len(got) != 1 || got[0] != f.live {
			t.Errorf("%s selected tenants %v, want only the live one %s", what, got, f.live)
		}
	}

	t.Run("lifecycle and replication", func(t *testing.T) {
		rows, err := q.ListCollectionBindingsForBucket(ctx, trashBackend, trashBucket)
		if err != nil {
			t.Fatal(err)
		}
		var got []uuid.UUID
		for _, r := range rows {
			got = append(got, uuid.UUID(r.TenantID.Bytes))
		}
		only(t, "ListCollectionBindingsForBucket", got)
	})

	t.Run("trash emptying", func(t *testing.T) {
		rows, err := q.ListHardDeletable(ctx, pgtype.Timestamptz{Time: time.Now(), Valid: true}, 10)
		if err != nil {
			t.Fatal(err)
		}
		var got []uuid.UUID
		for _, r := range rows {
			got = append(got, uuid.UUID(r.TenantID.Bytes))
		}
		only(t, "ListHardDeletable", got)
		// The recheck refuses a row listed before its tenant was trashed.
		var id uuid.UUID
		var version int64
		if err := f.pool.QueryRow(ctx, `SELECT id, resource_version FROM objects WHERE tenant_id = $1 AND state = 'DELETED'`, f.trashed).Scan(&id, &version); err != nil {
			t.Fatal(err)
		}
		n, err := q.HardDeleteObjectIfStillDeleted(ctx, pgtype.UUID{Bytes: id, Valid: true}, version)
		if err != nil || n != 0 {
			t.Errorf("hard-deleted %d rows of a trashed tenant (err %v)", n, err)
		}
	})

	t.Run("pending uploads", func(t *testing.T) {
		ids, err := statemachine.New(f.pool).ScanPendingExpired(ctx, time.Minute, 10)
		if err != nil {
			t.Fatal(err)
		}
		var got []uuid.UUID
		for _, id := range ids {
			var tenant uuid.UUID
			if err := f.pool.QueryRow(ctx, `SELECT tenant_id FROM objects WHERE id = $1`, id).Scan(&tenant); err != nil {
				t.Fatal(err)
			}
			got = append(got, tenant)
		}
		only(t, "ScanPendingExpired", got)
	})

	t.Run("storage migrations", func(t *testing.T) {
		rows, err := q.ListActiveStorageMigrations(ctx, 10)
		if err != nil {
			t.Fatal(err)
		}
		var got []uuid.UUID
		for _, r := range rows {
			got = append(got, uuid.UUID(r.TenantStorageMigration.TenantID.Bytes))
		}
		only(t, "ListActiveStorageMigrations", got)
	})

	t.Run("operations", func(t *testing.T) {
		repo := adapters.NewOperationRepo(q, f.pool)
		first, err := repo.ClaimNext(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if first.TenantID != f.live {
			t.Errorf("claimed an operation of %s, want the live tenant's", first.TenantID)
		}
		if _, err := repo.ClaimNext(ctx); !errors.Is(err, pgx.ErrNoRows) && err == nil {
			t.Error("claimed the trashed tenant's operation")
		}
	})

	t.Run("event delivery", func(t *testing.T) {
		rec := newRecorder(http.StatusOK)
		defer rec.Close()
		d := h.dispatcher()
		for _, tenant := range []uuid.UUID{f.live, f.trashed} {
			h.seedSubscription(t, tenant, subOpts{URL: rec.srv.URL})
			if _, err := d.Dispatch(ctx, tenant.String(), makeEvent("", tenant)); err != nil {
				t.Fatal(err)
			}
		}
		h.tickOnce(t, h.outboxRunner(d))
		live := h.allDeliveryRows(t, f.live)
		if len(live) == 0 {
			t.Fatal("the live tenant has no delivery")
		}
		for _, r := range live {
			if r.Status != "delivered" {
				t.Errorf("live tenant's delivery = %+v, want delivered", r)
			}
		}
		if rows := h.allDeliveryRows(t, f.trashed); len(rows) != 1 || rows[0].Status != "pending" || rows[0].Attempts != 0 {
			t.Errorf("trashed tenant's delivery = %+v, want still pending and untried", rows)
		}
	})
}
