package operations

import (
	"context"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
)

// findByIDs is the shared read phase for batch executors: one
// FindByIDs query for the whole id list, indexed by id. Ids absent
// from the map were not found — callers report those per-id, matching
// the old one-FindByName-per-id contract without its N round-trips.
func findByIDs(ctx context.Context, repo objecth.Repository, tenantID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]objecth.Object, error) {
	objs, err := repo.FindByIDs(ctx, tenantID, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[uuid.UUID]objecth.Object, len(objs))
	for _, o := range objs {
		byID[o.ObjectID] = o
	}
	return byID, nil
}
