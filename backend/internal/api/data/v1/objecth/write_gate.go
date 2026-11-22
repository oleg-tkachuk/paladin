package objecth

import (
	"context"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
)

// writeBinding resolves collection's (backend, bucket) for a write RPC,
// through the bucket gate: the same backend and bucket state answers every
// write the same way (bucketgate's table). Unlike scopeBucket it fails closed,
// so a write is never authorized against an empty binding: a gate refusal or a
// missing collection keeps its own code, and a store failure answers Internal,
// which the RPC's outcome line logs at Error.
//
// Every write RPC calls it before its Cedar check, so the binding it returns is
// the one the check scopes.
func writeBinding(ctx context.Context, repo Repository, tenantID uuid.UUID, collection string) (backendID, bucket string, err error) {
	backendID, bucket, err = repo.LookupBucket(ctx, tenantID, collection, bucketgate.BytesWrite)
	if err != nil {
		return "", "", MapResolveErr(err)
	}
	return backendID, bucket, nil
}
