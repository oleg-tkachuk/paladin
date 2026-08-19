//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// TestLookupBucketReturnsBackendID proves the resolver plumbing for
// multi-backend routing (docs/backend-registry.md, ADR-0011 Phase 2):
// LookupBucket now returns (backend_id, bucket) from the same
// object_keys × storage_backends JOIN, so the storage boundary can key on
// the physical (backend, bucket) pair. Guards against a column-order swap in
// the SELECT (backend_id vs bucket_name) — the fixture's ids carry distinct
// prefixes so a swap is caught, not just a nil.
func TestLookupBucketReturnsBackendID(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)

	// Ground truth straight from the row the fixture bound.
	var wantBackend, wantBucket string
	if err := pool.QueryRow(ctx,
		`SELECT backend_id, bucket_name FROM object_keys WHERE tenant_id = $1 AND object_key = $2`,
		f.tenantID, f.objectKey,
	).Scan(&wantBackend, &wantBucket); err != nil {
		t.Fatalf("read fixture object_key binding: %v", err)
	}

	repo := adapters.NewObjectRepo(sqlc.New(pool), pool)
	backendID, bucket, err := repo.LookupBucket(ctx, f.tenantID, f.objectKey, false)
	if err != nil {
		t.Fatalf("LookupBucket: %v", err)
	}
	if backendID != wantBackend {
		t.Fatalf("backendID = %q, want %q (column-order swap in the SELECT?)", backendID, wantBackend)
	}
	if bucket != wantBucket {
		t.Fatalf("bucket = %q, want %q", bucket, wantBucket)
	}
	if backendID == bucket {
		t.Fatalf("backendID and bucket are identical (%q) — the fixture should give them distinct prefixes", backendID)
	}
}
