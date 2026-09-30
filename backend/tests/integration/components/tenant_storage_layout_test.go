//go:build integration

package components

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// TestTenantStorageLayout proves the schema baseline (001_initial_schema.sql) + the storage_layout plumbing
// (ADR-0015 Phase 1): a tenant created with storage_layout="dedicated"
// persists and reads back as such, while the default is "shared".
func TestTenantStorageLayout(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewTenantRepo(sqlc.New(pool), pool)

	// A dedicated-layout tenant provisions a bucket bound to a backend, and
	// buckets.backend_id is an FK into storage_backends (feature 002 made
	// backends first-class). Seed one first and pass it as DedicatedBackend —
	// mirrors TestCreateDedicatedTenantProvisionsBucket. The bare Create used
	// to work before the FK landed; the suite ran nowhere, so the drift went
	// unnoticed until it was wired into CI.
	const backendID = "be-layout"
	mustExec(t, ctx, pool, `INSERT INTO storage_backends (name, kind) VALUES ($1, 's3-compatible')`, backendID)

	hex := uuid.NewString()[:8]
	ded, err := repo.Create(ctx, tenanth.CreateTenantArgs{
		TenantID:         uuid.New(),
		Slug:             "ded-" + hex,
		DisplayName:      "dedicated-" + hex,
		StorageLayout:    "dedicated",
		DedicatedBackend: backendID,
	})
	if err != nil {
		t.Fatalf("create dedicated tenant: %v", err)
	}
	if ded.StorageLayout != "dedicated" {
		t.Fatalf("created tenant storage_layout = %q, want %q", ded.StorageLayout, "dedicated")
	}
	got, err := repo.Get(ctx, ded.TenantID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.StorageLayout != "dedicated" {
		t.Fatalf("read-back storage_layout = %q, want %q", got.StorageLayout, "dedicated")
	}

	// Default (empty) → "shared".
	sh, err := repo.Create(ctx, tenanth.CreateTenantArgs{
		TenantID:    uuid.New(),
		Slug:        "sh-" + hex,
		DisplayName: "shared-" + hex,
	})
	if err != nil {
		t.Fatalf("create shared tenant: %v", err)
	}
	if sh.StorageLayout != "shared" {
		t.Fatalf("default storage_layout = %q, want %q", sh.StorageLayout, "shared")
	}
}
