package auth

import (
	"context"

	"github.com/google/uuid"
)

// TenantState is whether a principal's tenant may use its credentials.
type TenantState int

const (
	// TenantLive is a tenant in use.
	TenantLive TenantState = iota + 1
	// TenantTrashed is a tenant in the trash: its rows remain, so it may be
	// restored, and its credentials then work again.
	TenantTrashed
	// TenantMissing is a tenant that does not exist: purged, or never was.
	TenantMissing
)

// TenantStateReader reads a tenant's state from where it is stored.
type TenantStateReader interface {
	TenantState(ctx context.Context, tenantID uuid.UUID) (TenantState, error)
}
