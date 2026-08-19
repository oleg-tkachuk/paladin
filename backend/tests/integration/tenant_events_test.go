//go:build integration

// End-to-end coverage for the producer half of the event bus —
// admin handler call → row in event_deliveries → CloudEvents
// envelope on NATS. The dispatcher pod's outbox loop and HTTP
// signing semantics are already exercised by dispatcher_test.go;
// this file pins the OTHER seam those tests intentionally
// short-circuited: that lifecycle handlers actually call
// `Dispatcher.Dispatch` after a successful write.
//
// History: commit c713f77 ("durable webhook fan-out") wired
// the consumer half but no production handler ever called the
// producer, so real Paladin events silently never fanned out (only
// TestSubscription's DeliverOne path lit up). This test guards
// the wiring landed in the same change as itself, and gives
// the next handler-class (bucket / objectKey / object lifecycle)
// a copy-paste template.
package integration

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	tenantpkg "github.com/oleg-tkachuk/paladin/internal/api/v1/tenant"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/internal/worker"
)

// TestTenantHandler_UpdateDispatchesPaladinTenantUpdated exercises the
// full producer chain: handler → outbox writer → dispatcher pod loop
// → embedded NATS subscriber. Asserts the subscriber receives a
// CloudEvents 1.0 envelope with `type == "paladin.tenant.updated"` and
// the right tenant_id.
//
// We use UpdateTenant rather than CreateTenant for one wiring reason:
// the producer fans out only to subscriptions whose `tenant_id`
// equals the event's tenant_id (single-tenant subscription scope is
// the v1 contract). For an `paladin.tenant.created` event the new
// tenant_id has no subscription yet, so a CreateTenant test would
// have to fabricate the subscription out-of-order. Update events
// fan out cleanly because the tenant exists by the time we
// subscribe.
func TestTenantHandler_UpdateDispatchesPaladinTenantUpdated(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)

	url := runEmbeddedNATSForIntegration(t)
	pc, err := nats.Connect(url, nats.Timeout(2*time.Second))
	if err != nil {
		t.Fatalf("probe connect: %v", err)
	}
	defer pc.Close()
	got := make(chan *nats.Msg, 1)
	if _, err := pc.Subscribe("paladin.events", func(m *nats.Msg) { got <- m }); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := pc.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "tenant-events-update")
	_ = f.seedNATSSubscription(t, tenant, url, "paladin.events")

	pool := worker.NewNatsConnPool(nil)
	defer pool.Close()
	d := f.dispatcher()
	d.NATS = pool

	repo := adapters.NewTenantRepo(sqlc.New(f.h.PoolMigrate), f.h.PoolMigrate)
	handler := tenantpkg.NewHandler(repo, allowAuth{})
	handler.SetEventProducer(d)

	ctx := ctxAdmin(t, tenant)

	newDisplay := "Tenant After Update"
	if _, err := handler.UpdateTenant(ctx, tenantpkg.UpdateTenantArgs{
		TenantID: tenant,
		// resource_version=1 — fresh tenants land at v1 per the
		// `tenants.resource_version` DEFAULT, and tenant.UpdateTenant's
		// SQL hardcodes the OCC predicate (no 0-opt-out path on Update,
		// unlike Delete which has one). If a future migration changes
		// the default this needs to bump in lockstep.
		ExpectedVersion: 1,
		DisplayName:     &newDisplay,
	}); err != nil {
		t.Fatalf("UpdateTenant: %v", err)
	}

	// Producer half: outbox row landed.
	rows := f.allDeliveryRows(t, tenant)
	if len(rows) != 1 {
		t.Fatalf("event_deliveries rows = %d, want 1 — handler.SetEventProducer wiring "+
			"is the most likely regression. Check build_listeners_admin.go "+
			"and tenanth.dispatchEvent.", len(rows))
	}
	if rows[0].Status != "pending" {
		t.Fatalf("row[0].Status = %q, want pending", rows[0].Status)
	}

	// Consumer half: outbox runner picks it up and the NATS subscriber
	// gets a CloudEvents envelope.
	r := f.outboxRunner(d)
	if n := f.tickOnce(t, r); n != 1 {
		t.Fatalf("tick processed = %d, want 1", n)
	}

	select {
	case m := <-got:
		var env struct {
			SpecVersion string `json:"specversion"`
			Type        string `json:"type"`
			TenantID    string `json:"tenantid"`
			Subject     string `json:"subject"`
		}
		if err := json.Unmarshal(m.Data, &env); err != nil {
			t.Fatalf("envelope unmarshal: %v (raw: %s)", err, string(m.Data))
		}
		if env.SpecVersion != "1.0" {
			t.Errorf("specversion = %q, want 1.0", env.SpecVersion)
		}
		if env.Type != "paladin.tenant.updated" {
			t.Errorf("type = %q, want paladin.tenant.updated", env.Type)
		}
		if env.TenantID != tenant.String() {
			t.Errorf("tenantid = %q, want %q", env.TenantID, tenant.String())
		}
		// resource_name → CloudEvents `subject` per the dispatcher's
		// envelope mapping. Stable name lets subscribers route on it.
		want := "tenants/" + tenant.String()
		if env.Subject != want {
			t.Errorf("subject = %q, want %q", env.Subject, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nats: no message within 2s — producer wired but " +
			"delivery short-circuited; check dispatcher.NATS pool + " +
			"runner tick semantics")
	}
}

// TestTenantHandler_UpdateWithNilProducer_DoesNotPanic guards the
// "producer is optional" contract. In the early phase of any new
// deployment (or in unit tests that don't bother with the dispatcher)
// the handler is constructed without an EventProducer attached. The
// lifecycle calls must NOT panic on a nil producer; they should just
// silently skip the dispatch and return success. Without this guard
// a regression that drops the nil-check (`if h.events == nil`) would
// crash every UpdateTenant request in those environments.
func TestTenantHandler_UpdateWithNilProducer_DoesNotPanic(t *testing.T) {
	t.Parallel()
	f := setupDispatcher(t)

	tenant := mustCreateTenant(t, f.h.PoolMigrate, "tenant-events-nilprod")
	repo := adapters.NewTenantRepo(sqlc.New(f.h.PoolMigrate), f.h.PoolMigrate)
	handler := tenantpkg.NewHandler(repo, allowAuth{})
	// Deliberately NOT calling SetEventProducer.

	ctx := ctxAdmin(t, tenant)
	newDisplay := "After (no producer)"
	if _, err := handler.UpdateTenant(ctx, tenantpkg.UpdateTenantArgs{
		TenantID:        tenant,
		ExpectedVersion: 1,
		DisplayName:     &newDisplay,
	}); err != nil {
		t.Fatalf("UpdateTenant with nil producer: %v", err)
	}

	// And no rows should land in the outbox — silent skip, not silent insert.
	if rows := f.allDeliveryRows(t, tenant); len(rows) != 0 {
		t.Errorf("unexpected event_deliveries rows = %d, want 0 — "+
			"handler with nil producer should NOT insert outbox rows", len(rows))
	}
}

// Compile-time assertion: *worker.Dispatcher must satisfy
// tenantpkg.EventProducer. Without this the integration test would
// only catch the mismatch at SetEventProducer call time, but we want
// the failure to surface in `go vet`/`go build` immediately.
var _ tenantpkg.EventProducer = (*worker.Dispatcher)(nil)
