//go:build integration

// OutboxRunner.OutboxDepth is the fan-out-volume measurement: cluster-wide
// pending backlog + the deepest single-tenant backlog. Verifies the aggregate
// against a seeded mix so the numbers operators tune on are trustworthy.
package integration

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

func TestOutboxDepth_TotalAndMaxPerTenant(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()

	tenantA := mustCreateTenant(t, h.PoolMigrate, "ten-depth-a")
	tenantB := mustCreateTenant(t, h.PoolMigrate, "ten-depth-b")
	// One subscription per tenant: the deliveries FK to it, and the depth
	// gauge counts rows, not subscriptions.
	subA := seedSubscriptionRow(t, h.PoolMigrate, tenantA)
	subB := seedSubscriptionRow(t, h.PoolMigrate, tenantB)
	subOf := map[uuid.UUID]uuid.UUID{tenantA: subA, tenantB: subB}

	seedPending := func(tenant uuid.UUID, n int) {
		for i := 0; i < n; i++ {
			if _, err := h.PoolMigrate.Exec(ctx,
				`INSERT INTO event_deliveries
				   (id, tenant_id, subscription_id, event_type, event_at, event_payload, status)
				 VALUES ($1, $2, $3, 'paladin.test.event', now(), '{}'::jsonb, 'pending')`,
				uuid.New(), tenant, subOf[tenant],
			); err != nil {
				t.Fatalf("seed pending: %v", err)
			}
		}
	}
	seedPending(tenantA, 3)
	seedPending(tenantB, 1)
	// A delivered row must NOT count toward the pending backlog.
	if _, err := h.PoolMigrate.Exec(ctx,
		`INSERT INTO event_deliveries
		   (id, tenant_id, subscription_id, event_type, event_at, event_payload, status)
		 VALUES ($1, $2, $3, 'paladin.test.event', now(), '{}'::jsonb, 'delivered')`,
		uuid.New(), tenantA, subA,
	); err != nil {
		t.Fatalf("seed delivered: %v", err)
	}

	r := &worker.OutboxRunner{Pool: h.PoolMigrate}
	total, maxPerTenant, err := r.OutboxDepth(ctx)
	if err != nil {
		t.Fatalf("OutboxDepth: %v", err)
	}
	if total != 4 {
		t.Errorf("total pending = %d, want 4 (3+1; delivered excluded)", total)
	}
	if maxPerTenant != 3 {
		t.Errorf("max per-tenant = %d, want 3 (tenant A)", maxPerTenant)
	}
}

// seedSubscriptionRow inserts the minimum event_subscriptions row an outbox
// delivery can point at. event_deliveries.subscription_id is a real FK, so a
// synthetic uuid no longer stands in for a subscription that was never
// created.
func seedSubscriptionRow(t *testing.T, pool *pgxpool.Pool, tenant uuid.UUID) uuid.UUID {
	t.Helper()
	subID := uuid.New()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO event_subscriptions (id, tenant_id, sink_kind, sink_config)
		 VALUES ($1, $2, 'http', '{"url": "https://example.invalid/hook"}'::jsonb)`,
		subID, tenant,
	); err != nil {
		t.Fatalf("seed event_subscriptions: %v", err)
	}
	return subID
}
