//go:build integration

// Persists one event subscription per sink kind the dispatcher delivers to and
// reads it back. The broker suites call DeliverOne on an in-memory row, so a
// kind the event_sink_kind enum does not accept would pass them and still fail
// the INSERT every admin create goes through.
package components

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

func TestEventSubscription_PersistsEverySinkKind(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()

	tenantID := uuid.New()
	if _, err := h.PoolMigrate.Exec(ctx,
		`INSERT INTO tenants (id, slug, display_name) VALUES ($1, $2, $3)`,
		tenantID, "sink-kind-persist", "Sink Kind Persist",
	); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	repo := adapters.NewEventSubscriptionRepoV2(sqlc.New(h.PoolMigrate))

	// The kinds sinkToConfig writes and Dispatcher.deliver switches on.
	kinds := []sqlc.EventSinkKind{
		sqlc.EventSinkKindHttp,
		sqlc.EventSinkKindNats,
		sqlc.EventSinkKindKafka,
		sqlc.EventSinkKindSqs,
		sqlc.EventSinkKindRabbitmq,
	}
	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			cfg, err := json.Marshal(map[string]string{"probe": string(kind)})
			if err != nil {
				t.Fatalf("marshal sink config: %v", err)
			}
			sub := admindomain.EventSubscription{
				TenantID:   tenantID,
				SinkKind:   string(kind),
				SinkConfig: cfg,
			}
			if err := repo.Create(ctx, &sub); err != nil {
				t.Fatalf("Create(%s): %v", kind, err)
			}
			got, err := repo.Get(ctx, sub.SubscriptionID)
			if err != nil {
				t.Fatalf("Get(%s): %v", kind, err)
			}
			if got.SinkKind != string(kind) {
				t.Errorf("SinkKind = %q, want %q", got.SinkKind, kind)
			}
		})
	}
}
