//go:build integration

// Exercises the tenant default-binding store adapter (ADR-0010 Phase 3 /
// migration 034) end-to-end against a real Postgres: set → get → the resolve
// lookup → bad-bucket FK → clear. Pure store/SQL behaviour, so it can only be
// verified against the real schema.
package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/tenant"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

func TestTenantDefaultBinding_SetGetClear(t *testing.T) {
	f := setupDispatcher(t)
	ctx := context.Background()
	tid := mustCreateTenant(t, f.h.PoolMigrate, "binding-crud")
	seedOwnedBucket(t, f.h.PoolMigrate, tid, "primary", "paladin-test")

	repo := adapters.NewTenantRepo(sqlc.New(f.h.PoolMigrate), f.h.PoolMigrate)

	// No binding yet → Get is ErrNotFound; the resolve lookup reports found=false.
	if _, err := repo.GetDefaultBinding(ctx, tid); !errors.Is(err, tenant.ErrNotFound) {
		t.Fatalf("Get before set: err = %v, want ErrNotFound", err)
	}
	if _, _, found, err := repo.TenantDefaultBinding(ctx, tid); err != nil || found {
		t.Fatalf("lookup before set: found=%v err=%v, want found=false", found, err)
	}

	// Set.
	b, err := repo.SetDefaultBinding(ctx, tid, "primary", "paladin-test", "admin@local")
	if err != nil {
		t.Fatalf("SetDefaultBinding: %v", err)
	}
	if b.TenantID != tid || b.BackendID != "primary" || b.BucketName != "paladin-test" || b.SetBy != "admin@local" || b.SetAt.IsZero() {
		t.Fatalf("set returned %+v", b)
	}

	// Get + resolve lookup agree.
	got, err := repo.GetDefaultBinding(ctx, tid)
	if err != nil || got.BackendID != "primary" || got.BucketName != "paladin-test" {
		t.Fatalf("Get after set: %+v, err=%v", got, err)
	}
	backend, bucket, found, err := repo.TenantDefaultBinding(ctx, tid)
	if err != nil || !found || backend != "primary" || bucket != "paladin-test" {
		t.Fatalf("lookup after set: (%q,%q,%v) err=%v", backend, bucket, found, err)
	}

	// Upsert to a different bucket.
	seedOwnedBucket(t, f.h.PoolMigrate, tid, "primary", "paladin-test-2")
	if _, err := repo.SetDefaultBinding(ctx, tid, "primary", "paladin-test-2", "admin@local"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if got, _ := repo.GetDefaultBinding(ctx, tid); got.BucketName != "paladin-test-2" {
		t.Fatalf("after upsert bucket = %q, want paladin-test-2", got.BucketName)
	}

	// A bucket that does not exist trips the composite FK.
	if _, err := repo.SetDefaultBinding(ctx, tid, "primary", "nope", "admin@local"); !errors.Is(err, tenant.ErrDefaultBindingBucketMissing) {
		t.Fatalf("bad bucket: err = %v, want ErrDefaultBindingBucketMissing", err)
	}

	// Clear is idempotent; after it Get is ErrNotFound again.
	if err := repo.ClearDefaultBinding(ctx, tid); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if err := repo.ClearDefaultBinding(ctx, tid); err != nil {
		t.Fatalf("clear again (idempotent): %v", err)
	}
	if _, err := repo.GetDefaultBinding(ctx, tid); !errors.Is(err, tenant.ErrNotFound) {
		t.Fatalf("Get after clear: err = %v, want ErrNotFound", err)
	}
}
