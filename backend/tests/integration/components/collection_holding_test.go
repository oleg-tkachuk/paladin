//go:build integration

package components

import (
	"context"
	"errors"
	"testing"

	objectkey "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/collectionh"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/tests/objectrow"
)

// A trashed object holds its collection as a live one does: the delete is
// refused, and Holding counts each kind — the live, pending included, and the
// trashed — so the refusal can say which.
func TestCollectionHolding_CountsTheTrashToo(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	repo := adapters.NewCollectionRepo(sqlc.New(pool), pool)

	seed := func(path string, state sqlc.ObjectState) {
		seedObject(t, ctx, pool, objectrow.Row{
			TenantID: f.tenantID, CollectionID: f.collectionID, Path: path,
			State: string(state), SizeBytes: objectrow.Ptr[int64](100),
		})
	}
	seed("trashed", sqlc.ObjectStateDELETED)
	got, err := repo.Holding(ctx, f.tenantID, f.collection)
	if err != nil {
		t.Fatal(err)
	}
	if got != (objectkey.CollectionHolding{Trashed: 1}) {
		t.Errorf("trash only: holding = %+v, want 1 trashed", got)
	}
	current, err := repo.Get(ctx, f.tenantID, f.collection)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(ctx, f.tenantID, f.collection, current.ResourceVersion); !errors.Is(err, objectkey.ErrCollectionHasObjects) {
		t.Errorf("delete with only a trashed object: %v, want ErrCollectionHasObjects", err)
	}

	seed("live-1", sqlc.ObjectStateAVAILABLE)
	seed("live-2", sqlc.ObjectStateAVAILABLE)
	seed("uploading", sqlc.ObjectStatePENDING)
	got, err = repo.Holding(ctx, f.tenantID, f.collection)
	if err != nil {
		t.Fatal(err)
	}
	if got != (objectkey.CollectionHolding{Live: 3, Trashed: 1}) {
		t.Errorf("mixed: holding = %+v, want 3 live and 1 trashed", got)
	}
}
