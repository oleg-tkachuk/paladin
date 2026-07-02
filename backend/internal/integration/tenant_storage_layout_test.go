//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/tenant"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// TestTenantStorageLayout proves migration 054 + the storage_layout plumbing
// (ADR-0011 Phase 1): a tenant created with storage_layout="dedicated"
// persists and reads back as such, while the default is "shared".
func TestTenantStorageLayout(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewTenantRepo(sqlc.New(pool), pool)

	hex := uuid.NewString()[:8]
	ded, err := repo.Create(ctx, tenant.CreateTenantArgs{
		TenantID:      uuid.New(),
		Slug:          "ded-" + hex,
		DisplayName:   "dedicated-" + hex,
		StorageLayout: "dedicated",
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
	sh, err := repo.Create(ctx, tenant.CreateTenantArgs{
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
