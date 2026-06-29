//go:build integration

// Integration coverage for the storage-backend enable/disable state
// (feature 002). Exercises the repo SetEnabled path against a real
// Postgres: OCC bumping, version-mismatch, not-found, and the enabled
// round-trip through Get/List.
package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/bucketh"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/tests/integration/pgharness"
)

func TestBackendSetEnabled_RepoRoundTrip(t *testing.T) {
	h := pgharness.Setup(t)
	repo := adapters.NewBackendRepoV2(sqlc.New(h.PoolMigrate), h.PoolMigrate)
	ctx := context.Background()

	seedBackend(t, h.PoolMigrate, "be-roundtrip")

	// Fresh backends default to enabled (migration 037 DEFAULT true).
	got, err := repo.Get(ctx, "be-roundtrip")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Enabled {
		t.Fatalf("new backend: Enabled=false, want true (DEFAULT true)")
	}
	rv := got.ResourceVersion

	// Disable under OCC. The BEFORE UPDATE trigger bumps resource_version.
	if err := repo.SetEnabled(ctx, "be-roundtrip", false, rv); err != nil {
		t.Fatalf("disable: %v", err)
	}
	got, err = repo.Get(ctx, "be-roundtrip")
	if err != nil {
		t.Fatalf("get after disable: %v", err)
	}
	if got.Enabled {
		t.Fatalf("after disable: Enabled=true, want false")
	}
	if got.ResourceVersion <= rv {
		t.Fatalf("resource_version not bumped: was %d, now %d", rv, got.ResourceVersion)
	}

	// Re-enable restores serving state.
	if err := repo.SetEnabled(ctx, "be-roundtrip", true, got.ResourceVersion); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if again, _ := repo.Get(ctx, "be-roundtrip"); !again.Enabled {
		t.Fatalf("after re-enable: Enabled=false, want true")
	}
}

func TestBackendSetEnabled_VersionMismatch(t *testing.T) {
	h := pgharness.Setup(t)
	repo := adapters.NewBackendRepoV2(sqlc.New(h.PoolMigrate), h.PoolMigrate)
	ctx := context.Background()

	seedBackend(t, h.PoolMigrate, "be-occ")

	// A stale (wrong) expected version must be refused — no silent
	// overwrite. expectedVersion=999 will never match.
	err := repo.SetEnabled(ctx, "be-occ", false, 999)
	if !errors.Is(err, admindomain.ErrVersionMismatch) {
		t.Fatalf("stale version: err=%v, want ErrVersionMismatch", err)
	}
	// State unchanged.
	if got, _ := repo.Get(ctx, "be-occ"); !got.Enabled {
		t.Fatalf("backend flipped despite version mismatch")
	}
}

func TestBackendSetEnabled_NotFound(t *testing.T) {
	h := pgharness.Setup(t)
	repo := adapters.NewBackendRepoV2(sqlc.New(h.PoolMigrate), h.PoolMigrate)

	err := repo.SetEnabled(context.Background(), "does-not-exist", false, 1)
	if !errors.Is(err, admindomain.ErrNotFound) {
		t.Fatalf("missing backend: err=%v, want ErrNotFound", err)
	}
}

// TestBackendDisabled_ResolverGate proves the data-plane chokepoint:
// once a backend is disabled, the bucket resolver (LookupBucket /
// LookupBucketMeta) — through which EVERY object/presign/multipart/copy
// op resolves its bucket before touching the object store — returns
// object.ErrBackendDisabled. Because the resolver fails first, no S3
// call can be reached (SC-001). Re-enabling restores resolution.
func TestBackendDisabled_ResolverGate(t *testing.T) {
	h := pgharness.Setup(t)
	repo := adapters.NewObjectRepo(sqlc.New(h.PoolMigrate), h.PoolMigrate)
	ctx := context.Background()

	tenantID := mustCreateTenant(t, h.PoolMigrate, "gate-tenant")
	seedBackend(t, h.PoolMigrate, "gate-be")
	mustSeedBucketAndKey(t, h.PoolMigrate, tenantID, "gate-be", "gate-bucket", "docs")

	// Enabled by default → resolution succeeds.
	if _, err := repo.LookupBucket(ctx, tenantID, "docs"); err != nil {
		t.Fatalf("enabled LookupBucket: %v", err)
	}
	if _, err := repo.LookupBucketMeta(ctx, tenantID, "docs"); err != nil {
		t.Fatalf("enabled LookupBucketMeta: %v", err)
	}

	// Disable the backend.
	be := adapters.NewBackendRepoV2(sqlc.New(h.PoolMigrate), h.PoolMigrate)
	cur, _ := be.Get(ctx, "gate-be")
	if err := be.SetEnabled(ctx, "gate-be", false, cur.ResourceVersion); err != nil {
		t.Fatalf("disable: %v", err)
	}

	// Both resolver paths now refuse with the sentinel — the gate every
	// object op funnels through.
	if _, err := repo.LookupBucket(ctx, tenantID, "docs"); !errors.Is(err, object.ErrBackendDisabled) {
		t.Errorf("disabled LookupBucket: err=%v, want ErrBackendDisabled", err)
	}
	if _, err := repo.LookupBucketMeta(ctx, tenantID, "docs"); !errors.Is(err, object.ErrBackendDisabled) {
		t.Errorf("disabled LookupBucketMeta: err=%v, want ErrBackendDisabled", err)
	}

	// Re-enable → resolution works again (reversible, no data touched).
	cur, _ = be.Get(ctx, "gate-be")
	if err := be.SetEnabled(ctx, "gate-be", true, cur.ResourceVersion); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if _, err := repo.LookupBucket(ctx, tenantID, "docs"); err != nil {
		t.Errorf("re-enabled LookupBucket: %v", err)
	}
}

