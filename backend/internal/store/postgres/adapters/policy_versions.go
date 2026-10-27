package adapters

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/policyh"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// PolicyVersionRepo reads policy history (migration 101) under the request's
// row-level security.
type PolicyVersionRepo struct {
	q *sqlc.Queries
}

// NewPolicyVersionRepo reads through q, whose pool sets the RLS session.
func NewPolicyVersionRepo(q *sqlc.Queries) *PolicyVersionRepo {
	return &PolicyVersionRepo{q: q}
}

var _ policyh.VersionStore = (*PolicyVersionRepo)(nil)

// policyLayers maps the handler's layers onto the policy_layer enum.
var policyLayers = map[policyh.Layer]sqlc.PolicyLayer{
	policyh.LayerStorageBackend: sqlc.PolicyLayerStorageBackend,
	policyh.LayerBucket:         sqlc.PolicyLayerBucket,
	policyh.LayerCollection:     sqlc.PolicyLayerCollection,
	policyh.LayerTenant:         sqlc.PolicyLayerTenant,
}

// ListPolicyVersions resolves ref to the row holding the layer and pages its
// versions. A bucket or collection that does not exist is ErrLayerNotFound;
// a backend or tenant id is the owner itself, so an unknown one reads as no
// history.
func (r *PolicyVersionRepo) ListPolicyVersions(ctx context.Context, ref policyh.LayerRef, beforeVersion, pageSize int32) ([]policyh.Version, error) {
	layer, ok := policyLayers[ref.Layer]
	if !ok {
		return nil, fmt.Errorf("list policy versions: unknown layer %d", ref.Layer)
	}
	owner, err := r.owner(ctx, ref)
	if err != nil {
		return nil, err
	}
	rows, err := r.q.ListPolicyVersions(ctx, layer, owner, beforeVersion, pageSize)
	if err != nil {
		return nil, fmt.Errorf("list policy versions: %w", err)
	}
	out := make([]policyh.Version, 0, len(rows))
	for _, row := range rows {
		out = append(out, policyh.Version{
			Version:         row.Version,
			CedarPolicy:     row.CedarPolicy,
			WrittenBy:       row.WrittenBy,
			WrittenByTenant: optionalUUID(row.WrittenByTenantID),
			WrittenAt:       row.WrittenAt.Time,
		})
	}
	return out, nil
}

// owner is the id of the row ref's layer is stored on.
func (r *PolicyVersionRepo) owner(ctx context.Context, ref policyh.LayerRef) (pgtype.UUID, error) {
	var (
		id  pgtype.UUID
		err error
	)
	switch ref.Layer {
	case policyh.LayerStorageBackend:
		return pgUUID(ref.BackendID), nil
	case policyh.LayerTenant:
		return pgUUID(ref.TenantID), nil
	case policyh.LayerBucket:
		id, err = r.q.BucketIDForPolicy(ctx, pgUUID(ref.BackendID), ref.Bucket)
	case policyh.LayerCollection:
		id, err = r.q.CollectionIDForPolicy(ctx, pgUUID(ref.TenantID), ref.Collection)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, policyh.ErrLayerNotFound
	}
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("list policy versions: %w", err)
	}
	return id, nil
}

// optionalUUID is a nullable column's id, nil for NULL.
func optionalUUID(v pgtype.UUID) *uuid.UUID {
	if !v.Valid {
		return nil
	}
	id := uuid.UUID(v.Bytes)
	return &id
}
