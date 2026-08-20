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

// TestCreateDedicatedTenantProvisionsBucket proves ADR-0011 Phase 1
// provisioning: CreateTenant with storage_layout="dedicated" inserts, in the
// same tx, a pending bucket owned by the tenant plus a default binding to it.
// The (backend-routed) bucket reconciler then creates it physically.
func TestCreateDedicatedTenantProvisionsBucket(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)

	const backendID = "be-dedicated"
	mustExec(t, ctx, pool, `INSERT INTO storage_backends (name, kind) VALUES ($1, 's3-compatible')`, backendID)

	repo := adapters.NewTenantRepo(sqlc.New(pool), pool)
	tid := uuid.New()
	hex := uuid.NewString()[:8]
	tn, err := repo.Create(ctx, tenant.CreateTenantArgs{
		TenantID:         tid,
		Slug:             "ded-" + hex,
		DisplayName:      "dedicated-" + hex,
		StorageLayout:    "dedicated",
		DedicatedBackend: backendID,
	})
	if err != nil {
		t.Fatalf("create dedicated tenant: %v", err)
	}
	if tn.StorageLayout != "dedicated" {
		t.Fatalf("storage_layout = %q, want dedicated", tn.StorageLayout)
	}

	wantBucket := "paladin-" + tid.String()

	// A pending bucket owned by the tenant exists on the chosen backend.
	var (
		gotBackend, gotState string
		gotOwner             uuid.UUID
	)
	if err := pool.QueryRow(ctx,
		`SELECT backend_id, provision_state, owner_tenant_id FROM buckets WHERE bucket_name = $1`,
		wantBucket,
	).Scan(&gotBackend, &gotState, &gotOwner); err != nil {
		t.Fatalf("read provisioned bucket %q: %v", wantBucket, err)
	}
	if gotBackend != backendID {
		t.Fatalf("bucket backend = %q, want %q", gotBackend, backendID)
	}
	if gotState != "pending" {
		t.Fatalf("bucket provision_state = %q, want pending (the reconciler provisions it)", gotState)
	}
	if gotOwner != tid {
		t.Fatalf("bucket owner_tenant_id = %s, want %s", gotOwner, tid)
	}

	// The tenant's default binding points at its dedicated bucket.
	var bindBackend, bindBucket string
	if err := pool.QueryRow(ctx,
		`SELECT backend_id, bucket_name FROM tenant_default_bindings WHERE tenant_id = $1`,
		tid,
	).Scan(&bindBackend, &bindBucket); err != nil {
		t.Fatalf("read default binding: %v", err)
	}
	if bindBackend != backendID || bindBucket != wantBucket {
		t.Fatalf("default binding = (%s, %s), want (%s, %s)", bindBackend, bindBucket, backendID, wantBucket)
	}
}
