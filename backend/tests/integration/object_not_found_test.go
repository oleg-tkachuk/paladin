//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

// The store answers a missing object with objecth.ErrObjectNotFound, the one
// error the handlers turn into NotFound. It answered with the driver's
// pgx.ErrNoRows, which the handlers passed to the caller as text.
func TestObjectFindByNameNotFound(t *testing.T) {
	h := pgharness.Setup(t)
	repo := adapters.NewObjectRepo(sqlc.New(h.PoolMigrate), h.PoolMigrate)
	tenant := mustCreateTenant(t, h.PoolMigrate, "find-by-name")
	mustCreateCollection(t, h.PoolMigrate, tenant, "docs")
	present := mustInsertAvailableObject(t, h.PoolMigrate, tenant, "docs", "here.txt")

	for name, id := range map[string]string{
		"no such object":           uuid.NewString(),
		"an id that is not a UUID": "not-a-uuid",
	} {
		if _, err := repo.FindByName(context.Background(), tenant, "docs", id); !errors.Is(err, objecth.ErrObjectNotFound) {
			t.Errorf("%s: err = %v, want ErrObjectNotFound", name, err)
		}
	}
	if _, err := repo.FindByName(context.Background(), tenant, "docs", present.String()); err != nil {
		t.Errorf("an object that exists: %v", err)
	}
}
