//go:build integration

// OutboxRunner.OutboxDepth is the fan-out-volume measurement: cluster-wide
// pending backlog + the deepest single-tenant backlog. Verifies the aggregate
// against a seeded mix so the numbers operators tune on are trustworthy.
package integration

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin-private/internal/worker"
	"github.com/oleg-tkachuk/paladin-private/tests/integration/pgharness"
)

func TestOutboxDepth_TotalAndMaxPerTenant(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()

	tenantA := mustCreateTenant(t, h.PoolMigrate, "ten-depth-a")
	tenantB := mustCreateTenant(t, h.PoolMigrate, "ten-depth-b")

	seedPending := func(tenant uuid.UUID, n int) {
		for i := 0; i < n; i++ {
			if _, err := h.PoolMigrate.Exec(ctx,
				`INSERT INTO event_deliveries
				   (id, tenant_id, subscription_id, event_type, event_at, event_payload, status)
				 VALUES ($1, $2, $3, 'paladin.test.event', now(), '{}'::jsonb, 'pending')`,
				uuid.New(), tenant, uuid.New(),
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
		uuid.New(), tenantA, uuid.New(),
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
