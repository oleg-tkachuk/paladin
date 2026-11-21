//go:build integration

package components

import (
	"errors"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// A path with no live object is objecth.ErrObjectNotFound, as FindByName
// answers an id; FindByPath used to hand back the driver's pgx.ErrNoRows,
// which LookupObject could not tell from an outage and so answered every
// error as not found.
func TestFindByPath_MissingIsNotFound(t *testing.T) {
	t.Parallel()
	ctx, pool, f := setupDistinctTags(t)
	repo := adapters.NewObjectRepo(sqlc.New(pool), pool)

	if _, err := repo.FindByPath(ctx, f.tenantID, f.collection, "no-such-key"); !errors.Is(err, objecth.ErrObjectNotFound) {
		t.Errorf("err = %v, want ErrObjectNotFound", err)
	}
}
