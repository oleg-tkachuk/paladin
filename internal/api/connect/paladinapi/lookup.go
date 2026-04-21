package paladinapi

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/domain"
)

// identity represents the addressable identity of an object in a request.
// Exactly one of (ObjectID) or (Bucket + Key) should be populated; callers
// that supply both forms will be resolved by ObjectID first.
type identity struct {
	ObjectID string
	Bucket   string
	Key      string
}

// resolveObject locates an object using the preferred object_id when present,
// falling back to a (bucket, key) lookup. Returns connect.CodeInvalidArgument
// when the request carries no usable identity, and connect.CodeNotFound when
// the object cannot be located.
//
// This is the single entry point all object-mutating handlers should use to
// turn a request into a domain.Object — centralising identity resolution
// prevents adapters from re-implementing ad-hoc fallbacks.
func resolveObject(ctx context.Context, svc domain.ObjectsService, tenantID string, id identity) (*domain.Object, error) {
	// Preferred path: stable UUID.
	if id.ObjectID != "" {
		parsed, err := uuid.Parse(id.ObjectID)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("object_id is not a valid UUID: %w", err))
		}

		return svc.Get(ctx, tenantID, parsed)
	}

	// Fallback: storage-path addressing.
	if id.Bucket == "" || id.Key == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("either object_id or (bucket + key) must be provided"))
	}

	rec, err := svc.GetByKey(ctx, tenantID, id.Bucket, id.Key)
	if err == nil {
		return rec, nil
	}

	// Historical fallback: older clients sometimes pass a UUID in the key
	// field. Accept it so they keep working against this API.
	if errors.Is(err, domain.ErrNotFound) {
		if parsed, parseErr := uuid.Parse(id.Key); parseErr == nil {
			return svc.Get(ctx, tenantID, parsed)
		}
	}

	return nil, err
}

// getObjectResiliently is a thin back-compat shim over resolveObject for call
// sites that only have (bucket, key). New code should build an `identity` and
// call resolveObject directly.
//
// Deprecated: prefer resolveObject with an identity{} value.
func getObjectResiliently(ctx context.Context, svc domain.ObjectsService, tenantID, bucket, key string) (*domain.Object, error) {
	return resolveObject(ctx, svc, tenantID, identity{Bucket: bucket, Key: key})
}
