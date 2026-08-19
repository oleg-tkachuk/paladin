//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/tests/integration/pgharness"
)

// TestEventSubscriptionRepoV2_Create_StampsGeneratedID pins the
// pointer-receiver behaviour of `Create`. The bug it guards against
// (commit 19d2296):
//
//	Before the fix, Create accepted `s admindomain.EventSubscription`
//	by value. The mutation `s.SubscriptionID = uuid.NewV7()` happened on
//	the local copy, so the caller's struct retained `uuid.Nil`. The
//	admin handler then called `repo.Get(ctx, s.SubscriptionID)` with
//	the zero UUID, hit pgx.ErrNoRows → admindomain.ErrNotFound → 500
//	"admin: resource not found" returned to the operator, while the
//	row sat happily committed in the DB (autocommit on INSERT).
//
// The contract this test asserts:
//
//  1. Caller passes uuid.Nil. Repo generates one and writes it back
//     via the pointer.
//  2. The id the caller now holds matches the row in the DB (Get
//     finds it).
//
// If anyone flips the receiver back to a value type for "aesthetics",
// this test fails with a clear "subscription id was not stamped"
// message before any production traffic notices.
func TestEventSubscriptionRepoV2_Create_StampsGeneratedID(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()

	// Seed a tenant the FK can attach to. Use paladin_migrate (BYPASSRLS)
	// so we don't have to plumb tenant_id through the GUC just to
	// satisfy RLS during the seed.
	tenantID := uuid.New()
	if _, err := h.PoolMigrate.Exec(ctx,
		`INSERT INTO tenants (tenant_id, slug, display_name) VALUES ($1, $2, $3)`,
		tenantID, "regression-create-stamp", "Regression Tenant",
	); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	repo := adapters.NewEventSubscriptionRepoV2(sqlc.New(h.PoolMigrate))

	cfg, _ := json.Marshal(map[string]string{
		"url":     "https://example.test/webhook",
		"subject": "paladin.events",
	})
	sub := admindomain.EventSubscription{
		// SubscriptionID intentionally left zero — this is the path
		// the admin handler takes from CreateSubscription.
		TenantID:   tenantID,
		CELFilter:  "",
		SinkKind:   "http",
		SinkConfig: cfg,
		Disabled:   false,
	}

	if err := repo.Create(ctx, &sub); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sub.SubscriptionID == uuid.Nil {
		t.Fatal("subscription id was not stamped on the input — " +
			"the pointer-receiver contract on Create was broken; " +
			"see commit 19d2296 for the regression history")
	}

	// Cross-check: the id Create stamped is the id Get finds.
	got, err := repo.Get(ctx, sub.SubscriptionID)
	if err != nil {
		t.Fatalf("Get(stamped id): %v", err)
	}
	if got.SubscriptionID != sub.SubscriptionID {
		t.Errorf("round-trip id mismatch: stamped=%s got=%s",
			sub.SubscriptionID, got.SubscriptionID)
	}
	if got.TenantID != tenantID {
		t.Errorf("round-trip tenant_id: got=%s want=%s",
			got.TenantID, tenantID)
	}
}

// TestEventSubscriptionRepoV2_Create_AcceptsNATSSinkKind guards
// migration 029 — the CHECK constraint widening that lets sink_kind
// take 'nats'. Before 029, an INSERT with sink_kind='nats' failed
// SQLSTATE 23514. The migration is part of `migrations.FS`, applied
// automatically by pgharness.Setup, so this test will fail at INSERT
// time if 029 ever stops applying (e.g. another goose syntax slip).
func TestEventSubscriptionRepoV2_Create_AcceptsNATSSinkKind(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()

	tenantID := uuid.New()
	if _, err := h.PoolMigrate.Exec(ctx,
		`INSERT INTO tenants (tenant_id, slug, display_name) VALUES ($1, $2, $3)`,
		tenantID, "regression-nats-kind", "Regression NATS",
	); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	repo := adapters.NewEventSubscriptionRepoV2(sqlc.New(h.PoolMigrate))

	cfg, _ := json.Marshal(map[string]string{
		"url":     "nats://nats.example:4222",
		"subject": "paladin.events",
	})
	sub := admindomain.EventSubscription{
		TenantID:   tenantID,
		SinkKind:   "nats",
		SinkConfig: cfg,
	}

	if err := repo.Create(ctx, &sub); err != nil {
		t.Fatalf("Create with sink_kind='nats': %v "+
			"(migration 029 likely failed to apply — check the "+
			"goose annotations on 029_event_subscriptions_sink_kind_nats.sql)",
			err)
	}

	// Sanity: row is actually there + sink_kind round-trips.
	var sinkKind string
	if err := h.PoolMigrate.QueryRow(ctx,
		`SELECT sink_kind FROM event_subscriptions WHERE subscription_id = $1`,
		sub.SubscriptionID,
	).Scan(&sinkKind); err != nil {
		if err == pgx.ErrNoRows {
			t.Fatalf("row missing after Create — pointer-receiver "+
				"regression? see TestEventSubscriptionRepoV2_Create_StampsGeneratedID. "+
				"stamped id: %s", sub.SubscriptionID)
		}
		t.Fatalf("verify row: %v", err)
	}
	if sinkKind != "nats" {
		t.Errorf("sink_kind round-trip: got %q want %q", sinkKind, "nats")
	}
}
