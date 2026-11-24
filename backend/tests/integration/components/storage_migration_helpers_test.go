//go:build integration

package components

import (
	"context"
	"fmt"
	"testing"
	"uuid"

	objecth "github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
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

// upload is a tenant write whose URL has expired and settled: an AVAILABLE
// object row placed in the bucket the collection is bound to now, and its
// bytes there.
func (f publicMigrationFixture) upload(t *testing.T, ctx context.Context, collection, key string, size int64) {
	t.Helper()
	mustExec(t, ctx, f.pool, `INSERT INTO objects (tenant_id, collection_id, path, state, content_type, size_bytes, placement_bucket_id)
		SELECT $1, c.id, $3, 'AVAILABLE', 'text/plain', $4, c.bucket_id FROM collections c
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

func storeLoc(l objecth.Location) worker.CopyLocation {
	return worker.CopyLocation{BackendID: l.BackendID, TenantID: l.TenantID, Bucket: l.Bucket, Collection: l.Collection, Key: l.Key}
}

// storeObjects is objecth.Storage and adapters.HeadProber over the fake store
// the migration worker copies through: what a CompleteObject or a reconciler
// HEAD sees is what the migration put there.
type storeObjects struct {
	objecth.Storage
	s        *fakeObjectStore
	notFound error // what a HEAD of nothing wraps
}

func (o storeObjects) Head(_ context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key, _ string) (string, int64, string, string, error) {
	l := worker.CopyLocation{BackendID: backendID, TenantID: tenantID, Bucket: bucket, Collection: collection, Key: key}
	size, ok := o.s.sizeOf(l)
	if !ok {
		return "", 0, "", "", fmt.Errorf("head %s: %w", storeKey(l), o.notFound)
	}
	return "etag-" + bucket, size, "", "", nil
}

func (o storeObjects) CopyObject(ctx context.Context, src, dst objecth.Location) error {
	return o.s.CopyObject(ctx, storeLoc(src), storeLoc(dst))
}

func (o storeObjects) DeleteObject(ctx context.Context, backendID, bucket string, tenantID uuid.UUID, collection, key string) error {
	return o.s.DeleteObject(ctx, worker.CopyLocation{BackendID: backendID, TenantID: tenantID, Bucket: bucket, Collection: collection, Key: key})
}

// presignBeforeRebind registers lateKey as UploadObject does, its URL signed

// migrationState is the tenant's migration's state.
func (f publicMigrationFixture) migrationState(t *testing.T, ctx context.Context) string {
	t.Helper()
	var state string
	if err := f.pool.QueryRow(ctx, `SELECT state FROM tenant_storage_migrations WHERE tenant_id = $1`, f.tenant).Scan(&state); err != nil {
		t.Fatalf("read migration: %v", err)
	}
	return state
}