// TestBackendDisabled_CreateBucketRefused covers the one admin-plane op
// that does not flow through the object resolver: CreateBucket must
// refuse binding a bucket to a disabled backend (FailedPrecondition).
func TestBackendDisabled_CreateBucketRefused(t *testing.T) {
	h := pgharness.Setup(t)
	q := sqlc.New(h.PoolMigrate)
	handler := bucketh.NewHandler(adapters.NewBucketRepoV2(q, h.PoolMigrate), nil, allowAll{})
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject: "tester", Roles: []string{"platform.admin"},
	})

	seedBackend(t, h.PoolMigrate, "cb-be")
	be := adapters.NewBackendRepoV2(q, h.PoolMigrate)
	cur, _ := be.Get(ctx, "cb-be")
	if err := be.SetEnabled(ctx, "cb-be", false, cur.ResourceVersion); err != nil {
		t.Fatalf("disable: %v", err)
	}

	_, err := handler.CreateBucket(ctx, bucketh.CreateBucketInput{
		Bucket: admindomain.Bucket{BackendID: "cb-be", BucketName: "nope"},
	})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("create bucket on disabled backend: got %v, want FailedPrecondition", connect.CodeOf(err))
	}

	// Re-enable → CreateBucket succeeds.
	cur, _ = be.Get(ctx, "cb-be")
	if err := be.SetEnabled(ctx, "cb-be", true, cur.ResourceVersion); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if _, err := handler.CreateBucket(ctx, bucketh.CreateBucketInput{
		Bucket: admindomain.Bucket{BackendID: "cb-be", BucketName: "ok-bucket"},
	}); err != nil {
		t.Fatalf("create bucket on enabled backend: %v", err)
	}
}

// allowAll is a permissive cedar.Authorizer stub so the integration test
// exercises the disabled-backend gate, not the policy layer.
type allowAll struct{}

func (allowAll) IsAuthorized(_ context.Context, _ *cedar.Principal, _ string, _ *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionAllow, nil
}

// TestBackendDisable_ReversibleNoDataLoss covers US2 / FR-007 / SC-002:
// disabling then re-enabling a backend leaves stored object rows
// completely untouched and fully restores access. Disabling only flips a
// Postgres flag — it never reads or mutates object data.
func TestBackendDisable_ReversibleNoDataLoss(t *testing.T) {
	h := pgharness.Setup(t)
	repo := adapters.NewObjectRepo(sqlc.New(h.PoolMigrate), h.PoolMigrate)
	be := adapters.NewBackendRepoV2(sqlc.New(h.PoolMigrate), h.PoolMigrate)
	ctx := context.Background()

	tenantID := mustCreateTenant(t, h.PoolMigrate, "rev-tenant")
	seedBackend(t, h.PoolMigrate, "rev-be")
	mustSeedBucketAndKey(t, h.PoolMigrate, tenantID, "rev-be", "rev-bucket", "docs")
	mustInsertObject(t, h.PoolMigrate, tenantID, "docs", "key-rev")

	before := mustReadObjectFingerprint(t, h.PoolMigrate, tenantID, "key-rev")

	// Disable → access refused.
	cur, _ := be.Get(ctx, "rev-be")
	if err := be.SetEnabled(ctx, "rev-be", false, cur.ResourceVersion); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := repo.LookupBucket(ctx, tenantID, "docs"); !errors.Is(err, object.ErrBackendDisabled) {
		t.Fatalf("expected disabled refusal, got %v", err)
	}

	// Re-enable → access restored.
	cur, _ = be.Get(ctx, "rev-be")
	if err := be.SetEnabled(ctx, "rev-be", true, cur.ResourceVersion); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if _, err := repo.LookupBucket(ctx, tenantID, "docs"); err != nil {
		t.Fatalf("re-enabled resolution: %v", err)
	}

	// The object row is byte-for-byte the same — disable/enable touched
	// no object data.
	after := mustReadObjectFingerprint(t, h.PoolMigrate, tenantID, "key-rev")
	if before != after {
		t.Errorf("object row changed across disable/enable:\n before=%q\n after =%q", before, after)
	}
}

func mustReadObjectFingerprint(t *testing.T, pool *pgxpool.Pool, tenantID uuid.UUID, key string) string {
	t.Helper()
	var state, contentType string
	var size int64
	if err := pool.QueryRow(context.Background(), `
        SELECT state, content_type, COALESCE(size_bytes, 0)
        FROM objects WHERE tenant_id = $1 AND key = $2
    `, tenantID, key).Scan(&state, &contentType, &size); err != nil {
		t.Fatalf("read object fingerprint: %v", err)
	}
	return fmt.Sprintf("%s|%s|%d", state, contentType, size)
}

func mustSeedBucketAndKey(t *testing.T, pool *pgxpool.Pool, tenantID uuid.UUID, backendID, bucketName, objectKey string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
        INSERT INTO buckets (backend_id, bucket_name) VALUES ($1, $2)
        ON CONFLICT DO NOTHING
    `, backendID, bucketName); err != nil {
		t.Fatalf("seed bucket: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
        INSERT INTO object_keys (tenant_id, object_key, backend_id, bucket_name)
        VALUES ($1, $2, $3, $4)
    `, tenantID, objectKey, backendID, bucketName); err != nil {
		t.Fatalf("seed object_key: %v", err)
	}
}

func seedBackend(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
        INSERT INTO storage_backends (id, kind, region, endpoint)
        VALUES ($1, 's3-compatible', 'us-east-1', 'http://localhost')
        ON CONFLICT (id) DO NOTHING
    `, id); err != nil {
		t.Fatalf("seed backend %q: %v", id, err)
	}
}
