//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	tenantapi "github.com/oleg-tkachuk/paladin/internal/api/v1/tenant"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// TestRenameRecordsSlugHistory proves migration 039 + the transactional
// capture in TenantRepo.Rename: a real slug rotation writes exactly one
// tenant_slug_history row mapping old→new (in the same tx as the slug bump),
// while the idempotent same-slug rename writes none. This is the durable
// source a future ResolveRenamedSlug resolver reads to back a 404 redirect.
func TestRenameRecordsSlugHistory(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)

	id := uuid.New()
	mustExec(t, ctx, pool,
		`INSERT INTO tenants (tenant_id, slug, display_name) VALUES ($1, $2, $3)`,
		id, "acme", "Acme")

	repo := adapters.NewTenantRepo(sqlc.New(pool), pool)

	rvOf := func() int64 {
		t.Helper()
		var rv int64
		if err := pool.QueryRow(ctx,
			`SELECT resource_version FROM tenants WHERE tenant_id = $1`, id,
		).Scan(&rv); err != nil {
			t.Fatalf("read resource_version: %v", err)
		}
		return rv
	}
	countHistory := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM tenant_slug_history WHERE tenant_id = $1`, id,
		).Scan(&n); err != nil {
			t.Fatalf("count history: %v", err)
		}
		return n
	}

	if _, err := repo.Rename(ctx, tenantapi.RenameTenantSlugArgs{
		TenantID: id, NewSlug: "acme-corp", ExpectedVersion: rvOf(),
	}); err != nil {
		t.Fatalf("rename: %v", err)
	}

	if got := countHistory(); got != 1 {
		t.Fatalf("history rows after rename = %d, want 1", got)
	}
	var old, neu string
	if err := pool.QueryRow(ctx,
		`SELECT old_slug, new_slug FROM tenant_slug_history WHERE tenant_id = $1`, id,
	).Scan(&old, &neu); err != nil {
		t.Fatalf("read history: %v", err)
	}
	if old != "acme" || neu != "acme-corp" {
		t.Fatalf("history = (%q→%q), want (acme→acme-corp)", old, neu)
	}

	// Idempotent same-slug rename is a no-op: no version bump, no new row.
	if _, err := repo.Rename(ctx, tenantapi.RenameTenantSlugArgs{
		TenantID: id, NewSlug: "acme-corp", ExpectedVersion: rvOf(),
	}); err != nil {
		t.Fatalf("idempotent rename: %v", err)
	}
	if got := countHistory(); got != 1 {
		t.Fatalf("history rows after no-op rename = %d, want 1", got)
	}

	// LookupRenamedSlug (backs ResolveRenamedSlug): "acme" resolves to the
	// new slug within a generous window; an unknown slug and a zero-length
	// window both miss.
	res, found, err := repo.LookupRenamedSlug(ctx, "acme", time.Hour)
	if err != nil {
		t.Fatalf("LookupRenamedSlug: %v", err)
	}
	if !found || res.NewSlug != "acme-corp" || res.TenantID != id {
		t.Fatalf("lookup acme = (%+v, found=%v), want acme-corp / %s", res, found, id)
	}
	if _, found, _ := repo.LookupRenamedSlug(ctx, "never-existed", time.Hour); found {
		t.Errorf("lookup of unknown slug returned found=true")
	}
	if _, found, _ := repo.LookupRenamedSlug(ctx, "acme", time.Nanosecond); found {
		t.Errorf("lookup with sub-window age returned found=true (grace window not enforced)")
	}
}
