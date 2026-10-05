//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/migrations"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

// trailEntry is an audit row a platform admin left, or one tenant's own
// principal when actor is that tenant.
func trailEntry(actor uuid.UUID, resource string, at time.Time) admindomain.AuditEntry {
	return admindomain.AuditEntry{
		EntryID:       uuid.Must(uuid.NewV7()),
		At:            at,
		ActorSubject:  "trail@test",
		ActorTenantID: actor,
		ActorAudience: "paladin-admin",
		Action:        "/paladin.admin.v1.Test/Act",
		ResourceName:  resource,
	}
}

// A tenant's trail is what its principals did and what was done to it — a
// platform admin's work inside it included, under every shape a resource name
// takes. It is selected in the query, so a page is filled with the trail's
// rows however many other tenants' rows are newer.
func TestAuditTenantTrail(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()
	repo := adapters.NewAuditRepoV2(sqlc.New(h.PoolMigrate), h.PoolMigrate)

	platform := mustCreateTenant(t, h.PoolMigrate, "trail-platform")
	tenant := mustCreateTenant(t, h.PoolMigrate, "trail-tenant")
	decoy := mustCreateTenant(t, h.PoolMigrate, "trail-decoy")
	name := "tenants/" + tenant.String()

	t0 := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	trail := []admindomain.AuditEntry{
		trailEntry(tenant, "", t0),
		trailEntry(platform, name, t0.Add(1*time.Second)),
		trailEntry(platform, name+"/quota", t0.Add(2*time.Second)),
		trailEntry(platform, "storageBackends/primary/buckets/b/"+name+"/collections/c", t0.Add(3*time.Second)),
	}
	// Newer than every trail row, so a filter applied after the fetch would
	// see only these on the first page.
	others := []admindomain.AuditEntry{
		trailEntry(platform, "tenants/"+decoy.String(), t0.Add(10*time.Second)),
		trailEntry(platform, "", t0.Add(11*time.Second)),
		trailEntry(decoy, "", t0.Add(12*time.Second)),
	}
	for _, e := range append(append([]admindomain.AuditEntry{}, trail...), others...) {
		if err := repo.Insert(ctx, e); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	pageSize := int32(len(trail) / 2)
	var got []uuid.UUID
	args := admindomain.ListAuditArgs{TrailTenantID: tenant, PageSize: pageSize}
	for {
		page, next, err := repo.List(ctx, args)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if next != "" && len(page) != int(pageSize) {
			t.Fatalf("a page of %d with more to come, want a full page of %d", len(page), pageSize)
		}
		for _, e := range page {
			got = append(got, e.EntryID)
		}
		if next == "" {
			break
		}
		at, id, _ := strings.Cut(next, "/")
		args.AfterAt, _ = time.Parse(time.RFC3339Nano, at)
		args.AfterID = uuid.MustParse(id)
	}

	want := map[uuid.UUID]bool{}
	for _, e := range trail {
		want[e.EntryID] = true
	}
	if len(got) != len(want) {
		t.Fatalf("trail has %d entries, want %d", len(got), len(want))
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("entry %s is not in the tenant's trail", id)
		}
	}

	nested, err := repo.Get(ctx, trail[3].EntryID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if nested.ResourceTenantID != tenant {
		t.Errorf("a collection's entry is filed under %s, want %s", nested.ResourceTenantID, tenant)
	}
}

// Rows written before the column existed are filed by the backfill migration,
// by the rule the insert applies: a UUID after the first `tenants` segment,
// nothing for a slug.
func TestAuditResourceTenantBackfill(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()
	repo := adapters.NewAuditRepoV2(sqlc.New(h.PoolMigrate), h.PoolMigrate)

	tenant := mustCreateTenant(t, h.PoolMigrate, "backfill-tenant")
	at := time.Now().UTC().Truncate(time.Microsecond)
	nested := trailEntry(uuid.Nil, "storageBackends/primary/buckets/b/tenants/"+tenant.String()+"/collections/c", at)
	slug := trailEntry(uuid.Nil, "tenants/platform", at)
	for _, e := range []admindomain.AuditEntry{nested, slug} {
		if err := repo.Insert(ctx, e); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	// As the rows stood before 040.
	if _, err := h.PoolMigrate.Exec(ctx, `UPDATE audit_log SET resource_tenant_id = NULL`); err != nil {
		t.Fatalf("clear: %v", err)
	}

	raw, err := migrations.FS.ReadFile("041_audit_resource_tenant_backfill.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	up, _, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("the backfill migration has no Down marker")
	}
	if _, err := h.PoolMigrate.Exec(ctx, up); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	for _, c := range []struct {
		entry admindomain.AuditEntry
		want  uuid.UUID
	}{{nested, tenant}, {slug, uuid.Nil}} {
		got, err := repo.Get(ctx, c.entry.EntryID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.ResourceTenantID != c.want {
			t.Errorf("%q backfilled to %s, want %s", c.entry.ResourceName, got.ResourceTenantID, c.want)
		}
	}
}
