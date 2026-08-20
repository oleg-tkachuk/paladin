//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	objecth "github.com/oleg-tkachuk/paladin/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/tenant"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// mkTenant inserts a bare tenant and returns its id + slug.
func mkTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, layout string) (uuid.UUID, string) {
	t.Helper()
	id := uuid.New()
	hex := uuid.NewString()[:8]
	slug := "t-" + hex
	mustExec(t, ctx, pool,
		`INSERT INTO tenants (id, slug, display_name, storage_layout) VALUES ($1, $2, $3, $4)`,
		id, slug, "tn-"+hex, layout)
	return id, slug
}

// TestAdversarial_CrossTenantDedicatedBucketBindRejected proves the DB trigger
// enforce_collection_bucket_tenancy is a hard isolation boundary: tenant B
// cannot bind an collection to tenant A's OWNED (dedicated) bucket, even by a
// direct INSERT that bypasses the application layer. Without this, a bug or a
// compromised app role could route B's writes into A's dedicated bucket.
func TestAdversarial_CrossTenantDedicatedBucketBindRejected(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)

	const backendID = "be-iso"
	mustExec(t, ctx, pool, `INSERT INTO storage_backends (name, kind) VALUES ($1, 's3-compatible')`, backendID)

	// Tenant A owns a dedicated bucket.
	tenantRepo := adapters.NewTenantRepo(sqlc.New(pool), pool)
	victimA := uuid.New()
	hexA := uuid.NewString()[:8]
	if _, err := tenantRepo.Create(ctx, tenant.CreateTenantArgs{
		TenantID: victimA, Slug: "victim-" + hexA, DisplayName: "victim-" + hexA,
		StorageLayout: "dedicated", DedicatedBackend: backendID,
	}); err != nil {
		t.Fatalf("create victim tenant: %v", err)
	}
	victimBucket := "paladin-" + victimA.String()

	// Tenant B (attacker) exists and tries to bind an collection to A's bucket.
	attackerB, _ := mkTenant(t, ctx, pool, "shared")
	_, err := pool.Exec(ctx,
		`INSERT INTO collections (tenant_id, name, bucket_id)
		 SELECT $1, $2, b.id FROM buckets b
		   JOIN storage_backends sb ON sb.id = b.backend_id
		  WHERE sb.name = $3 AND b.name = $4`,
		attackerB, "steal", backendID, victimBucket)
	if err == nil {
		t.Fatal("SECURITY: tenant B bound an collection to tenant A's dedicated bucket — tenancy trigger bypassed")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" { // check_violation raised by the trigger
		t.Fatalf("cross-tenant bind failed with the wrong error: %v (want check_violation)", err)
	}

	// Positive control: tenant A CAN bind to its own dedicated bucket.
	if _, err := pool.Exec(ctx,
		`INSERT INTO collections (tenant_id, name, bucket_id)
		 SELECT $1, $2, b.id FROM buckets b
		   JOIN storage_backends sb ON sb.id = b.backend_id
		  WHERE sb.name = $3 AND b.name = $4`,
		victimA, "docs", backendID, victimBucket); err != nil {
		t.Fatalf("owner could not bind to its own dedicated bucket: %v", err)
	}
}

