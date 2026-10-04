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
//
// Only objects in collection count as found. Ids are unique across the
// tenant, but the batch was authorised for collection alone: an id from
// another collection is not an object of this batch, and acting on it
// would reach past that authorisation.
func findByIDs(ctx context.Context, repo objecth.Repository, tenantID uuid.UUID, collection string, ids []uuid.UUID) (map[uuid.UUID]objecth.Object, error) {
	objs, err := repo.FindByIDs(ctx, tenantID, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[uuid.UUID]objecth.Object, len(objs))
	for _, o := range objs {
		if o.Collection == collection {
			byID[o.ObjectID] = o
		}
	}
	return byID, nil
}
