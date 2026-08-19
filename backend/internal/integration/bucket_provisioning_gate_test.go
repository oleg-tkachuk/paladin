//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	objecth "github.com/oleg-tkachuk/paladin/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/tenant"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// TestBucketProvisioningGate proves the ADR-0011 Phase 1 write gate: while a
// dedicated tenant's bucket is provision_state != 'ready', a mutation
// (write=true) resolution returns ErrBucketProvisioning; reads still resolve;
// and once the reconciler marks it 'ready' writes resolve too.
func TestBucketProvisioningGate(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)

	const backendID = "be-gate"
	mustExec(t, ctx, pool, `INSERT INTO storage_backends (id, kind) VALUES ($1, 's3-compatible')`, backendID)

	tenantRepo := adapters.NewTenantRepo(sqlc.New(pool), pool)
	tid := uuid.New()
	hex := uuid.NewString()[:8]
	if _, err := tenantRepo.Create(ctx, tenant.CreateTenantArgs{
		TenantID:         tid,
		Slug:             "gate-" + hex,
		DisplayName:      "gate-" + hex,
		StorageLayout:    "dedicated",
		DedicatedBackend: backendID,
	}); err != nil {
		t.Fatalf("create dedicated tenant: %v", err)
	}
	bucket := "paladin-" + tid.String()

	// Bind an object_key to the tenant's (still pending) dedicated bucket.
	objectKey := "ok-" + hex
	mustExec(t, ctx, pool,
		`INSERT INTO object_keys (tenant_id, object_key, backend_id, bucket_name) VALUES ($1, $2, $3, $4)`,
		tid, objectKey, backendID, bucket)

	repo := adapters.NewObjectRepo(sqlc.New(pool), pool)

	// Mutation is gated while the bucket is provisioning.
	if _, _, err := repo.LookupBucket(ctx, tid, objectKey, true); !errors.Is(err, objecth.ErrBucketProvisioning) {
		t.Fatalf("write LookupBucket on a pending bucket: err = %v, want ErrBucketProvisioning", err)
	}
	// Reads still resolve (nothing to read yet, but resolution succeeds).
	if _, _, err := repo.LookupBucket(ctx, tid, objectKey, false); err != nil {
		t.Fatalf("read LookupBucket on a pending bucket: %v, want success", err)
	}

	// Reconciler marks the bucket ready → writes resolve.
	mustExec(t, ctx, pool,
		`UPDATE buckets SET provision_state = 'ready' WHERE backend_id = $1 AND bucket_name = $2`,
		backendID, bucket)
	if _, _, err := repo.LookupBucket(ctx, tid, objectKey, true); err != nil {
		t.Fatalf("write LookupBucket after ready: %v, want success", err)
	}
}
