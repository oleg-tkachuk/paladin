package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// ObjectTaintRepo persists objects.taint.
type ObjectTaintRepo struct {
	q *sqlc.Queries
}

// NewObjectTaintRepo wires the taint queries.
func NewObjectTaintRepo(q *sqlc.Queries) *ObjectTaintRepo {
	return &ObjectTaintRepo{q: q}
}

var _ objecth.TaintRepository = (*ObjectTaintRepo)(nil)

// SetTaint replaces an object's signals. objecth.ErrObjectNotFound when the
// tenant has no such object.
func (r *ObjectTaintRepo) SetTaint(ctx context.Context, tenantID, objectID uuid.UUID, signals []string) ([]string, error) {
	if signals == nil {
		signals = []string{}
	}
	got, err := r.q.SetObjectTaint(ctx, pgUUID(tenantID), pgUUID(objectID), signals)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, objecth.ErrObjectNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("set object taint: %w", err)
	}
	return got, nil
}

// TaintAtPath returns the signals of the live object at a path; none when
// there is no live object there.
func (r *ObjectTaintRepo) TaintAtPath(ctx context.Context, tenantID uuid.UUID, collection, key string) ([]string, error) {
	got, err := r.q.ObjectTaintAtPath(ctx, pgUUID(tenantID), collection, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("object taint at path: %w", err)
	}
	return got, nil
}
