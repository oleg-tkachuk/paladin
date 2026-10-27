package tenanth

import (
	"context"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// PurgePreviewNames is how many API tokens and event subscriptions
// PreviewPurgeTenant names of each; the counts beside them are all of them.
// The contract states the same number (PreviewPurgeTenantResponse).
const PurgePreviewNames = 50

// PurgePreview is what a purge of a tenant would remove and what would
// refuse it, read in one snapshot.
type PurgePreview struct {
	// APITokens are the first PurgePreviewNames of APITokenCount, in issue
	// order.
	APITokens     []PurgedAPIToken
	APITokenCount int64
	// Subscriptions are the first PurgePreviewNames of SubscriptionCount, in
	// creation order.
	Subscriptions     []PurgedSubscription
	SubscriptionCount int64
	// CapabilityCount is every capability of the tenant on record.
	CapabilityCount int64
	// Blockers are the rows that refuse the purge while any is left.
	Blockers PurgeBlockers
}

// PurgedAPIToken is one API token a purge removes.
type PurgedAPIToken struct {
	ID   uuid.UUID
	Name string
}

// PurgedSubscription is one event subscription a purge removes, by the kind
// of sink it delivers to.
type PurgedSubscription struct {
	ID       uuid.UUID
	SinkKind string
}

// PurgeBlockers counts the ON DELETE RESTRICT children of a tenant
// (docs/deletion-semantics.md).
type PurgeBlockers struct {
	Collections, Buckets, Users int64
}

// None reports whether nothing the tenant owns refuses its purge.
func (b PurgeBlockers) None() bool {
	return b.Collections == 0 && b.Buckets == 0 && b.Users == 0
}

// PreviewPurgeTenant answers what PurgeTenant would remove with the tenant
// and what would refuse it, changing nothing. It asks what PurgeTenant asks
// of the caller: it names the tenant's tokens, which is as much as the purge
// reaches. The tenant may be active or trashed.
func (h *Handler) PreviewPurgeTenant(ctx context.Context, tenantID uuid.UUID) (*PurgePreview, error) {
	if err := requirePlatformAdmin(ctx); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, cedar.ActionManageTenant, tenantID); err != nil {
		return nil, err
	}
	if _, err := h.repo.Get(ctx, tenantID); err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err.Error()).WithCause(err)
	}
	p, err := h.repo.PreviewPurge(ctx, tenantID, PurgePreviewNames)
	if err != nil {
		return nil, apiutil.MapError(err)
	}
	return &p, nil
}