// TestAdversarial_RLSFiltersCrossTenantObjects proves the objects RLS policy
// under the RESTRICTED paladin_app role (no BYPASSRLS): a session scoped to tenant
// A via the paladin.tenant_id GUC sees only A's objects; an UNSCOPED session sees
// none (closed-by-default). Guards the last-line-of-defense that a missing or
// wrong GUC cannot leak another tenant's rows.
func TestAdversarial_RLSFiltersCrossTenantObjects(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)

	const backendID, bucket = "be-rls", "shared-rls"
	mustExec(t, ctx, pool, `INSERT INTO storage_backends (name, kind) VALUES ($1, 's3-compatible')`, backendID)
	mustExec(t, ctx, pool, `INSERT INTO buckets (backend_id, name)
		 SELECT sb.id, $2 FROM storage_backends sb WHERE sb.name = $1`, backendID, bucket)

	seedObj := func(tid uuid.UUID) {
		ok := "ok-" + uuid.NewString()[:8]
		mustExec(t, ctx, pool, `INSERT INTO collections (tenant_id, name, bucket_id)
		 SELECT $1, $2, (SELECT b.id FROM buckets b JOIN storage_backends sb ON sb.id = b.backend_id
			  WHERE sb.name = $3 AND b.name = $4)`,
			tid, ok, backendID, bucket)
		mustExec(t, ctx, pool,
			`INSERT INTO objects (id, tenant_id, collection, key, state, content_type, checksum_algorithm)
			 VALUES ($1,$2,$3,$4,'AVAILABLE','text/plain',0)`,
			uuid.Must(uuid.NewV7()), tid, ok, "k-"+uuid.NewString()[:8])
	}
	tenantA, _ := mkTenant(t, ctx, pool, "shared")
	tenantB, _ := mkTenant(t, ctx, pool, "shared")
	seedObj(tenantA)
	seedObj(tenantB)

	// Enforce RLS by dropping to the restricted app role for this tx.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE paladin_app`); err != nil {
		t.Fatalf("set role paladin_app: %v", err)
	}

	// Scoped to tenant A → sees exactly A's one object.
	if _, err := tx.Exec(ctx, `SELECT set_config('paladin.tenant_id', $1, true)`, tenantA.String()); err != nil {
		t.Fatalf("set guc: %v", err)
	}
	var nA int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM objects`).Scan(&nA); err != nil {
		t.Fatalf("count as A: %v", err)
	}
	if nA != 1 {
		t.Fatalf("RLS leak: tenant-A session saw %d objects, want 1 (its own)", nA)
	}
	// Prove it's A's, not B's: B's rows must be invisible.
	var nBfromA int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM objects WHERE tenant_id = $1`, tenantB).Scan(&nBfromA); err != nil {
		t.Fatalf("count B-from-A: %v", err)
	}
	if nBfromA != 0 {
		t.Fatalf("RLS leak: tenant-A session counted %d of tenant B's objects", nBfromA)
	}

	// Unset GUC → closed-by-default (zero rows), never "all rows".
	if _, err := tx.Exec(ctx, `SELECT set_config('paladin.tenant_id', '', true)`); err != nil {
		t.Fatalf("clear guc: %v", err)
	}
	var nNone int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM objects`).Scan(&nNone); err != nil {
		t.Fatalf("count unscoped: %v", err)
	}
	if nNone != 0 {
		t.Fatalf("RLS leak: unscoped session saw %d objects, want 0 (closed-by-default)", nNone)
	}
}

// TestAdversarial_ProvisionGateOnUploadPath proves the provision-state gate
// also covers the UploadObject resolver (LookupBucketMeta), not just the
// presign LookupBucket — otherwise a dedicated tenant could push a
// multipart/streaming upload into a bucket S3 doesn't have yet.
func TestAdversarial_ProvisionGateOnUploadPath(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)

	const backendID = "be-gate2"
	mustExec(t, ctx, pool, `INSERT INTO storage_backends (name, kind) VALUES ($1, 's3-compatible')`, backendID)
	tenantRepo := adapters.NewTenantRepo(sqlc.New(pool), pool)
	tid := uuid.New()
	hex := uuid.NewString()[:8]
	if _, err := tenantRepo.Create(ctx, tenant.CreateTenantArgs{
		TenantID: tid, Slug: "gate2-" + hex, DisplayName: "gate2-" + hex,
		StorageLayout: "dedicated", DedicatedBackend: backendID,
	}); err != nil {
		t.Fatalf("create dedicated tenant: %v", err)
	}
	bucket := "paladin-" + tid.String()
	mustExec(t, ctx, pool, `INSERT INTO collections (tenant_id, name, bucket_id)
		 SELECT $1, $2, (SELECT b.id FROM buckets b JOIN storage_backends sb ON sb.id = b.backend_id
			  WHERE sb.name = $3 AND b.name = $4)`,
		tid, "docs", backendID, bucket)

	repo := adapters.NewObjectRepo(sqlc.New(pool), pool)

	// Upload path (LookupBucketMeta, write=true) must be gated while pending.
	if _, err := repo.LookupBucketMeta(ctx, tid, "docs", true); !errors.Is(err, objecth.ErrBucketProvisioning) {
		t.Fatalf("upload-path meta lookup on pending bucket: err = %v, want ErrBucketProvisioning", err)
	}
	// Read path resolves.
	if _, err := repo.LookupBucketMeta(ctx, tid, "docs", false); err != nil {
		t.Fatalf("read-path meta lookup on pending bucket: %v, want success", err)
	}
	// After ready, writes resolve.
	mustExec(t, ctx, pool, `UPDATE buckets SET provision_state='ready' WHERE backend_id=$1 AND bucket_name=$2`, backendID, bucket)
	if _, err := repo.LookupBucketMeta(ctx, tid, "docs", true); err != nil {
		t.Fatalf("upload-path meta lookup after ready: %v, want success", err)
	}
}

// TestAdversarial_CedarAuthoritativeSlugIsolation proves ADR-0012 end-to-end
// against the REAL PostgresStore: tenant membership keys on the DB slug, so a
// caller in tenant B cannot satisfy tenant A's member permit — not even by
// claiming A's slug in its principal. Verifies the slug genuinely flows from
// the tenants row, not the request.
func TestAdversarial_CedarAuthoritativeSlugIsolation(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)

	// Tenant A (slug "alpha") with a member permit keyed on its slug.
	alpha := uuid.New()
	const alphaPolicy = `permit ( principal in Tenant::"alpha", action in [Action::"GetObject"], resource );`
	mustExec(t, ctx, pool,
		`INSERT INTO tenants (id, slug, display_name, inherited_cedar_policy) VALUES ($1,'alpha','Alpha',$2)`,
		alpha, alphaPolicy)
	// Tenant B — no relation to A.
	bravo, _ := mkTenant(t, ctx, pool, "shared")

	eng := cedar.NewEngine(cedar.NewPostgresStore(pool), time.Minute)
	res := &cedar.Resource{TenantID: alpha, Collection: "docs", Key: "f"}

	// A's own member is allowed (authoritative slug "alpha" matches).
	aMember := &cedar.Principal{Subject: "a@alpha", TenantID: alpha, TenantSlug: "alpha", Roles: []string{"tenant.user"}}
	if d, err := eng.IsAuthorized(ctx, aMember, cedar.ActionGetObject, res, cedar.RequestContext{}); err != nil || d != cedar.DecisionAllow {
		t.Fatalf("A's own member: decision=%v err=%v, want Allow", d, err)
	}

	// B's admin, EVEN claiming slug "alpha", is denied A's member permit.
	bSpoof := &cedar.Principal{Subject: "b@bravo", TenantID: bravo, TenantSlug: "alpha", Roles: []string{"tenant.admin"}}
	if d, err := eng.IsAuthorized(ctx, bSpoof, cedar.ActionGetObject, res, cedar.RequestContext{}); err != nil || d != cedar.DecisionDeny {
		t.Fatalf("SECURITY: B-principal claiming A's slug: decision=%v err=%v, want Deny", d, err)
	}

	// B's platform.admin is still allowed (role permit, not membership).
	bAdmin := &cedar.Principal{Subject: "root", TenantID: bravo, Roles: []string{"platform.admin"}}
	if d, err := eng.IsAuthorized(ctx, bAdmin, cedar.ActionGetObject, res, cedar.RequestContext{}); err != nil || d != cedar.DecisionAllow {
		t.Fatalf("platform.admin cross-tenant: decision=%v err=%v, want Allow", d, err)
	}
}
