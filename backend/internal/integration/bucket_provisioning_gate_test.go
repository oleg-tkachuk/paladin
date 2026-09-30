//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	objecth "github.com/oleg-tkachuk/paladin/backend/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/tenant"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// TestBucketProvisioningGate proves the ADR-0011 Phase 1 write gate: while a
// dedicated tenant's bucket is provision_state != 'ready', a mutation
// (write=true) resolution returns ErrBucketProvisioning; reads still resolve;
// and once the reconciler marks it 'ready' writes resolve too.
func TestBucketProvisioningGate(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)

	const backendID = "be-gate"
	mustExec(t, ctx, pool, `INSERT INTO storage_backends (name, kind) VALUES ($1, 's3-compatible')`, backendID)

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

	// Bind a collection to the tenant's (still pending) dedicated bucket.
	collection := "ok-" + hex
	mustExec(t, ctx, pool,
		`INSERT INTO collections (tenant_id, name, bucket_id)
		 SELECT $1, $2, b.id FROM buckets b
		   JOIN storage_backends sb ON sb.id = b.backend_id
		  WHERE sb.name = $3 AND b.name = $4`,
		tid, collection, backendID, bucket)

	repo := adapters.NewObjectRepo(sqlc.New(pool), pool)

	// Mutation is gated while the bucket is provisioning.
	if _, _, err := repo.LookupBucket(ctx, tid, collection, true); !errors.Is(err, objecth.ErrBucketProvisioning) {
		t.Fatalf("write LookupBucket on a pending bucket: err = %v, want ErrBucketProvisioning", err)
	}
	// Reads still resolve (nothing to read yet, but resolution succeeds).
	if _, _, err := repo.LookupBucket(ctx, tid, collection, false); err != nil {
		t.Fatalf("read LookupBucket on a pending bucket: %v, want success", err)
	}

	// Reconciler marks the bucket ready → writes resolve.
	mustExec(t, ctx, pool,
		`UPDATE buckets SET provision_state = 'ready' WHERE id = (SELECT b.id FROM buckets b JOIN storage_backends sb ON sb.id = b.backend_id
			  WHERE sb.name = $1 AND b.name = $2)`,
		backendID, bucket)
	if _, _, err := repo.LookupBucket(ctx, tid, collection, true); err != nil {
		t.Fatalf("write LookupBucket after ready: %v, want success", err)
	}
}
