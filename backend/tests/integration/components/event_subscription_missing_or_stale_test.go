//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// A guarded subscription write that touches no row has two causes: the
// subscription is gone (NOT_FOUND, stop) or it is there at another version
// (ABORTED, re-read and retry). Both used to come back as a version mismatch,
// so a write with the version check skipped answered ABORTED for a
// subscription that can never be found.
func TestGuardedEventSubscriptionWritesTellMissingFromStale(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewEventSubscriptionRepoV2(sqlc.New(pool))

	tenantID, _ := mkTenant(t, ctx, pool, "shared")
	sub := admindomain.EventSubscription{
		TenantID:   tenantID,
		SinkKind:   string(sqlc.EventSinkKindHttp),
		SinkConfig: []byte(`{}`),
	}
	if err := repo.Create(ctx, &sub); err != nil {
		t.Fatalf("create: %v", err)
	}
	current, err := repo.Get(ctx, sub.SubscriptionID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	stale := current.ResourceVersion + 1

	writes := map[string]func(id uuid.UUID, version int64) error{
		"update": func(id uuid.UUID, v int64) error {
			return repo.Update(ctx, admindomain.EventSubscription{SubscriptionID: id, Disabled: true},
				v, []string{admindomain.EventSubscriptionPathDisabled})
		},
		"delete": func(id uuid.UUID, v int64) error { return repo.Delete(ctx, id, v) },
	}
	for what, write := range writes {
		t.Run(what, func(t *testing.T) {
			for _, version := range []int64{skipVersionCheck, stale} {
				if err := write(uuid.New(), version); !errors.Is(err, admindomain.ErrNotFound) {
					t.Errorf("missing subscription at version %d = %v, want ErrNotFound", version, err)
				}
			}
			if err := write(sub.SubscriptionID, stale); !errors.Is(err, admindomain.ErrVersionMismatch) {
				t.Errorf("stale version = %v, want ErrVersionMismatch", err)
			}
		})
	}
}
