//go:build integration

package components

import (
	"context"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5"

	tenanth "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// The stats page labels the tenants the worker's census names by id: live
// and trashed ones alike, since a trashed tenant's objects still take space.
// An id with no tenant, or none that parses, is simply not labelled.
func TestSystemInventoryLabelsTenants(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewTenantRepo(sqlc.New(pool), pool)
	create := func(slug string) uuid.UUID {
		t.Helper()
		id := uuid.New()
		if err := repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			return repo.CreateTx(ctx, tx, tenanth.CreateTenantArgs{TenantID: id, Slug: slug, DisplayName: slug + " name"})
		}); err != nil {
			t.Fatalf("create %s: %v", slug, err)
		}
		return id
	}
	live, trashed := create("inv-live"), create("inv-trashed")
	if err := repo.SoftDelete(ctx, trashed, 0); err != nil {
		t.Fatal(err)
	}
	gone := uuid.New()

	names, err := adapters.NewSystemInventory(pool).TenantNames(ctx,
		[]string{live.String(), trashed.String(), gone.String(), "not-a-uuid"})
	if err != nil {
		t.Fatalf("TenantNames: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("labelled %d tenants, want 2: %v", len(names), names)
	}
	if n := names[live.String()]; n.Slug != "inv-live" || n.DisplayName != "inv-live name" {
		t.Errorf("live = %+v", n)
	}
	if names[trashed.String()].Slug != "inv-trashed" {
		t.Errorf("trashed tenant not labelled: %v", names)
	}
}
