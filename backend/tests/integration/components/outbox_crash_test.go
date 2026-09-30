//go:build integration

// Package components holds Postgres-backed tests for the transactional
// outbox (ADR-0003). They are gated behind the `integration` build tag so
// the default `go test ./...` (and the pre-push hook) stay hermetic — run
// them with `go test -tags=integration ./tests/integration/components/...`, which
// needs a Docker daemon for testcontainers.
package components

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
	"github.com/oleg-tkachuk/paladin/backend/migrations"
)

// startPostgres boots a throwaway Postgres, applies the full goose schema,
// and returns a live pool. The cleanup terminates the container.
func startPostgres(t testing.TB) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("paladin"),
		tcpostgres.WithUsername("paladin"),
		tcpostgres.WithPassword("paladin"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(90*time.Second)),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	// Apply migrations via the same goose + embedded FS the app uses.
	cc, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	sqlDB := stdlib.OpenDB(*cc)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("goose dialect: %v", err)
	}
	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.Up(sqlDB, "."); err != nil {
		t.Fatalf("goose up: %v", err)
	}
	_ = sqlDB.Close()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// fixture seeds one tenant + backend + collection + an active (match-all)
// subscription, so any object event for the tenant fans out exactly one
// outbox row. Returns the tenant id and the collection the objects hang off.
type fixture struct {
	tenantID uuid.UUID
	// collection is the name; collectionID is what objects reference. Tests
	// that exercise RLS need the id: resolving the name by tenant would make a
	// cross-tenant INSERT match zero rows instead of tripping the policy, and
	// the test would pass for the wrong reason.
	collection   string
	collectionID uuid.UUID
}

func seedFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) fixture {
	t.Helper()
	hex := uuid.NewString()[:8]
	f := fixture{tenantID: uuid.New(), collection: "ok-" + hex}
	backendID := "be-" + hex
	bucketName := "bkt-" + hex // satisfies buckets.bucket_name_format

	mustExec(t, ctx, pool, `INSERT INTO storage_backends (name, kind) VALUES ($1, 's3-compatible')`, backendID)
	// Shared bucket (owner_tenant_id NULL) so the collections tenancy trigger
	// permits binding from the test tenant. collections FKs (backend_id,
	// bucket_name) → buckets.
	mustExec(t, ctx, pool, `INSERT INTO buckets (backend_id, name)
		 SELECT sb.id, $2 FROM storage_backends sb WHERE sb.name = $1`, backendID, bucketName)
	// slug + display_name are NOT NULL with format/unique CHECKs (migrations
	// 009 / 033). Mirror 009's `t-<hex>` backfill shape for the slug.
	mustExec(t, ctx, pool, `INSERT INTO tenants (id, slug, display_name) VALUES ($1, $2, $3)`,
		f.tenantID, "t-"+hex, "tn-"+hex)
	if err := pool.QueryRow(ctx, `INSERT INTO collections (tenant_id, name, bucket_id)
		 SELECT $1, $2, b.id FROM buckets b
		   JOIN storage_backends sb ON sb.id = b.backend_id
		  WHERE sb.name = $3 AND b.name = $4
		 RETURNING id`,
		f.tenantID, f.collection, backendID, bucketName).Scan(&f.collectionID); err != nil {
		t.Fatalf("seed collection: %v", err)
	}
	mustExec(t, ctx, pool, `INSERT INTO event_subscriptions (id, tenant_id, cel_filter, sink_kind, sink_config) VALUES ($1, $2, '', 'http', '{}'::jsonb)`, uuid.New(), f.tenantID)
	return f
}

// seedPendingObject inserts a fresh PENDING object under the fixture's
// collection and returns its id.
func seedPendingObject(t *testing.T, ctx context.Context, pool *pgxpool.Pool, f fixture) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	mustExec(t, ctx, pool,
		`INSERT INTO objects (id, tenant_id, collection_id, path, state, content_type, checksum_algorithm)
		 SELECT $1, $2, c.id, $4, 'PENDING', 'application/octet-stream', 0
		   FROM collections c WHERE c.tenant_id = $2 AND c.name = $3`,
		id, f.tenantID, f.collection, "key-"+uuid.NewString()[:8])
	return id
}

