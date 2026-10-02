//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

const (
	// retentionTTL is the age past which a terminal row is purged.
	retentionTTL = time.Hour
	// terminalIndex is the partial index the purge must ride.
	terminalIndex = "event_deliveries_terminal_idx"
	// pendingBacklog is the live queue the purge must not read; large enough
	// that a scan of it loses to the partial index.
	pendingBacklog = 20000
)

// seedDelivery inserts one outbox row in status, last attempted at lastAttempt
// (nil for never).
func seedDelivery(t *testing.T, f *dispatcherFixture, tenant, sub uuid.UUID, status string, lastAttempt *time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := f.h.PoolMigrate.Exec(context.Background(),
		`INSERT INTO event_deliveries
		   (id, tenant_id, subscription_id, event_type, event_at, event_payload, status, last_attempt_at)
		 VALUES ($1, $2, $3, 'paladin.object.uploaded', now(), '{}', $4, $5)`,
		id, tenant, sub, status, lastAttempt,
	); err != nil {
		t.Fatalf("seed delivery: %v", err)
	}
	return id
}

func deliveryExists(t *testing.T, f *dispatcherFixture, id uuid.UUID) bool {
	t.Helper()
	var n int
	if err := f.h.PoolMigrate.QueryRow(context.Background(),
		`SELECT count(*) FROM event_deliveries WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatalf("count delivery: %v", err)
	}
	return n == 1
}

func TestEventDeliveryRetention_PurgesOnlyOldTerminalRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := setupDispatcher(t)
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "retention")
	sub := f.seedSubscription(t, tenant, subOpts{URL: "http://unused.invalid"})

	old := time.Now().Add(-2 * retentionTTL)
	recent := time.Now().Add(-retentionTTL / 2)
	purged := []uuid.UUID{
		seedDelivery(t, f, tenant, sub, "delivered", &old),
		seedDelivery(t, f, tenant, sub, "failed", &old),
	}
	kept := map[string]uuid.UUID{
		"a recently delivered row":       seedDelivery(t, f, tenant, sub, "delivered", &recent),
		"a recently failed row":          seedDelivery(t, f, tenant, sub, "failed", &recent),
		"a pending row retried long ago": seedDelivery(t, f, tenant, sub, "pending", &old),
		"a pending row never attempted":  seedDelivery(t, f, tenant, sub, "pending", nil),
	}

	repo := adapters.NewEventDeliveryRepo(sqlc.New(f.h.PoolMigrate))
	cutoff := time.Now().Add(-retentionTTL)
	// A batch of one: each call takes one row, so the second proves the
	// batch bound and the third that nothing else qualifies.
	for i, want := range []int64{1, 1, 0} {
		n, err := repo.PurgeTerminalBefore(ctx, cutoff, 1)
		if err != nil {
			t.Fatalf("purge %d: %v", i, err)
		}
		if n != want {
			t.Errorf("purge %d deleted %d rows, want %d", i, n, want)
		}
	}
	for _, id := range purged {
		if deliveryExists(t, f, id) {
			t.Errorf("an old terminal row %s survived the purge", id)
		}
	}
	for what, id := range kept {
		if !deliveryExists(t, f, id) {
			t.Errorf("the purge deleted %s", what)
		}
	}
}

func TestEventDeliveryRetention_RidesThePartialIndex(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := setupDispatcher(t)
	tenant := mustCreateTenant(t, f.h.PoolMigrate, "retention-plan")
	sub := f.seedSubscription(t, tenant, subOpts{URL: "http://unused.invalid"})

	mustExecPool(t, f, `INSERT INTO event_deliveries
		   (tenant_id, subscription_id, event_type, event_at, event_payload, status, last_attempt_at)
		 SELECT $1, $2, 'paladin.object.uploaded', now(), '{}', 'pending', now() - interval '1 day'
		   FROM generate_series(1, $3::int)`, tenant, sub, pendingBacklog)
	old := time.Now().Add(-2 * retentionTTL)
	seedDelivery(t, f, tenant, sub, "delivered", &old)
	mustExecPool(t, f, `ANALYZE event_deliveries`)

	rows, err := f.h.PoolMigrate.Query(ctx, `EXPLAIN
		SELECT d.ctid FROM event_deliveries AS d
		WHERE d.status IN ('delivered', 'failed') AND d.last_attempt_at < $1::timestamptz
		ORDER BY d.last_attempt_at LIMIT 10000`, time.Now().Add(-retentionTTL))
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plan.WriteString(line + "\n")
	}
	if !strings.Contains(plan.String(), terminalIndex) {
		t.Errorf("the purge does not use %s — it reads the pending queue:\n%s", terminalIndex, plan.String())
	}
}

func mustExecPool(t *testing.T, f *dispatcherFixture, sql string, args ...any) {
	t.Helper()
	if _, err := f.h.PoolMigrate.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec: %v", err)
	}
}
