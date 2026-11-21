//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"uuid"

	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// A guarded user write that touches no row has two causes: the user is gone
// (NOT_FOUND, stop) or it is there at another version (ABORTED, re-read and
// retry). Both used to come back as a version mismatch, and resetting a gone
// user's password touched nothing and reported success.
func TestUserWritesTellMissingFromStale(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewUserRepo(sqlc.New(pool))

	tenantID, _ := mkTenant(t, ctx, pool, "shared")
	u, err := repo.Create(ctx, authstore.User{TenantID: tenantID, Subject: "u-" + uuid.New().String()[:8], PasswordHash: []byte("x")})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	stale := u.ResourceVersion + 1

	writes := map[string]func(id uuid.UUID, version int64) error{
		"update": func(id uuid.UUID, v int64) error {
			_, err := repo.Update(ctx, authstore.User{UserID: id, TenantID: tenantID, DisplayName: "x"}, v)
			return err
		},
		"delete": func(id uuid.UUID, v int64) error { return repo.Delete(ctx, id, v) },
	}
	for what, write := range writes {
		t.Run(what, func(t *testing.T) {
			for _, version := range []int64{skipVersionCheck, stale} {
				if err := write(uuid.New(), version); !errors.Is(err, authstore.ErrNotFound) {
					t.Errorf("missing user at version %d = %v, want ErrNotFound", version, err)
				}
			}
			if err := write(u.UserID, stale); !errors.Is(err, authstore.ErrVersionMismatch) {
				t.Errorf("stale version = %v, want ErrVersionMismatch", err)
			}
		})
	}
	t.Run("password", func(t *testing.T) {
		if err := repo.UpdatePasswordHash(ctx, uuid.New(), []byte("y")); !errors.Is(err, authstore.ErrNotFound) {
			t.Errorf("missing user's password = %v, want ErrNotFound", err)
		}
		if err := repo.UpdatePasswordHash(ctx, u.UserID, []byte("y")); err != nil {
			t.Errorf("present user's password = %v, want nil", err)
		}
	})
	t.Run("page token", func(t *testing.T) {
		if _, _, err := repo.List(ctx, authstore.ListUsersArgs{TenantID: tenantID, PageToken: "not-a-cursor"}); !errors.Is(err, authstore.ErrInvalidPageToken) {
			t.Errorf("malformed page token = %v, want ErrInvalidPageToken", err)
		}
	})
}