func mustExec(t testing.TB, ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func objectState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(ctx, `SELECT state FROM objects WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatalf("read state: %v", err)
	}
	return s
}

func deliveryCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, eventType string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM event_deliveries WHERE tenant_id = $1 AND event_type = $2`,
		tenantID, eventType).Scan(&n); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	return n
}

func objectExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) bool {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM objects WHERE id = $1)`, id).Scan(&exists); err != nil {
		t.Fatalf("exists: %v", err)
	}
	return exists
}

// TestOutboxCrashWindow proves the ADR-0003 invariant on a live Postgres:
// the state transition and its outbox rows commit (or roll back) as one
// unit, so there is no committed-but-unenqueued window a crash could leak.
func TestOutboxCrashWindow(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)

	q := sqlc.New(pool)
	disp := &worker.Dispatcher{
		Store:       worker.NewRepoSubscriptionStore(adapters.NewEventSubscriptionRepoV2(q)),
		Outbox:      worker.PgxOutboxWriter{Pool: pool},
		Logger:      zap.NewNop(),
		MaxAttempts: 3,
	}
	sm := statemachine.New(pool)
	repo := adapters.NewObjectRepo(q, pool)

	uploaded := func(tenantID uuid.UUID) worker.Event {
		return worker.Event{Type: "paladin.object.uploaded", At: time.Now().UTC(), TenantID: tenantID.String(), ResourceName: "r"}
	}

	// ── statemachine seam: promote + event commit together ───────────────
	t.Run("promote commits state and outbox atomically", func(t *testing.T) {
		id := seedPendingObject(t, ctx, pool, f)
		changed, err := sm.PromoteToAvailableInTx(ctx, id, "etag", 10, "", "", statemachine.SourceEvent,
			func(ctx context.Context, tx pgx.Tx) error {
				_, e := disp.DispatchTx(ctx, tx, f.tenantID.String(), uploaded(f.tenantID))
				return e
			})
		if err != nil || !changed {
			t.Fatalf("promote: changed=%v err=%v", changed, err)
		}
		if got := objectState(t, ctx, pool, id); got != "AVAILABLE" {
			t.Fatalf("state = %s, want AVAILABLE", got)
		}
		if n := deliveryCount(t, ctx, pool, f.tenantID, "paladin.object.uploaded"); n != 1 {
			t.Fatalf("outbox rows = %d, want 1", n)
		}
	})

	// ── statemachine seam: dispatch error rolls BOTH back ────────────────
	// The closure writes the outbox row on the tx, THEN errors — proving the
	// row that the dispatcher already inserted is rolled back with the
	// transition. This is the crash window the ADR closes: no half-state.
	t.Run("dispatch error rolls back promote and outbox", func(t *testing.T) {
		id := seedPendingObject(t, ctx, pool, f)
		before := deliveryCount(t, ctx, pool, f.tenantID, "paladin.object.uploaded")
		_, err := sm.PromoteToAvailableInTx(ctx, id, "etag", 10, "", "", statemachine.SourceEvent,
			func(ctx context.Context, tx pgx.Tx) error {
				if _, e := disp.DispatchTx(ctx, tx, f.tenantID.String(), uploaded(f.tenantID)); e != nil {
					return e
				}
				return errors.New("boom: simulated dispatch failure")
			})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if got := objectState(t, ctx, pool, id); got != "PENDING" {
			t.Fatalf("state = %s, want PENDING (rolled back)", got)
		}
		if n := deliveryCount(t, ctx, pool, f.tenantID, "paladin.object.uploaded"); n != before {
			t.Fatalf("outbox rows = %d, want %d (unchanged — rolled back)", n, before)
		}
	})

	// ── repo seam: permanent delete + event commit together ──────────────
	t.Run("hard delete commits row removal and outbox atomically", func(t *testing.T) {
		id := seedPendingObject(t, ctx, pool, f)
		mustExec(t, ctx, pool, `UPDATE objects SET state = 'AVAILABLE' WHERE id = $1`, id)
		err := repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			if e := repo.HardDeleteTx(ctx, tx, f.tenantID, id, 0); e != nil {
				return e
			}
			_, e := disp.DispatchTx(ctx, tx, f.tenantID.String(), worker.Event{
				Type: "paladin.object.deleted", At: time.Now().UTC(), TenantID: f.tenantID.String(), ResourceName: "r",
			})
			return e
		})
		if err != nil {
			t.Fatalf("hard delete: %v", err)
		}
		if objectExists(t, ctx, pool, id) {
			t.Fatal("row still present, want deleted")
		}
		if n := deliveryCount(t, ctx, pool, f.tenantID, "paladin.object.deleted"); n != 1 {
			t.Fatalf("outbox rows = %d, want 1", n)
		}
	})

	// ── repo seam: dispatch error rolls BOTH back ────────────────────────
	t.Run("dispatch error rolls back hard delete and outbox", func(t *testing.T) {
		id := seedPendingObject(t, ctx, pool, f)
		mustExec(t, ctx, pool, `UPDATE objects SET state = 'AVAILABLE' WHERE id = $1`, id)
		before := deliveryCount(t, ctx, pool, f.tenantID, "paladin.object.deleted")
		err := repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			if e := repo.HardDeleteTx(ctx, tx, f.tenantID, id, 0); e != nil {
				return e
			}
			if _, e := disp.DispatchTx(ctx, tx, f.tenantID.String(), worker.Event{
				Type: "paladin.object.deleted", At: time.Now().UTC(), TenantID: f.tenantID.String(), ResourceName: "r",
			}); e != nil {
				return e
			}
			return errors.New("boom: simulated dispatch failure")
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !objectExists(t, ctx, pool, id) {
			t.Fatal("row removed despite rollback")
		}
		if n := deliveryCount(t, ctx, pool, f.tenantID, "paladin.object.deleted"); n != before {
			t.Fatalf("outbox rows = %d, want %d (unchanged — rolled back)", n, before)
		}
	})

	// ── bucket reconciler seam: terminal delete + event commit together ──
	// The BucketReconciler removes the row and enqueues paladin.bucket.deleted on
	// one tx (RunInTx + GetTx + DeleteTx + DispatchTx). Exercise that exact
	// seam against a standalone bucket owned by the fixture tenant.
	bucketRepo := adapters.NewBucketRepoV2(q, pool)
	seedOwnedBucket := func(t *testing.T) (string, string) {
		t.Helper()
		be := "be2-" + uuid.NewString()[:8]
		bn := "bkt2-" + uuid.NewString()[:8]
		mustExec(t, ctx, pool, `INSERT INTO storage_backends (name, kind) VALUES ($1, 's3-compatible')`, be)
		mustExec(t, ctx, pool, `INSERT INTO buckets (backend_id, name, owner_tenant_id)
		 SELECT sb.id, $2, $3 FROM storage_backends sb WHERE sb.name = $1`, be, bn, f.tenantID)
		return be, bn
	}

	t.Run("bucket terminal delete commits row removal and outbox atomically", func(t *testing.T) {
		be, bn := seedOwnedBucket(t)
		err := bucketRepo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			b, e := bucketRepo.GetTx(ctx, tx, be, bn)
			if e != nil {
				return e
			}
			if e := bucketRepo.DeleteTx(ctx, tx, be, bn, 0); e != nil {
				return e
			}
			_, e = disp.DispatchTx(ctx, tx, b.OwnerTenantID.String(), worker.Event{
				Type: "paladin.bucket.deleted", At: time.Now().UTC(), TenantID: b.OwnerTenantID.String(), ResourceName: "r",
			})
			return e
		})
		if err != nil {
			t.Fatalf("bucket terminal delete: %v", err)
		}
		if bucketExists(t, ctx, pool, be, bn) {
			t.Fatal("bucket row still present, want deleted")
		}
		if n := deliveryCount(t, ctx, pool, f.tenantID, "paladin.bucket.deleted"); n != 1 {
			t.Fatalf("outbox rows = %d, want 1", n)
		}
	})

	t.Run("dispatch error rolls back bucket terminal delete and outbox", func(t *testing.T) {
		be, bn := seedOwnedBucket(t)
		before := deliveryCount(t, ctx, pool, f.tenantID, "paladin.bucket.deleted")
		err := bucketRepo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			if e := bucketRepo.DeleteTx(ctx, tx, be, bn, 0); e != nil {
				return e
			}
			if _, e := disp.DispatchTx(ctx, tx, f.tenantID.String(), worker.Event{
				Type: "paladin.bucket.deleted", At: time.Now().UTC(), TenantID: f.tenantID.String(), ResourceName: "r",
			}); e != nil {
				return e
			}
			return errors.New("boom: simulated dispatch failure")
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !bucketExists(t, ctx, pool, be, bn) {
			t.Fatal("bucket row removed despite rollback")
		}
		if n := deliveryCount(t, ctx, pool, f.tenantID, "paladin.bucket.deleted"); n != before {
			t.Fatalf("outbox rows = %d, want %d (unchanged — rolled back)", n, before)
		}
	})
}

func objectEtag(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) string {
	t.Helper()
	var e *string
	if err := pool.QueryRow(ctx, `SELECT etag FROM objects WHERE id = $1`, id).Scan(&e); err != nil {
		t.Fatalf("read etag: %v", err)
	}
	if e == nil {
		return ""
	}
	return *e
}

// TestPromoteSequencerRace covers the HEAD→promote race fix: an empty-
// sequencer (RPC/HEAD/copy) promote is a first-promote only — it must not
// overwrite an already-AVAILABLE row that a storage event promoted first,
// nor re-fire on a second call (which would double-charge quota).
func TestPromoteSequencerRace(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	sm := statemachine.New(pool)

	t.Run("empty-sequencer promote defers to event-promoted row", func(t *testing.T) {
		id := seedPendingObject(t, ctx, pool, f)
		// Storage event promotes first, with authoritative etag + sequencer.
		changed, err := sm.PromoteToAvailable(ctx, id, "evt-etag", 100, "", "s100", statemachine.SourceEvent)
		if err != nil || !changed {
			t.Fatalf("event promote: changed=%v err=%v", changed, err)
		}
		// RPC HEAD path (empty sequencer) arrives late with a different etag.
		changed, err = sm.PromoteToAvailable(ctx, id, "rpc-etag", 200, "", "", statemachine.SourceRPC)
		if err != nil {
			t.Fatalf("rpc promote: %v", err)
		}
		if changed {
			t.Fatal("rpc promote re-touched an already-AVAILABLE row (would re-charge quota)")
		}
		if got := objectEtag(t, ctx, pool, id); got != "evt-etag" {
			t.Fatalf("etag = %q, want evt-etag (event value preserved)", got)
		}
	})

	t.Run("double empty-sequencer promote charges once", func(t *testing.T) {
		id := seedPendingObject(t, ctx, pool, f)
		changed, err := sm.PromoteToAvailable(ctx, id, "rpc-etag", 10, "", "", statemachine.SourceRPC)
		if err != nil || !changed {
			t.Fatalf("first promote: changed=%v err=%v", changed, err)
		}
		changed, err = sm.PromoteToAvailable(ctx, id, "rpc-etag", 10, "", "", statemachine.SourceRPC)
		if err != nil {
			t.Fatalf("second promote: %v", err)
		}
		if changed {
			t.Fatal("second promote returned changed=true (would double-charge quota)")
		}
	})
}

func bucketExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, backendID, bucketName string) bool {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM buckets b
		                  JOIN storage_backends sb ON sb.id = b.backend_id
		                 WHERE sb.name = $1 AND b.name = $2)`,
		backendID, bucketName).Scan(&exists); err != nil {
		t.Fatalf("bucket exists: %v", err)
	}
	return exists
}
