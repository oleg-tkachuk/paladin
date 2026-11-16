//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5"

	tenanth "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// A slug names its tenant in Cedar, in policies, in tokens and in every
// tenants/<slug> a client holds. While slugs are reserved, one a trashed
// tenant still holds or a purged tenant held is refused to a new tenant;
// with reuse allowed, the old behaviour stands. The purge records the slug
// whatever the setting, so turning reservation on later still covers it.
func TestTenantSlugReservation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewTenantRepo(sqlc.New(pool), pool)

	create := func(slug string, allowReuse bool) (uuid.UUID, error) {
		id := uuid.New()
		err := repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			return repo.CreateTx(ctx, tx, tenanth.CreateTenantArgs{
				TenantID: id, Slug: slug, DisplayName: slug + "-" + id.String()[:8],
				InheritedCedarPolicy: "", AllowSlugReuse: allowReuse,
			})
		})
		return id, err
	}
	trash := func(id uuid.UUID) {
		t.Helper()
		if err := repo.SoftDelete(ctx, id, 0); err != nil {
			t.Fatalf("trash %s: %v", id, err)
		}
	}
	purge := func(id uuid.UUID) {
		t.Helper()
		if err := repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			return repo.HardDeleteTx(ctx, tx, id, 0)
		}); err != nil {
			t.Fatalf("purge %s: %v", id, err)
		}
	}

	t.Run("a live tenant's slug is taken, as before", func(t *testing.T) {
		if _, err := create("live-held", false); err != nil {
			t.Fatal(err)
		}
		if _, err := create("live-held", true); !errors.Is(err, tenanth.ErrSlugConflict) {
			t.Fatalf("err = %v, want ErrSlugConflict", err)
		}
	})

	t.Run("a trashed tenant's slug is reserved", func(t *testing.T) {
		id, err := create("in-trash", false)
		if err != nil {
			t.Fatal(err)
		}
		trash(id)
		if _, err := create("in-trash", false); !errors.Is(err, tenanth.ErrSlugHeldInTrash) {
			t.Fatalf("err = %v, want ErrSlugHeldInTrash", err)
		}
	})

	t.Run("with reuse allowed, a trashed tenant's slug is free", func(t *testing.T) {
		id, err := create("in-trash-reuse", false)
		if err != nil {
			t.Fatal(err)
		}
		trash(id)
		if _, err := create("in-trash-reuse", true); err != nil {
			t.Fatalf("reuse allowed: %v", err)
		}
	})

	t.Run("a purged tenant's slug is retired and reserved", func(t *testing.T) {
		id, err := create("purged", false)
		if err != nil {
			t.Fatal(err)
		}
		trash(id)
		purge(id)
		var former uuid.UUID
		if err := pool.QueryRow(ctx, `SELECT former_tenant_id FROM retired_tenant_slugs WHERE slug = $1`, "purged").Scan(&former); err != nil || former != id {
			t.Fatalf("retired row = %s, %v; want %s", former, err, id)
		}
		if _, err := create("purged", false); !errors.Is(err, tenanth.ErrSlugRetired) {
			t.Fatalf("err = %v, want ErrSlugRetired", err)
		}
	})

	t.Run("with reuse allowed, a purged tenant's slug is free", func(t *testing.T) {
		id, err := create("purged-reuse", false)
		if err != nil {
			t.Fatal(err)
		}
		trash(id)
		purge(id)
		next, err := create("purged-reuse", true)
		if err != nil {
			t.Fatalf("reuse allowed: %v", err)
		}
		// The second tenant's purge leaves the first record as it was.
		trash(next)
		purge(next)
		var former uuid.UUID
		if err := pool.QueryRow(ctx, `SELECT former_tenant_id FROM retired_tenant_slugs WHERE slug = $1`, "purged-reuse").Scan(&former); err != nil || former != id {
			t.Fatalf("retired row = %s, %v; want the first tenant %s", former, err, id)
		}
	})

	t.Run("a refused purge retires nothing", func(t *testing.T) {
		id, err := create("kept", false)
		if err != nil {
			t.Fatal(err)
		}
		const staleVersion = 999
		_ = repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			return repo.HardDeleteTx(ctx, tx, id, staleVersion)
		})
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM retired_tenant_slugs WHERE slug = $1`, "kept").Scan(&n); err != nil || n != 0 {
			t.Fatalf("%d retired rows for a tenant still there, %v", n, err)
		}
	})

	t.Run("the slug cannot be updated, whatever the session says", func(t *testing.T) {
		id, err := create("fixed", false)
		if err != nil {
			t.Fatal(err)
		}
		_, err = pool.Exec(ctx, `SELECT set_config('paladin.allow_slug_rename', 'on', false)`)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE tenants SET slug = 'fixed-2' WHERE id = $1`, id); err == nil {
			t.Fatal("the slug changed")
		}
	})
}
