//go:build integration

package components

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/tenantstate"
)

// A trashed tenant's slug may be taken by a live tenant. The slug then names
// the live one — for the freeze and for the handlers alike — and the trashed
// one only while no live tenant holds it.
func TestASlugNamesTheLiveTenant(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	app := rlsPool(t, ctx, admin)
	reader := tenantstate.NewReader(app)
	repo := adapters.NewTenantRepo(sqlc.New(admin), admin)

	old, slug := mkTenant(t, ctx, admin, "shared")
	mustExec(t, ctx, admin, `UPDATE tenants SET deleted_at = now() WHERE id = $1`, old)
	if id, found, err := reader.TenantIDBySlug(ctx, slug); err != nil || !found || id != old {
		t.Fatalf("only the trashed tenant holds it: got %s %v %v, want %s", id, found, err, old)
	}

	live := uuid.New()
	mustExec(t, ctx, admin,
		`INSERT INTO tenants (id, slug, display_name, storage_layout) VALUES ($1, $2, $3, 'shared')`,
		live, slug, "live holder of "+slug)
	if id, found, err := reader.TenantIDBySlug(ctx, slug); err != nil || !found || id != live {
		t.Errorf("freeze: got %s %v %v, want the live tenant %s", id, found, err, live)
	}
	if got, err := repo.GetBySlug(ctx, slug); err != nil || got.TenantID != live {
		t.Errorf("handlers: got %v %v, want the live tenant %s", got.TenantID, err, live)
	}

	if _, found, err := reader.TenantIDBySlug(ctx, "no-such-slug"); err != nil || found {
		t.Errorf("an unknown slug: found=%v err=%v", found, err)
	}
}

// A tenant in the trash takes no update where the row is written, whatever
// reached the repository.
func TestUpdateOfATrashedTenantIsRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	repo := adapters.NewTenantRepo(sqlc.New(admin), admin)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	mustExec(t, ctx, admin, `UPDATE tenants SET deleted_at = now() WHERE id = $1`, tenant)
	// The version it has now, so only the trash can refuse the update.
	var version int64
	if err := admin.QueryRow(ctx, `SELECT resource_version FROM tenants WHERE id = $1`, tenant).Scan(&version); err != nil {
		t.Fatal(err)
	}
	name := "renamed"
	_, err := repo.Update(ctx, tenanth.UpdateTenantArgs{
		TenantID: tenant, DisplayName: &name, ExpectedVersion: version,
	})
	if !errors.Is(err, tenanth.ErrAlreadyDeleted) {
		t.Errorf("err = %v, want ErrAlreadyDeleted", err)
	}
}
