//go:build integration

package integration

import (
	"context"
	"net/http"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// statusDelivered and statusFailed are event_deliveries.status values.
const (
	statusDelivered = "delivered"
	statusFailed    = "failed"
)

// A delivery that exhausted its attempts stayed failed for good. A redrive
// queues it again with a fresh budget, under the caller's tenant only.
func TestRedriveFailedDeliveries_SendsThemAgain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := setupDispatcher(t)
	// The first delivery fails, every later one succeeds.
	rec := newRecorder(http.StatusOK, http.StatusInternalServerError)
	defer rec.Close()

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "redrive")
	sub := f.seedSubscription(t, tenant, subOpts{URL: rec.srv.URL})
	other := mustCreateTenant(t, f.h.PoolMigrate, "redrive-other")
	otherSub := f.seedSubscription(t, other, subOpts{URL: "http://unused.invalid"})
	otherFailed := seedDelivery(t, f, other, otherSub, statusFailed, nil)

	d := f.dispatcher()
	if _, err := d.Dispatch(ctx, tenant.String(), makeEvent("", tenant)); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	row := f.allDeliveryRows(t, tenant)[0]
	runner := f.outboxRunnerWithMax(d, 1)
	f.tickOnce(t, runner)
	if got := f.deliveryRow(t, row.ID); got.Status != statusFailed {
		t.Fatalf("status = %q after its only attempt failed, want %s", got.Status, statusFailed)
	}

	// Through the runtime pool, as the handler runs: RLS scopes it to the
	// caller's tenant.
	asTenant := auth.WithPrincipal(ctx, &auth.Principal{TenantID: tenant})
	repo := adapters.NewEventSubscriptionRepoV2(sqlc.New(f.h.PoolApp))
	n, err := repo.RequeueFailedDeliveries(asTenant, sub)
	if err != nil {
		t.Fatalf("redrive: %v", err)
	}
	if n != 1 {
		t.Fatalf("requeued %d, want 1", n)
	}
	got := f.deliveryRow(t, row.ID)
	if got.Status != "pending" || got.Attempts != 0 || got.LastError != "" {
		t.Errorf("redriven row = %s/%d/%q, want pending with a fresh budget and no error",
			got.Status, got.Attempts, got.LastError)
	}

	f.tickOnce(t, runner)
	if got := f.deliveryRow(t, row.ID); got.Status != statusDelivered {
		t.Errorf("status = %q after the redrive, want %s", got.Status, statusDelivered)
	}
	if n, err := repo.RequeueFailedDeliveries(asTenant, sub); err != nil || n != 0 {
		t.Errorf("second redrive = (%d, %v), want (0, nil)", n, err)
	}

	// Another tenant's subscription, named by id, is out of reach.
	if n, err := repo.RequeueFailedDeliveries(asTenant, otherSub); err != nil || n != 0 {
		t.Errorf("redrive of another tenant's subscription = (%d, %v), want (0, nil)", n, err)
	}
	if got := f.deliveryRow(t, otherFailed); got.Status != statusFailed {
		t.Errorf("another tenant's failed delivery became %s", got.Status)
	}
}
