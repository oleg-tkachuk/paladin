//go:build integration

// Exercises the RLS-aware harness: pgharness.AssertRLSHidesCrossTenant pins the
// production failure mode both the dispatcher and the reaper bugs hit — a
// cross-tenant read on a GUC-less paladin_app pool (no request principal → no
// tenant GUC) matches ZERO rows, so any background component that runs there
// silently no-ops and MUST instead use a BYPASSRLS pool.
package integration

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

// TestRLSHarness_HidesCrossTenant_DispatcherOutbox: event_deliveries (the
// dispatcher's outbox) is RLS-scoped. Seeded cross-tenant via PoolMigrate, it is
// visible on the BYPASSRLS pool but invisible on the GUC-less paladin_app pool —
// exactly why the dispatcher / OutboxRunner must run on a BYPASSRLS pool.
func TestRLSHarness_HidesCrossTenant_DispatcherOutbox(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()
	tenantID := mustCreateTenant(t, h.PoolMigrate, "ten-rls-harness")

	if _, err := h.PoolMigrate.Exec(ctx,
		`INSERT INTO event_deliveries
		   (id, tenant_id, subscription_id, event_type, event_at, event_payload)
		 VALUES ($1, $2, $3, 'paladin.test.event', now(), '{}'::jsonb)`,
		uuid.New(), tenantID, seedSubscriptionRow(t, h.PoolMigrate, tenantID),
	); err != nil {
		t.Fatalf("seed event_deliveries: %v", err)
	}

	h.AssertRLSHidesCrossTenant(t,
		`SELECT count(*) FROM event_deliveries WHERE tenant_id = $1`, tenantID)
}
