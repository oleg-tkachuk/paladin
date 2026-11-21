//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// An update that touches no tenant row has three causes, and each asks the
// caller for something different: the tenant does not exist (NOT_FOUND), it
// is in the trash (FAILED_PRECONDITION), or it is at another resource_version
// (ABORTED, re-read and retry). A missing tenant used to read as the last.
func TestUpdateTenant_MissingTrashedOrStale(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewTenantRepo(sqlc.New(pool), pool)

	live, _ := mkTenant(t, ctx, pool, "shared")
	trashed, _ := mkTenant(t, ctx, pool, "shared")
	mustExec(t, ctx, pool, `UPDATE tenants SET deleted_at = now() WHERE id = $1`, trashed)
	current, err := repo.Get(ctx, live)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	displayName := "renamed-" + uuid.New().String()[:8]

	for _, tc := range []struct {
		name    string
		id      uuid.UUID
		version int64
		want    error
	}{
		{"no such tenant", uuid.New(), current.ResourceVersion, tenanth.ErrNotFound},
		{"in the trash", trashed, current.ResourceVersion, tenanth.ErrAlreadyDeleted},
		{"stale version", live, current.ResourceVersion + 1, tenanth.ErrVersionMismatch},
	} {
		_, err := repo.Update(ctx, tenanth.UpdateTenantArgs{
			TenantID: tc.id, ExpectedVersion: tc.version, DisplayName: &displayName,
		})
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
}

// An update that does land returns the row it wrote, and one that takes
// another tenant's display name is refused as that, not as a stale version.
func TestUpdateTenant_AppliesOrNamesTheCollision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewTenantRepo(sqlc.New(pool), pool)

	id, _ := mkTenant(t, ctx, pool, "shared")
	other, _ := mkTenant(t, ctx, pool, "shared")
	current, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	taken, err := repo.Get(ctx, other)
	if err != nil {
		t.Fatalf("get other: %v", err)
	}
	if _, err := repo.Update(ctx, tenanth.UpdateTenantArgs{
		TenantID: id, ExpectedVersion: current.ResourceVersion, DisplayName: &taken.DisplayName,
	}); !errors.Is(err, tenanth.ErrDisplayNameConflict) {
		t.Errorf("taken display name: err = %v, want ErrDisplayNameConflict", err)
	}

	const policy = "permit(principal, action, resource);"
	policyText := policy
	got, err := repo.Update(ctx, tenanth.UpdateTenantArgs{
		TenantID: id, ExpectedVersion: current.ResourceVersion, InheritedCedarPolicy: &policyText,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got.InheritedCedarPolicy != policy || got.ResourceVersion == current.ResourceVersion {
		t.Errorf("updated = (%q, v%d), want (%q, a new version than v%d)",
			got.InheritedCedarPolicy, got.ResourceVersion, policy, current.ResourceVersion)
	}
}
