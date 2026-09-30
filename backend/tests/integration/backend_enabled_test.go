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
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/bucketh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

func TestBackendSetEnabled_RepoRoundTrip(t *testing.T) {
	h := pgharness.Setup(t)
	repo := adapters.NewBackendRepoV2(sqlc.New(h.PoolMigrate), h.PoolMigrate)
	ctx := context.Background()

	seedBackend(t, h.PoolMigrate, "be-roundtrip")

	// Fresh backends default to enabled (the schema baseline (001_initial_schema.sql) DEFAULT true).
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
	if _, _, err := repo.LookupBucket(ctx, tenantID, "docs", false); err != nil {
		t.Fatalf("enabled LookupBucket: %v", err)
	}
	if _, err := repo.LookupBucketMeta(ctx, tenantID, "docs", false); err != nil {
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
	if _, _, err := repo.LookupBucket(ctx, tenantID, "docs", false); !errors.Is(err, objecth.ErrBackendDisabled) {
		t.Errorf("disabled LookupBucket: err=%v, want ErrBackendDisabled", err)
	}
	if _, err := repo.LookupBucketMeta(ctx, tenantID, "docs", false); !errors.Is(err, objecth.ErrBackendDisabled) {
		t.Errorf("disabled LookupBucketMeta: err=%v, want ErrBackendDisabled", err)
	}

	// Re-enable → resolution works again (reversible, no data touched).
	cur, _ = be.Get(ctx, "gate-be")
	if err := be.SetEnabled(ctx, "gate-be", true, cur.ResourceVersion); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if _, _, err := repo.LookupBucket(ctx, tenantID, "docs", false); err != nil {
		t.Errorf("re-enabled LookupBucket: %v", err)
	}
}

// TestBackendHealth_RecordAndSurface proves the derived-health round-trip
// (the schema baseline (001_initial_schema.sql)): Get/List start at "unknown" (LEFT JOIN COALESCE), SetHealth
// upserts into the separate storage_backend_health table, and the outcome
// surfaces on the backend read — WITHOUT bumping resource_version (health is
// not an operator config change).
func TestBackendHealth_RecordAndSurface(t *testing.T) {
	h := pgharness.Setup(t)
	be := adapters.NewBackendRepoV2(sqlc.New(h.PoolMigrate), h.PoolMigrate)
	ctx := context.Background()

	seedBackend(t, h.PoolMigrate, "health-be")

	got, err := be.Get(ctx, "health-be")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.HealthStatus != "unknown" {
		t.Errorf("initial health = %q, want unknown", got.HealthStatus)
	}
	rvBefore := got.ResourceVersion

	// Record a failed probe.
	if err := be.SetHealth(ctx, "health-be", "error", "dial tcp: refused", time.Now().UTC()); err != nil {
		t.Fatalf("set health error: %v", err)
	}
	got, _ = be.Get(ctx, "health-be")
	if got.HealthStatus != "error" || got.HealthMessage != "dial tcp: refused" {
		t.Errorf("after error probe: status=%q message=%q", got.HealthStatus, got.HealthMessage)
	}
	if got.HealthCheckedAt.IsZero() {
		t.Error("health_checked_at should be set after a probe")
	}
	// Health is NOT a config change — resource_version must not move.
	if got.ResourceVersion != rvBefore {
		t.Errorf("resource_version moved on health write: %d → %d", rvBefore, got.ResourceVersion)
	}

	// A subsequent OK probe overwrites (upsert) and clears the message.
	if err := be.SetHealth(ctx, "health-be", "ok", "", time.Now().UTC()); err != nil {
		t.Fatalf("set health ok: %v", err)
	}
	got, _ = be.Get(ctx, "health-be")
	if got.HealthStatus != "ok" || got.HealthMessage != "" {
		t.Errorf("after ok probe: status=%q message=%q, want ok/empty", got.HealthStatus, got.HealthMessage)
	}
}

// TestBackendMaintenance_AdvisoryNotAGate proves the operator-set maintenance
// flag (the schema baseline (001_initial_schema.sql)) round-trips via the repo AND is purely advisory: a
// backend flagged for maintenance still resolves reads AND writes (it is a UI
// label, not a resolver gate — unlike enabled/read_only).
func TestBackendMaintenance_AdvisoryNotAGate(t *testing.T) {
	h := pgharness.Setup(t)
	repo := adapters.NewObjectRepo(sqlc.New(h.PoolMigrate), h.PoolMigrate)
	be := adapters.NewBackendRepoV2(sqlc.New(h.PoolMigrate), h.PoolMigrate)
	ctx := context.Background()

	tenantID := mustCreateTenant(t, h.PoolMigrate, "maint-tenant")
	seedBackend(t, h.PoolMigrate, "maint-be")
	mustSeedBucketAndKey(t, h.PoolMigrate, tenantID, "maint-be", "maint-bucket", "docs")

	cur, _ := be.Get(ctx, "maint-be")
	if cur.Maintenance {
		t.Fatal("maintenance should default to false")
	}
	if err := be.SetMaintenance(ctx, "maint-be", true, cur.ResourceVersion); err != nil {
		t.Fatalf("set maintenance: %v", err)
	}
	got, _ := be.Get(ctx, "maint-be")
	if !got.Maintenance {
		t.Error("maintenance flag did not persist")
	}
	// Advisory: neither reads nor writes are gated by maintenance.
	if _, _, err := repo.LookupBucket(ctx, tenantID, "docs", false); err != nil {
		t.Errorf("read under maintenance should resolve: %v", err)
	}
	if _, _, err := repo.LookupBucket(ctx, tenantID, "docs", true); err != nil {
		t.Errorf("write under maintenance should resolve (advisory, not a gate): %v", err)
	}
}

// TestBackendReadOnly_ResolverGate proves the drain (read-only) split by
// operation class (the schema baseline (001_initial_schema.sql)): on an enabled+read_only backend, the
// resolver refuses MUTATIONS (write=true) with object.ErrBackendReadOnly but
// still serves READS (write=false). Clearing read_only restores writes.
func TestBackendReadOnly_ResolverGate(t *testing.T) {
	h := pgharness.Setup(t)
	repo := adapters.NewObjectRepo(sqlc.New(h.PoolMigrate), h.PoolMigrate)
	ctx := context.Background()

	tenantID := mustCreateTenant(t, h.PoolMigrate, "drain-tenant")
	seedBackend(t, h.PoolMigrate, "drain-be")
	mustSeedBucketAndKey(t, h.PoolMigrate, tenantID, "drain-be", "drain-bucket", "docs")

	// Writable by default: a mutation resolves.
	if _, _, err := repo.LookupBucket(ctx, tenantID, "docs", true); err != nil {
		t.Fatalf("writable LookupBucket(write): %v", err)
	}

	// Drain the backend (enabled stays true).
	be := adapters.NewBackendRepoV2(sqlc.New(h.PoolMigrate), h.PoolMigrate)
	cur, _ := be.Get(ctx, "drain-be")
	if err := be.SetReadOnly(ctx, "drain-be", true, cur.ResourceVersion); err != nil {
		t.Fatalf("drain: %v", err)
	}

	// Mutations are refused with the read-only sentinel …
	if _, _, err := repo.LookupBucket(ctx, tenantID, "docs", true); !errors.Is(err, objecth.ErrBackendReadOnly) {
		t.Errorf("drained LookupBucket(write): err=%v, want ErrBackendReadOnly", err)
	}
	if _, err := repo.LookupBucketMeta(ctx, tenantID, "docs", true); !errors.Is(err, objecth.ErrBackendReadOnly) {
		t.Errorf("drained LookupBucketMeta(write): err=%v, want ErrBackendReadOnly", err)
	}
	// … but reads still resolve — the whole point of a drain.
	if _, _, err := repo.LookupBucket(ctx, tenantID, "docs", false); err != nil {
		t.Errorf("drained LookupBucket(read): err=%v, want success", err)
	}
	if _, err := repo.LookupBucketMeta(ctx, tenantID, "docs", false); err != nil {
		t.Errorf("drained LookupBucketMeta(read): err=%v, want success", err)
	}

	// Undrain → writes resolve again (reversible, no data touched).
	cur, _ = be.Get(ctx, "drain-be")
	if err := be.SetReadOnly(ctx, "drain-be", false, cur.ResourceVersion); err != nil {
		t.Fatalf("undrain: %v", err)
	}
	if _, _, err := repo.LookupBucket(ctx, tenantID, "docs", true); err != nil {
		t.Errorf("undrained LookupBucket(write): %v", err)
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
	if _, _, err := repo.LookupBucket(ctx, tenantID, "docs", false); !errors.Is(err, objecth.ErrBackendDisabled) {
		t.Fatalf("expected disabled refusal, got %v", err)
	}

	// Re-enable → access restored.
	cur, _ = be.Get(ctx, "rev-be")
	if err := be.SetEnabled(ctx, "rev-be", true, cur.ResourceVersion); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if _, _, err := repo.LookupBucket(ctx, tenantID, "docs", false); err != nil {
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
        FROM objects WHERE tenant_id = $1 AND path = $2
    `, tenantID, key).Scan(&state, &contentType, &size); err != nil {
		t.Fatalf("read object fingerprint: %v", err)
	}
	return fmt.Sprintf("%s|%s|%d", state, contentType, size)
}

func mustSeedBucketAndKey(t *testing.T, pool *pgxpool.Pool, tenantID uuid.UUID, backendID, bucketName, collection string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
        INSERT INTO buckets (backend_id, name)
		 SELECT sb.id, $2 FROM storage_backends sb WHERE sb.name = $1
        ON CONFLICT DO NOTHING
    `, backendID, bucketName); err != nil {
		t.Fatalf("seed bucket: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
        INSERT INTO collections (tenant_id, name, bucket_id)
		 SELECT $1, $2, b.id FROM buckets b
		   JOIN storage_backends sb ON sb.id = b.backend_id
		  WHERE sb.name = $3 AND b.name = $4
    `, tenantID, collection, backendID, bucketName); err != nil {
		t.Fatalf("seed collection: %v", err)
	}
}

func seedBackend(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
        INSERT INTO storage_backends (name, kind, region, endpoint)
        VALUES ($1, 's3-compatible', 'us-east-1', 'http://localhost')
        ON CONFLICT (name) DO NOTHING
    `, id); err != nil {
		t.Fatalf("seed backend %q: %v", id, err)
	}
}

// TestResolverGates_AllThreeRepos closes the gap the two tests above leave
// open. Their comments claim the gate covers "EVERY object/presign/multipart
// op", but both drive only ObjectRepo — and the resolver is not shared: the
// same 28-line body, query and all three gates, is copied verbatim into
// PresignRepo.LookupBucket and MultipartRepo.LookupBucket.
//
// The uncovered copies are the ones with the least recourse. A presigned PUT
// hands the client a URL it uses against the backend directly, so a drift in
// that copy is not caught later by the API — there is no later. This drives
// all three through the same fixture and the same three states, so a copy
// that stops refusing fails here.
func TestResolverGates_AllThreeRepos(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()
	q := sqlc.New(h.PoolMigrate)

	resolvers := []struct {
		name   string
		lookup func(context.Context, uuid.UUID, string, bool) (string, string, error)
	}{
		{"object", adapters.NewObjectRepo(q, h.PoolMigrate).LookupBucket},
		{"presign", adapters.NewPresignRepo(q, h.PoolMigrate).LookupBucket},
		{"multipart", adapters.NewMultipartRepo(q, h.PoolMigrate).LookupBucket},
	}

	tenantID := mustCreateTenant(t, h.PoolMigrate, "three-tenant")
	seedBackend(t, h.PoolMigrate, "three-be")
	mustSeedBucketAndKey(t, h.PoolMigrate, tenantID, "three-be", "three-bucket", "docs")
	be := adapters.NewBackendRepoV2(q, h.PoolMigrate)

	setFlag := func(t *testing.T, set func(rv int64) error) {
		t.Helper()
		cur, err := be.Get(ctx, "three-be")
		if err != nil {
			t.Fatalf("get backend: %v", err)
		}
		if err := set(cur.ResourceVersion); err != nil {
			t.Fatalf("set backend flag: %v", err)
		}
	}
	setProvisionState := func(t *testing.T, state string) {
		t.Helper()
		if _, err := h.PoolMigrate.Exec(ctx,
			`UPDATE buckets SET provision_state = $1 WHERE name = 'three-bucket'`, state); err != nil {
			t.Fatalf("set provision_state=%s: %v", state, err)
		}
	}

	// Baseline: an enabled, writable, ready bucket resolves for every repo,
	// so a later refusal is the gate firing and not a broken fixture.
	for _, r := range resolvers {
		for _, write := range []bool{false, true} {
			if _, _, err := r.lookup(ctx, tenantID, "docs", write); err != nil {
				t.Fatalf("%s baseline (write=%v): %v", r.name, write, err)
			}
		}
	}

	t.Run("disabled refuses both classes", func(t *testing.T) {
		setFlag(t, func(rv int64) error { return be.SetEnabled(ctx, "three-be", false, rv) })
		defer setFlag(t, func(rv int64) error { return be.SetEnabled(ctx, "three-be", true, rv) })

		for _, r := range resolvers {
			for _, write := range []bool{false, true} {
				_, _, err := r.lookup(ctx, tenantID, "docs", write)
				if !errors.Is(err, objecth.ErrBackendDisabled) {
					t.Errorf("%s disabled (write=%v): err=%v, want ErrBackendDisabled", r.name, write, err)
				}
			}
		}
	})

	t.Run("read-only refuses writes only", func(t *testing.T) {
		setFlag(t, func(rv int64) error { return be.SetReadOnly(ctx, "three-be", true, rv) })
		defer setFlag(t, func(rv int64) error { return be.SetReadOnly(ctx, "three-be", false, rv) })

		for _, r := range resolvers {
			if _, _, err := r.lookup(ctx, tenantID, "docs", true); !errors.Is(err, objecth.ErrBackendReadOnly) {
				t.Errorf("%s drained (write): err=%v, want ErrBackendReadOnly", r.name, err)
			}
			// Reads keep resolving — a drain that stopped serving reads
			// would be an outage, not a drain.
			if _, _, err := r.lookup(ctx, tenantID, "docs", false); err != nil {
				t.Errorf("%s drained (read): err=%v, want success", r.name, err)
			}
		}
	})

	t.Run("pending provisioning refuses writes only", func(t *testing.T) {
		setProvisionState(t, "pending")
		defer setProvisionState(t, "ready")

		for _, r := range resolvers {
			if _, _, err := r.lookup(ctx, tenantID, "docs", true); !errors.Is(err, objecth.ErrBucketProvisioning) {
				t.Errorf("%s pending (write): err=%v, want ErrBucketProvisioning", r.name, err)
			}
			if _, _, err := r.lookup(ctx, tenantID, "docs", false); err != nil {
				t.Errorf("%s pending (read): err=%v, want success", r.name, err)
			}
		}
	})
}
