//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"uuid"

	objectkey "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/collectionh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// A guarded collection write that touches no row has two causes: the tenant
// has no such collection (NOT_FOUND, stop), or it is there at another
// resource_version (ABORTED, re-read and retry). Update, Delete and Rebind
// answered a version mismatch for both. Another tenant's collection is absent
// to the caller.
func TestGuardedCollectionWritesTellMissingFromStale(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	otherTenant, _ := mkTenant(t, ctx, pool, "shared")
	repo := adapters.NewCollectionRepo(sqlc.New(pool), pool)

	current, err := repo.Get(ctx, f.tenantID, f.collection)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	stale := current.ResourceVersion + 1
	displayName := "renamed"

	writes := map[string]func(tc collectionAt, version int64) error{
		"update": func(at collectionAt, v int64) error {
			_, err := repo.Update(ctx, objectkey.UpdateCollectionArgs{
				TenantID: at.tenant, Collection: at.name, ExpectedVersion: v, DisplayName: &displayName,
			})
			return err
		},
		"rebind": func(at collectionAt, v int64) error {
			return repo.Rebind(ctx, at.tenant, at.name, current.BackendID, current.BucketName, v)
		},
		"delete": func(at collectionAt, v int64) error { return repo.Delete(ctx, at.tenant, at.name, v) },
	}
	absent := map[string]collectionAt{
		"no such collection": {f.tenantID, "no-such-collection"},
		"another tenant's":   {otherTenant, f.collection},
	}
	// Delete last: it removes the row the others need.
	for _, what := range []string{"update", "rebind", "delete"} {
		write := writes[what]
		t.Run(what, func(t *testing.T) {
			for why, at := range absent {
				for _, version := range []int64{skipVersionCheck, stale} {
					if err := write(at, version); !errors.Is(err, apiutil.ErrNotFound) {
						t.Errorf("%s at version %d = %v, want ErrNotFound", why, version, err)
					}
				}
			}
			if err := write(collectionAt{f.tenantID, f.collection}, stale); !errors.Is(err, objectkey.ErrVersionMismatch) {
				t.Errorf("stale version = %v, want ErrVersionMismatch", err)
			}
		})
	}
}

// collectionAt names a collection the way the repository's writes take it.
type collectionAt struct {
	tenant uuid.UUID
	name   string
}
