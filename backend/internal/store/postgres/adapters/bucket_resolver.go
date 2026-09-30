package adapters

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
)

// This file holds the collection→bucket resolution that every data-plane op
// funnels through, and the three gates it applies.
//
// It exists because the resolution used to be copied, byte for byte, into
// ObjectRepo, PresignRepo and MultipartRepo, with a fourth copy of the gates
// inlined in ObjectRepo.LookupBucketMeta. The copies agreed, but only the
// ObjectRepo one was covered: deleting the read-only gate from the presign
// copy and the provisioning gate from the multipart copy left the whole
// integration suite green. Duplicated policy diverges eventually, and the
// presign copy is the one with no recourse — a presigned PUT is used against
// the backend directly, so a gate that stops refusing there has nothing
// downstream to catch it.

// resolveBucketQuery reads the physical bucket bound to a collection via
// idx_collections_bucket_routing, joining storage_backends so a disabled
// backend is refused at this single chokepoint (feature 002) rather than one
// round trip later. bucket_name is NOT NULL after the schema baseline
// (001_initial_schema.sql), so a successful lookup always returns a non-empty
// name.
const resolveBucketQuery = `
		SELECT sb.name, bk.name, sb.enabled, sb.read_only, bk.provision_state
		FROM collections c
		JOIN buckets bk          ON bk.id = c.bucket_id
		JOIN storage_backends sb ON sb.id = bk.backend_id
		WHERE c.tenant_id = $1 AND c.name = $2`

// bucketOpAllowed applies the three gates to an already-read row. `write`
// splits them by operation class: a disabled backend refuses everything,
// while the read-only drain (migration 047) and the provisioning gate refuse
// mutations only — reads keep resolving, which is the entire point of a
// drain.
//
// Kept separate from the query so LookupBucketMeta, whose SELECT carries the
// versioning and lock columns too, applies the same policy without a second
// copy of it.
func bucketOpAllowed(write, enabled, readOnly bool, provisionState string) error {
	if !enabled {
		return objecth.ErrBackendDisabled
	}
	if write && readOnly {
		return objecth.ErrBackendReadOnly
	}
	if write && provisionState != "ready" {
		return objecth.ErrBucketProvisioning
	}
	return nil
}

// resolveBucket returns the (backend, bucket) pair bound to a collection, or
// the gate error refusing the operation.
func resolveBucket(
	ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, collection string, write bool,
) (string, string, error) {
	var (
		backendID      string
		bucket         string
		enabled        bool
		readOnly       bool
		provisionState string
	)
	if err := pool.QueryRow(ctx, resolveBucketQuery, pgUUID(tenantID), collection).
		Scan(&backendID, &bucket, &enabled, &readOnly, &provisionState); err != nil {
		if isNoRows(err) {
			return "", "", fmt.Errorf("collection %q not found", collection)
		}
		return "", "", fmt.Errorf("lookup bucket: %w", err)
	}
	if err := bucketOpAllowed(write, enabled, readOnly, provisionState); err != nil {
		return "", "", err
	}
	return backendID, bucket, nil
}
