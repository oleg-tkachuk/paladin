//go:build integration

// E2E for the ingest plane: SeaweedFS-shaped webhook payload → ingest
// worker → statemachine.PromoteToAvailable → row state flipped to
// AVAILABLE. Validates the full chain that the unit tests cover in
// pieces (parse, dedup, handler) against a real Postgres so the SQL
// columns, FK chain, and statemachine guards are exercised together.
//
// HTTP-layer (driver_webhook) coverage stays in the unit tests —
// HMAC verification, body cap, method-not-allowed are framework
// concerns that the integration harness wouldn't add value to.
package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/internal/eventingest"
	"github.com/oleg-tkachuk/paladin/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/tests/integration/pgharness"
)

// TestIngest_SeaweedFSWebhookPromotesPending — the canonical flow:
//  1. seed a tenant, collection, PENDING object
//  2. parse a SeaweedFS-shape webhook payload through the source
//     adapter
//  3. dispatch through Worker (dedup row + handler call)
//  4. assert the object row flipped to AVAILABLE and the dedup
//     row exists exactly once
//  5. replay the same event — second call must be a no-op (dedup
//     kicks in)
func TestIngest_SeaweedFSWebhookPromotesPending(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()

	tenantID := mustCreateTenant(t, h.PoolMigrate, "ingest-tenant")
	mustCreateCollection(t, h.PoolMigrate, tenantID, "docs")
	objectID := mustInsertPendingObject(t, h.PoolMigrate, tenantID, "docs", "report.pdf")

	q := sqlc.New(h.PoolMigrate)
	worker := &eventingest.Worker{
		Driver: nil, // we drive Deliver directly; the HTTP layer is unit-tested
		Handler: &eventingest.PromoteHandler{
			Lookup:       q,
			Transitioner: statemachine.New(h.PoolMigrate),
		},
		Dedup: &eventingest.PgxDedupStore{Q: q},
	}

	source := &eventingest.SeaweedFSSource{
		BucketName: "paladin-test",
		URI:        "seaweedfs://primary",
	}
	payload, _ := json.Marshal(map[string]any{
		"key":          fmt.Sprintf("/paladin-test/%s/docs/report.pdf", tenantID),
		"event_type":   "create",
		"timestamp_ns": time.Now().UnixNano(),
		"etag":         "abc123",
		"size":         1024,
	})

	ev, err := source.Parse(payload, "application/json")
	if err != nil {
		t.Fatalf("source parse: %v", err)
	}
	if ev.SubjectFields.TenantID != tenantID.String() {
		t.Fatalf("parsed tenant_id = %s, want %s", ev.SubjectFields.TenantID, tenantID)
	}

	if err := worker.Deliver(ctx, ev); err != nil {
		t.Fatalf("worker.Deliver: %v", err)
	}

	if state := mustObjectState(t, h.PoolMigrate, objectID); state != "AVAILABLE" {
		t.Errorf("object state = %s, want AVAILABLE", state)
	}
	if n := mustCountIngested(t, h.PoolMigrate); n != 1 {
		t.Errorf("ingested_events = %d, want 1", n)
	}

	// Replay: same event id → dedup hit, handler not invoked again.
	if err := worker.Deliver(ctx, ev); err != nil {
		t.Fatalf("worker.Deliver replay: %v", err)
	}
	if n := mustCountIngested(t, h.PoolMigrate); n != 1 {
		t.Errorf("ingested_events after replay = %d, want 1 (dedup kicked in)", n)
	}
}

// ─── helpers (object lifecycle) ─────────────────────────────────────

func mustInsertPendingObject(t *testing.T, pool *pgxpool.Pool, tenantID uuid.UUID, collection, key string) uuid.UUID {
	t.Helper()
	objID := uuid.New()
	_, err := pool.Exec(context.Background(), `
        INSERT INTO objects (
            object_id, tenant_id, collection, key, state,
            content_type, checksum_algorithm, presign_expires_at
        ) VALUES (
            $1, $2, $3, $4, 'PENDING',
            'application/octet-stream', 1, now() + interval '15 minutes'
        )
    `, objID, tenantID, collection, key)
	if err != nil {
		t.Fatalf("insert pending object: %v", err)
	}
	return objID
}

func mustObjectState(t *testing.T, pool *pgxpool.Pool, objectID uuid.UUID) string {
	t.Helper()
	var state string
	err := pool.QueryRow(context.Background(),
		`SELECT state::text FROM objects WHERE object_id = $1`,
		pgtype.UUID{Bytes: objectID, Valid: true},
	).Scan(&state)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("object %s not found", objectID)
		}
		t.Fatalf("query state: %v", err)
	}
	return state
}

func mustCountIngested(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM ingested_events`,
	).Scan(&n); err != nil {
		t.Fatalf("count ingested: %v", err)
	}
	return n
}
