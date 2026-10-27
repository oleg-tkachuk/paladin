//go:build integration

package components

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// fakeObjectStore is the physical side of a migration: which object lives in
// which bucket, at what size. A copy from a bucket that does not hold the
// object fails, as a real CopyObject would.
type fakeObjectStore struct {
	mu      sync.Mutex
	objects map[string]int64 // "bucket|tenant/collection/key" -> size
	touched []string         // every location a copy or delete named
}

func storeKey(l worker.CopyLocation) string {
	return fmt.Sprintf("%s|%s/%s/%s", l.Bucket, l.TenantID, l.Collection, l.Key)
}

func (s *fakeObjectStore) put(l worker.CopyLocation, size int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[storeKey(l)] = size
}

func (s *fakeObjectStore) has(l worker.CopyLocation) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objects[storeKey(l)]
	return ok
}

func (s *fakeObjectStore) CopyObject(_ context.Context, src, dst worker.CopyLocation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touched = append(s.touched, src.Collection, dst.Collection)
	size, ok := s.objects[storeKey(src)]
	if !ok {
		return fmt.Errorf("NoSuchKey: %s", storeKey(src))
	}
	s.objects[storeKey(dst)] = size
	return nil
}

func (s *fakeObjectStore) HeadObject(_ context.Context, l worker.CopyLocation) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	size, ok := s.objects[storeKey(l)]
	if !ok {
		return -1, fmt.Errorf("NotFound: %s: %w", storeKey(l), worker.ErrObjectMissing)
	}
	return size, nil
}

func (s *fakeObjectStore) DeleteObject(_ context.Context, l worker.CopyLocation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touched = append(s.touched, l.Collection)
	delete(s.objects, storeKey(l))
	return nil
}

// publicMigrationFixture is a shared tenant on a backend holding a shared
// private bucket and a shared public bucket. The tenant's dedicated bucket is
// created by start, as the RPC does.
type publicMigrationFixture struct {
	pool      *pgxpool.Pool
	store     *fakeObjectStore
	tenant    uuid.UUID
	backend   string
	private   string
	public    string
	dedicated string
}

func seedPublicMigrationFixture(t *testing.T, ctx context.Context) publicMigrationFixture {
	t.Helper()
	hex := uuid.New().String()[:8]
	f := publicMigrationFixture{
		pool:      startPostgres(t),
		store:     &fakeObjectStore{objects: map[string]int64{}},
		tenant:    uuid.New(),
		backend:   "be-" + hex,
		private:   "shared-" + hex,
		public:    "public-" + hex,
		dedicated: "paladin-" + hex,
	}
	mustExec(t, ctx, f.pool, `INSERT INTO storage_backends (name, kind) VALUES ($1, 's3-compatible')`, f.backend)
	mustExec(t, ctx, f.pool, `INSERT INTO tenants (id, slug, display_name) VALUES ($1, $2, $3)`,
		f.tenant, "t-"+hex, "tn-"+hex)
	// The buckets the tenant starts in are shared: another tenant owns them
	// and grants this one their use (ADR-0032).
	owner, _ := mkTenant(t, ctx, f.pool, "shared")
	for name, public := range map[string]bool{f.private: false, f.public: true} {
		mustExec(t, ctx, f.pool, `INSERT INTO buckets (backend_id, name, public_read, owner_tenant_id)
			SELECT sb.id, $2, $3, $4 FROM storage_backends sb WHERE sb.name = $1`,
			f.backend, name, public, owner)
		grantBucket(t, ctx, f.pool, f.tenant, f.backend, name)
	}
	return f
}

// addCollection creates a collection bound to bucket with one AVAILABLE
// object, both as a row and as bytes in that bucket.
func (f publicMigrationFixture) addCollection(t *testing.T, ctx context.Context, name, bucket string, public bool) {
	t.Helper()
	cacheControl := ""
	if public {
		cacheControl = "public, max-age=60"
	}
	mustExec(t, ctx, f.pool, `INSERT INTO collections (tenant_id, name, bucket_id, public_read, cache_control)
		SELECT $1, $2, b.id, $5, $6 FROM buckets b
		  JOIN storage_backends sb ON sb.id = b.backend_id
		 WHERE sb.name = $3 AND b.name = $4`,
		f.tenant, name, f.backend, bucket, public, cacheControl)
	const key, size = "obj-1", int64(42)
	mustExec(t, ctx, f.pool, `INSERT INTO objects (tenant_id, collection_id, path, state, content_type, size_bytes)
		SELECT $1, c.id, $3, 'AVAILABLE', 'image/png', $4 FROM collections c
		 WHERE c.tenant_id = $1 AND c.name = $2`,
		f.tenant, name, key, size)
	f.store.put(worker.CopyLocation{BackendID: f.backend, TenantID: f.tenant, Bucket: bucket, Collection: name, Key: key}, size)
}

