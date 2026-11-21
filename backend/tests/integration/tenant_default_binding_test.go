//go:build integration

// Exercises the tenant default-binding store adapter (ADR-0014 Phase 3 /
// the schema baseline (001_initial_schema.sql)) end-to-end against a real Postgres: set → get → the resolve
// lookup → bad-bucket FK → clear. Pure store/SQL behaviour, so it can only be
// verified against the real schema.
package integration

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// TestTenantDefaultBinding_MissingTenantIsNotFound: a tenant that does not
// exist owns and holds a grant on nothing, so its binding used to be refused
// as a bucket not granted — or, for a bucket that is not there either, as a
// missing bucket. The request names the tenant first; that it is missing is
// the answer.
func TestTenantDefaultBinding_MissingTenantIsNotFound(t *testing.T) {
	f := setupDispatcher(t)
	ctx := context.Background()
	owner := mustCreateTenant(t, f.h.PoolMigrate, "binding-owner")
	seedOwnedBucket(t, f.h.PoolMigrate, owner, "primary", "owned-elsewhere")
	other := mustCreateTenant(t, f.h.PoolMigrate, "binding-other")
	repo := adapters.NewTenantRepo(sqlc.New(f.h.PoolMigrate), f.h.PoolMigrate)

	for _, tc := range []struct {
		name   string
		tenant uuid.UUID
		bucket string
		want   error
	}{
		{"missing tenant, a bucket it holds no grant on", uuid.New(), "storageBackends/primary/buckets/owned-elsewhere", tenanth.ErrNotFound},
		{"missing tenant, a missing bucket", uuid.New(), "storageBackends/primary/buckets/nope", tenanth.ErrNotFound},
		{"real tenant, a bucket it holds no grant on", other, "storageBackends/primary/buckets/owned-elsewhere", admindomain.ErrBucketNotGranted},
		{"real tenant, a missing bucket", other, "storageBackends/primary/buckets/nope", tenanth.ErrDefaultBindingBucketMissing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := repo.SetDefaultBinding(ctx, tc.tenant, tc.bucket, "admin@local"); !errors.Is(err, tc.want) {
				t.Fatalf("SetDefaultBinding = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestTenantDefaultBinding_SetGetClear(t *testing.T) {
	f := setupDispatcher(t)
	ctx := context.Background()
	tid := mustCreateTenant(t, f.h.PoolMigrate, "binding-crud")
	seedOwnedBucket(t, f.h.PoolMigrate, tid, "primary", "paladin-test")

	repo := adapters.NewTenantRepo(sqlc.New(f.h.PoolMigrate), f.h.PoolMigrate)

	// No binding yet → Get is ErrNotFound; the resolve lookup reports found=false.
	if _, err := repo.GetDefaultBinding(ctx, tid); !errors.Is(err, tenanth.ErrNotFound) {
		t.Fatalf("Get before set: err = %v, want ErrNotFound", err)
	}
	if _, _, found, err := repo.TenantDefaultBinding(ctx, tid); err != nil || found {
		t.Fatalf("lookup before set: found=%v err=%v, want found=false", found, err)
	}

	// Set.
	b, err := repo.SetDefaultBinding(ctx, tid, "storageBackends/primary/buckets/paladin-test", "admin@local")
	if err != nil {
		t.Fatalf("SetDefaultBinding: %v", err)
	}
	if b.TenantID != tid || b.BackendName != "primary" || b.BucketName != "paladin-test" || b.SetBy != "admin@local" || b.SetAt.IsZero() {
		t.Fatalf("set returned %+v", b)
	}

	// Get + resolve lookup agree.
	got, err := repo.GetDefaultBinding(ctx, tid)
	if err != nil || got.BackendName != "primary" || got.BucketName != "paladin-test" {
		t.Fatalf("Get after set: %+v, err=%v", got, err)
	}
	backend, bucket, found, err := repo.TenantDefaultBinding(ctx, tid)
	if err != nil || !found || backend != "primary" || bucket != "paladin-test" {
		t.Fatalf("lookup after set: (%q,%q,%v) err=%v", backend, bucket, found, err)
	}

	// Upsert to a different bucket.
	seedOwnedBucket(t, f.h.PoolMigrate, tid, "primary", "paladin-test-2")
	if _, err := repo.SetDefaultBinding(ctx, tid, "storageBackends/primary/buckets/paladin-test-2", "admin@local"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if got, _ := repo.GetDefaultBinding(ctx, tid); got.BucketName != "paladin-test-2" {
		t.Fatalf("after upsert bucket = %q, want paladin-test-2", got.BucketName)
	}

	// A bucket that does not exist trips the composite FK.
	if _, err := repo.SetDefaultBinding(ctx, tid, "storageBackends/primary/buckets/nope", "admin@local"); !errors.Is(err, tenanth.ErrDefaultBindingBucketMissing) {
		t.Fatalf("bad bucket: err = %v, want ErrDefaultBindingBucketMissing", err)
	}

	// Clear is idempotent; after it Get is ErrNotFound again.
	if err := repo.ClearDefaultBinding(ctx, tid); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if err := repo.ClearDefaultBinding(ctx, tid); err != nil {
		t.Fatalf("clear again (idempotent): %v", err)
	}
	if _, err := repo.GetDefaultBinding(ctx, tid); !errors.Is(err, tenanth.ErrNotFound) {
		t.Fatalf("Get after clear: err = %v, want ErrNotFound", err)
	}
}
