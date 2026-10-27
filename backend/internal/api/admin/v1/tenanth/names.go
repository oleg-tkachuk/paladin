package tenanth

import (
	"context"
	"uuid"
)

// Name is a tenant as a listing of other rows names it: a budget summary
// joins its own rows to tenants by id.
type Name struct {
	Slug        string
	DisplayName string
	// Deleted reports that the tenant is in the trash.
	Deleted bool
}

// Names reads the names of tenants by id. A tenant that does not exist is
// absent from the result.
type Names interface {
	TenantNames(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]Name, error)
}
