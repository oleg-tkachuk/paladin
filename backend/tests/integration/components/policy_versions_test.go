//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/policyh"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// Policy texts the tests write; any two differ.
const (
	policyV1 = `permit (principal, action == Action::"GetObject", resource);`
	policyV2 = `forbid (principal, action == Action::"DeleteObject", resource);`
)

// versionsPageSize is large enough to read a test's whole history at once.
const versionsPageSize int32 = 100

// The triggers keep every text a layer has held (migration 101): whoever
// wrote it, through whichever path, and only when the text changed.
func TestPolicyVersions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	other, _ := mkTenant(t, ctx, admin, "shared")
	seedBucketRow(t, ctx, admin)
	seedCollectionFor(t, ctx, admin, tenant, "docs")
	seedCollectionFor(t, ctx, admin, other, "docs")

	pool := rlsPool(t, ctx, admin)
	repo := adapters.NewPolicyVersionRepo(sqlc.New(pool))
	const writer = "01a00000-0000-7000-8000-00000000beef"
	asWriter := auth.WithPrincipal(ctx, &auth.Principal{Subject: writer, TenantID: tenant})
	asTenant := auth.WithPrincipal(ctx, &auth.Principal{TenantID: tenant})
	asOther := auth.WithPrincipal(ctx, &auth.Principal{TenantID: other})
	docs := policyh.LayerRef{Layer: policyh.LayerCollection, TenantID: tenant, Collection: "docs"}

	setPolicy := func(c context.Context, text string) {
		t.Helper()
		if _, err := pool.Exec(c, `UPDATE collections SET cedar_policy = $1 WHERE tenant_id = $2 AND name = 'docs'`, text, tenant); err != nil {
			t.Fatalf("set policy: %v", err)
		}
	}
	history := func(c context.Context, ref policyh.LayerRef) []policyh.Version {
		t.Helper()
		v, err := repo.ListPolicyVersions(c, ref, 0, versionsPageSize)
		if err != nil {
			t.Fatalf("ListPolicyVersions: %v", err)
		}
		return v
	}

	setPolicy(asWriter, policyV1)
	setPolicy(asWriter, policyV1) // the same text: no new version
	setPolicy(asTenant, policyV2) // no subject: not recorded
	setPolicy(asWriter, "")       // cleared: a version too

	got := history(asTenant, docs)
	want := []struct {
		version int32
		text    string
		writer  *string
	}{
		{3, "", ptr(writer)},
		{2, policyV2, nil},
		{1, policyV1, ptr(writer)},
	}
	if len(got) != len(want) {
		t.Fatalf("history = %+v, want %d versions", got, len(want))
	}
	for i, w := range want {
		g := got[i]
		if g.Version != w.version || g.CedarPolicy != w.text || !sameWriter(g.WrittenBy, w.writer) {
			t.Errorf("version %d = {%d %q %v}, want {%d %q %v}", i, g.Version, g.CedarPolicy, writerName(g.WrittenBy), w.version, w.text, writerName(w.writer))
		}
		wantTenant := w.writer != nil
		if (g.WrittenByTenant != nil) != wantTenant || (wantTenant && *g.WrittenByTenant != tenant) {
			t.Errorf("version %d writer tenant = %v, want %v", g.Version, g.WrittenByTenant, wantTenant)
		}
		if g.WrittenAt.IsZero() {
			t.Errorf("version %d has no time", g.Version)
		}
	}

	t.Run("pages below a version", func(t *testing.T) {
		const below = 3
		v, err := repo.ListPolicyVersions(asTenant, docs, below, versionsPageSize)
		if err != nil || len(v) != 2 || v[0].Version != 2 {
			t.Errorf("below %d: %+v, %v", below, v, err)
		}
	})

	t.Run("another tenant cannot find the layer", func(t *testing.T) {
		// RLS hides the collection itself, so the history is not even
		// addressable: not found, rather than an empty list that says it exists.
		v, err := repo.ListPolicyVersions(asOther, docs, 0, versionsPageSize)
		if !errors.Is(err, policyh.ErrLayerNotFound) || len(v) != 0 {
			t.Errorf("tenant %s read %d versions of %s's policy (err %v)", other, len(v), tenant, err)
		}
		var n int
		if err := pool.QueryRow(asOther, `SELECT count(*) FROM policy_versions WHERE tenant_id = $1`, tenant).Scan(&n); err != nil || n != 0 {
			t.Errorf("tenant %s saw %d of %s's version rows (err %v)", other, n, tenant, err)
		}
	})

	t.Run("a collection that does not exist", func(t *testing.T) {
		_, err := repo.ListPolicyVersions(asTenant, policyh.LayerRef{Layer: policyh.LayerCollection, TenantID: tenant, Collection: "nope"}, 0, versionsPageSize)
		if !errors.Is(err, policyh.ErrLayerNotFound) {
			t.Errorf("err = %v, want ErrLayerNotFound", err)
		}
	})

	t.Run("a tenant's policy at creation is version 1", func(t *testing.T) {
		id := uuid.New()
		mustExec(t, ctx, admin,
			`INSERT INTO tenants (id, slug, display_name, storage_layout, inherited_cedar_policy) VALUES ($1, $2, $2, 'shared', $3)`,
			id, "t-"+id.String()[:8], policyV1)
		asNew := auth.WithPrincipal(ctx, &auth.Principal{TenantID: id})
		v := history(asNew, policyh.LayerRef{Layer: policyh.LayerTenant, TenantID: id})
		if len(v) != 1 || v[0].Version != 1 || v[0].CedarPolicy != policyV1 || v[0].WrittenBy != nil {
			t.Errorf("tenant history = %+v, want version 1 with the default text and no writer", v)
		}
	})

	t.Run("an admin acting on a tenant is recorded under its own tenant", func(t *testing.T) {
		const admin = "01a00000-0000-7000-8000-0000000000ad"
		asAdmin := auth.WithActingTenant(auth.WithPrincipal(ctx, &auth.Principal{Subject: admin, TenantID: other}), tenant)
		if _, err := pool.Exec(asAdmin, `UPDATE tenants SET inherited_cedar_policy = $1 WHERE id = $2`, policyV2, tenant); err != nil {
			t.Fatalf("set tenant policy: %v", err)
		}
		v := history(asTenant, policyh.LayerRef{Layer: policyh.LayerTenant, TenantID: tenant})
		if len(v) == 0 || v[0].CedarPolicy != policyV2 || !sameWriter(v[0].WrittenBy, ptr(admin)) ||
			v[0].WrittenByTenant == nil || *v[0].WrittenByTenant != other {
			t.Errorf("newest tenant version = %+v, want the admin under its own tenant %s", v, other)
		}
	})

	t.Run("a bucket's policy, a platform layer", func(t *testing.T) {
		var backend uuid.UUID
		if err := admin.QueryRow(ctx, `SELECT backend_id FROM buckets WHERE name = $1`, actingBucketName).Scan(&backend); err != nil {
			t.Fatalf("bucket: %v", err)
		}
		mustExec(t, ctx, admin, `UPDATE buckets SET cedar_policy = $1 WHERE name = $2`, policyV2, actingBucketName)
		v := history(asOther, policyh.LayerRef{Layer: policyh.LayerBucket, BackendID: backend, Bucket: actingBucketName})
		if len(v) == 0 || v[0].CedarPolicy != policyV2 {
			t.Errorf("bucket history = %+v, want the new text first", v)
		}
	})

	t.Run("a deleted collection takes its history with it", func(t *testing.T) {
		var id uuid.UUID
		if err := admin.QueryRow(ctx, `SELECT id FROM collections WHERE tenant_id = $1 AND name = 'docs'`, tenant).Scan(&id); err != nil {
			t.Fatalf("collection: %v", err)
		}
		mustExec(t, ctx, admin, `DELETE FROM collections WHERE id = $1`, id)
		var n int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM policy_versions WHERE owner_id = $1`, id).Scan(&n); err != nil || n != 0 {
			t.Errorf("%d versions outlived their collection (err %v)", n, err)
		}
	})
}

// The application cannot write history itself: only the triggers do.
func TestPolicyVersions_RuntimeRoleCannotWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	pool := rlsPool(t, ctx, admin)
	asTenant := auth.WithPrincipal(ctx, &auth.Principal{TenantID: tenant})
	if _, err := pool.Exec(asTenant,
		`INSERT INTO policy_versions (layer, owner_id, tenant_id, version, cedar_policy) VALUES ('tenant', $1, $1, 99, 'x')`, tenant); err == nil {
		t.Error("the runtime role wrote a policy version directly")
	}
}

func sameWriter(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// writerName prints a writer for a failure message.
func writerName(s *string) string {
	if s == nil {
		return "<not recorded>"
	}
	return *s
}
