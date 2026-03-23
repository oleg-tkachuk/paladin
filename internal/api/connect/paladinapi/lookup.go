package paladinapi

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/domain"
)

// getObjectResiliently attempts to find an object first by key-based addressing
// (bucket + path), and falls back to ID-based lookup if the key looks like a UUID
// and is not found as a literal path. This allows clients to use either the
// human-friendly key or the stable ObjectID.
func getObjectResiliently(ctx context.Context, svc domain.ObjectsService, tenantID, bucket, key string) (*domain.Object, error) {
	rec, err := svc.GetByKey(ctx, tenantID, bucket, key)
	if err == nil {
		return rec, nil
	}

	// Fallback to ID-based lookup if key looks like a UUID
	if errors.Is(err, domain.ErrNotFound) {
		if id, parseErr := uuid.Parse(key); parseErr == nil {
			return svc.Get(ctx, tenantID, id)
		}
	}

	return nil, err
}
