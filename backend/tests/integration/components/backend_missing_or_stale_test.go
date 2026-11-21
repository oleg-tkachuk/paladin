//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// A guarded backend write that touches no row has two causes: the backend is
// not registered (NOT_FOUND, stop), or it is at another resource_version
// (ABORTED, re-read and retry). Update and Delete answered a version mismatch
// for both, so editing or deleting a backend that does not exist told the
// caller to retry what can never succeed.
func TestGuardedBackendWritesTellMissingFromStale(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewBackendRepoV2(sqlc.New(pool), pool)

	backendID := "be-" + uuid.New().String()[:8]
	mustExec(t, ctx, pool,
		`INSERT INTO storage_backends (name, kind, provider, endpoint, region)
		 VALUES ($1, 's3-compatible', 'garage', 'http://x.invalid:3900', 'us-east-1')`,
		backendID)
	current, err := repo.Get(ctx, backendID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	stale := current.ResourceVersion + 1

	writes := map[string]func(name string, version int64) error{
		"update": func(name string, v int64) error {
			return repo.Update(ctx, admindomain.StorageBackend{BackendID: name, DisplayName: "x"},
				v, []string{admindomain.StorageBackendPathDisplayName})
		},
		"set enabled":     func(name string, v int64) error { return repo.SetEnabled(ctx, name, true, v) },
		"set read-only":   func(name string, v int64) error { return repo.SetReadOnly(ctx, name, false, v) },
		"set maintenance": func(name string, v int64) error { return repo.SetMaintenance(ctx, name, false, v) },
		"delete":          func(name string, v int64) error { return repo.Delete(ctx, name, v) },
	}
	// Delete last: it removes the row the others need.
	for _, what := range []string{"update", "set enabled", "set read-only", "set maintenance", "delete"} {
		write := writes[what]
		t.Run(what, func(t *testing.T) {
			for _, version := range []int64{skipVersionCheck, stale} {
				if err := write("no-such-backend", version); !errors.Is(err, admindomain.ErrNotFound) {
					t.Errorf("missing backend at version %d = %v, want ErrNotFound", version, err)
				}
			}
			if err := write(backendID, stale); !errors.Is(err, admindomain.ErrVersionMismatch) {
				t.Errorf("stale version = %v, want ErrVersionMismatch", err)
			}
		})
	}
}

// Update applies exactly the fields its mask names and leaves the rest.
func TestBackendUpdateAppliesOnlyMaskedFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewBackendRepoV2(sqlc.New(pool), pool)

	backendID := "be-" + uuid.New().String()[:8]
	const region = "us-east-1"
	mustExec(t, ctx, pool,
		`INSERT INTO storage_backends (name, kind, provider, endpoint, region)
		 VALUES ($1, 's3-compatible', 'garage', 'http://x.invalid:3900', $2)`,
		backendID, region)

	const displayName = "renamed"
	if err := repo.Update(ctx, admindomain.StorageBackend{
		BackendID: backendID, DisplayName: displayName, Region: "eu-west-3",
	}, skipVersionCheck, []string{admindomain.StorageBackendPathDisplayName}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := repo.Get(ctx, backendID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.DisplayName != displayName {
		t.Errorf("display name = %q, want %q", got.DisplayName, displayName)
	}
	if got.Region != region {
		t.Errorf("region = %q, want %q (not in the mask)", got.Region, region)
	}
}
