//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	objecth "github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// TestBucketProvisioningGate proves the ADR-0015 Phase 1 write gate: while a
// dedicated tenant's bucket is provision_state != 'ready', a mutation
// (write=true) resolution returns ErrBucketProvisioning, answered UNAVAILABLE;
// a provision that failed answers not ready; reads still resolve; and once the
// reconciler marks it 'ready' writes resolve too.
func TestBucketProvisioningGate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)

	const backendID = "be-gate"
	mustExec(t, ctx, pool, `INSERT INTO storage_backends (name, kind) VALUES ($1, 's3-compatible')`, backendID)

	tenantRepo := adapters.NewTenantRepo(sqlc.New(pool), pool)
	tid := uuid.New()
	hex := uuid.New().String()[:8]
	if _, err := tenantRepo.Create(ctx, tenanth.CreateTenantArgs{
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
	if _, _, err := repo.LookupBucket(ctx, tid, collection, bucketgate.BytesWrite); !errors.Is(err, objecth.ErrBucketProvisioning) {
		t.Fatalf("write LookupBucket on a pending bucket: err = %v, want ErrBucketProvisioning", err)
	}
	// Reads still resolve (nothing to read yet, but resolution succeeds).
	if _, _, err := repo.LookupBucket(ctx, tid, collection, bucketgate.Read); err != nil {
		t.Fatalf("read LookupBucket on a pending bucket: %v, want success", err)
	}
	// A client is told to retry: provisioning is temporary.
	_, _, err := repo.LookupBucket(ctx, tid, collection, bucketgate.BytesWrite)
	if got := connect.CodeOf(objecth.MapResolveErr(err)); got != connect.CodeUnavailable {
		t.Fatalf("pending write answers %v, want Unavailable", got)
	}

	// A provision that failed for good is not worth a retry: not ready.
	mustExec(t, ctx, pool,
		`UPDATE buckets SET provision_state = $3 WHERE id = (SELECT b.id FROM buckets b JOIN storage_backends sb ON sb.id = b.backend_id
			  WHERE sb.name = $1 AND b.name = $2)`,
		backendID, bucket, bucketgate.ProvisionFailed)
	_, _, err = repo.LookupBucket(ctx, tid, collection, bucketgate.BytesWrite)
	if !errors.Is(err, bucketgate.ErrBucketNotReady) {
		t.Fatalf("write LookupBucket on a failed bucket: err = %v, want ErrBucketNotReady", err)
	}
	if got := connect.CodeOf(objecth.MapResolveErr(err)); got != connect.CodeFailedPrecondition {
		t.Fatalf("failed-bucket write answers %v, want FailedPrecondition", got)
	}

	// Reconciler marks the bucket ready → writes resolve.
	mustExec(t, ctx, pool,
		`UPDATE buckets SET provision_state = 'ready' WHERE id = (SELECT b.id FROM buckets b JOIN storage_backends sb ON sb.id = b.backend_id
			  WHERE sb.name = $1 AND b.name = $2)`,
		backendID, bucket)
	if _, _, err := repo.LookupBucket(ctx, tid, collection, bucketgate.BytesWrite); err != nil {
		t.Fatalf("write LookupBucket after ready: %v, want success", err)
	}
}