// start records the migration the way MigrateTenantStorageLayout does, from
// the source bucket the repository reports.
func (f publicMigrationFixture) start(t *testing.T, ctx context.Context) error {
	t.Helper()
	repo := adapters.NewTenantRepo(sqlc.New(f.pool), f.pool)
	srcBackend, srcBucket, err := repo.TenantSourceBucket(ctx, f.tenant)
	if err != nil {
		return err
	}
	_, err = repo.StartStorageMigration(ctx, tenanth.StartStorageMigrationArgs{
		TenantID:          f.tenant,
		SourceBackendName: srcBackend, SourceBucketName: srcBucket,
		TargetBackendName: f.backend, TargetBucketName: f.dedicated,
		CleanupRetentionSeconds: 60,
	})
	if err != nil {
		return err
	}
	// The reconciler's part: the dedicated bucket exists on the backend.
	mustExec(t, ctx, f.pool, `UPDATE buckets SET provision_state = 'ready' WHERE name = $1`, f.dedicated)
	return nil
}

// drive runs the real worker against the real repository until the
// migration reaches a terminal state or the deadline passes, and returns the
// state it was left in with its recorded error.
func (f publicMigrationFixture) drive(t *testing.T, ctx context.Context) (string, string) {
	t.Helper()
	return f.driveWith(t, ctx, f.store, f.store)
}

// driveWith is drive with the worker's copier and header swapped for ones a
// test steers.
func (f publicMigrationFixture) driveWith(t *testing.T, ctx context.Context,
	copier worker.ObjectCopier, header worker.ObjectHeader,
) (string, string) {
	t.Helper()
	w := &worker.StorageMigrationWorker{
		Repo:     adapters.NewStorageMigrationRepo(sqlc.New(f.pool), f.pool),
		Copier:   copier,
		Deleter:  f.store,
		Header:   header,
		Interval: 10 * time.Millisecond,
		// Past the retention window, so a completed migration cleans up too.
		Now: func() time.Time { return time.Now().Add(time.Hour) },
	}
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	go func() { _ = w.Run(runCtx) }()
	var state, reason string
	for runCtx.Err() == nil {
		var msg *string
		if err := f.pool.QueryRow(ctx, `SELECT state, error FROM tenant_storage_migrations WHERE tenant_id = $1`,
			f.tenant).Scan(&state, &msg); err != nil {
			t.Fatalf("read migration: %v", err)
		}
		if msg != nil {
			reason = *msg
		}
		if state == worker.MigStateCleaned || state == worker.MigStateFailed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return state, reason
}

func (f publicMigrationFixture) boundTo(t *testing.T, ctx context.Context, collection string) string {
	t.Helper()
	var bucket string
	if err := f.pool.QueryRow(ctx, `SELECT b.name FROM collections c JOIN buckets b ON b.id = c.bucket_id
		WHERE c.tenant_id = $1 AND c.name = $2`, f.tenant, collection).Scan(&bucket); err != nil {
		t.Fatalf("read binding of %s: %v", collection, err)
	}
	return bucket
}

func (f publicMigrationFixture) layout(t *testing.T, ctx context.Context) string {
	t.Helper()
	var layout string
	if err := f.pool.QueryRow(ctx, `SELECT storage_layout FROM tenants WHERE id = $1`, f.tenant).Scan(&layout); err != nil {
		t.Fatalf("read layout: %v", err)
	}
	return layout
}

// A public collection never rebinds (collections_public_bucket_fixed,
// ADR-0027): its objects' URLs name the bucket. The migration moves the
// tenant's private collections and leaves its public ones where they are —
// before, the job either copied from a bucket the public objects were never
// in, or rebound them and was refused by the schema, retrying forever either
// way with the tenant stuck mid-migration.
func TestStorageMigrationLeavesPublicCollectionsInPlace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	assertMigrated := func(t *testing.T, f publicMigrationFixture) {
		t.Helper()
		if got := f.boundTo(t, ctx, "docs"); got != f.dedicated {
			t.Errorf("private collection bound to %s, want the dedicated bucket %s", got, f.dedicated)
		}
		if got := f.boundTo(t, ctx, "photos"); got != f.public {
			t.Errorf("public collection bound to %s, want it left in %s", got, f.public)
		}
		if got := f.layout(t, ctx); got != "dedicated" {
			t.Errorf("storage_layout = %s, want dedicated", got)
		}
		photo := worker.CopyLocation{BackendID: f.backend, TenantID: f.tenant, Bucket: f.public, Collection: "photos", Key: "obj-1"}
		if !f.store.has(photo) {
			t.Error("the public object is gone from its bucket")
		}
		for _, c := range f.store.touched {
			if c == "photos" {
				t.Error("the job copied or deleted an object of the public collection")
				break
			}
		}
	}

	t.Run("public and private from the start", func(t *testing.T) {
		t.Parallel()
		f := seedPublicMigrationFixture(t, ctx)
		f.addCollection(t, ctx, "docs", f.private, false)
		f.addCollection(t, ctx, "photos", f.public, true)
		// The public collection's bucket is not a second source.
		if err := f.start(t, ctx); err != nil {
			t.Fatalf("start: %v", err)
		}
		if state, reason := f.drive(t, ctx); state != worker.MigStateCleaned {
			t.Fatalf("migration left at %s (%s), want %s", state, reason, worker.MigStateCleaned)
		}
		assertMigrated(t, f)
	})

	t.Run("public collection created while copying", func(t *testing.T) {
		t.Parallel()
		f := seedPublicMigrationFixture(t, ctx)
		f.addCollection(t, ctx, "docs", f.private, false)
		if err := f.start(t, ctx); err != nil {
			t.Fatalf("start: %v", err)
		}
		// Visibility is fixed at creation, so a migration started without
		// public collections only meets one created after it started.
		f.addCollection(t, ctx, "photos", f.public, true)
		if state, reason := f.drive(t, ctx); state != worker.MigStateCleaned {
			t.Fatalf("migration left at %s (%s), want %s", state, reason, worker.MigStateCleaned)
		}
		assertMigrated(t, f)
	})

	t.Run("only public collections", func(t *testing.T) {
		t.Parallel()
		f := seedPublicMigrationFixture(t, ctx)
		f.addCollection(t, ctx, "photos", f.public, true)
		// Nothing private to move: refused before anything starts, rather
		// than copying public objects into a private bucket and then being
		// refused the rebind.
		if err := f.start(t, ctx); !errors.Is(err, tenanth.ErrNoCollectionsToMigrate) {
			t.Fatalf("start = %v, want ErrNoCollectionsToMigrate", err)
		}
		if got := f.boundTo(t, ctx, "photos"); got != f.public {
			t.Errorf("public collection bound to %s, want %s", got, f.public)
		}
	})
	// A rebind the bucket rules refuse comes back as ErrRebindRefused, which
	// the worker fails on rather than retrying forever; the transaction
	// leaves the tenant as it was.
	t.Run("refused rebind", func(t *testing.T) {
		t.Parallel()
		f := seedPublicMigrationFixture(t, ctx)
		f.addCollection(t, ctx, "docs", f.private, false)
		q := sqlc.New(f.pool)
		repo := adapters.NewStorageMigrationRepo(q, f.pool)
		// The rebind acts only on a migration in rebinding.
		if _, err := q.CreateStorageMigration(ctx, pgtype.UUID{Bytes: f.tenant, Valid: true},
			f.backend, f.private, f.backend, f.public, 0); err != nil {
			t.Fatalf("create migration: %v", err)
		}
		mustExec(t, ctx, f.pool,
			`UPDATE tenant_storage_migrations SET state = 'rebinding' WHERE tenant_id = $1`, f.tenant)
		// A private collection may not bind to a public bucket.
		err := repo.RebindTenant(ctx,
			worker.MigrationRead{TenantID: f.tenant, State: worker.MigStateRebinding}, f.backend, f.public)
		if !errors.Is(err, worker.ErrRebindRefused) {
			t.Fatalf("RebindTenant = %v, want ErrRebindRefused", err)
		}
		if got := f.boundTo(t, ctx, "docs"); got != f.private {
			t.Errorf("collection bound to %s after a refused rebind, want %s", got, f.private)
		}
		if got := f.layout(t, ctx); got != "shared" {
			t.Errorf("storage_layout = %s after a refused rebind, want shared", got)
		}
	})
}
