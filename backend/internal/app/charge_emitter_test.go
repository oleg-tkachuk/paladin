package app

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/worker"
)

// fakeEventDispatcher records every Dispatch call so the cross-cutting
// emitter tests can assert the produced worker.Event (and the drop guards)
// without a DB + NATS stack. Shared by charge + audit emitter tests.
type fakeEventDispatcher struct {
	calls []dispatchCall
	err   error
}

type dispatchCall struct {
	tenantID string
	evt      worker.Event
}

func (f *fakeEventDispatcher) Dispatch(_ context.Context, tenantID string, evt worker.Event) (int, error) {
	f.calls = append(f.calls, dispatchCall{tenantID: tenantID, evt: evt})
	if f.err != nil {
		return 0, f.err
	}
	return len(f.calls), nil
}

// TestChargeEmitter_EmitsChargedEvent pins the producer-wiring contract: a
// charge fans out exactly one paladin.capability.charged event carrying the
// tenant, the capabilities/<id> resource name (for subscriber routing), the
// actor, and the metering payload.
func TestChargeEmitter_EmitsChargedEvent(t *testing.T) {
	fake := &fakeEventDispatcher{}
	e := &chargeEmitter{dispatcher: fake, log: zap.NewNop()}

	tenant := uuid.New().String()
	cap := uuid.New().String()
	e.EmitCharged(context.Background(), tenant, cap, "get", "agent-1", 1.5, "USD")

	if len(fake.calls) != 1 {
		t.Fatalf("Dispatch calls = %d, want 1", len(fake.calls))
	}
	c := fake.calls[0]
	if c.tenantID != tenant {
		t.Errorf("Dispatch tenantID = %q, want %q", c.tenantID, tenant)
	}
	if c.evt.Type != "paladin.capability.charged" {
		t.Errorf("event type = %q, want paladin.capability.charged", c.evt.Type)
	}
	if c.evt.ResourceName != "capabilities/"+cap {
		t.Errorf("resource name = %q, want capabilities/%s", c.evt.ResourceName, cap)
	}
	if c.evt.TenantID != tenant {
		t.Errorf("event TenantID = %q, want %q", c.evt.TenantID, tenant)
	}
	if c.evt.ActorSubject != "agent-1" {
		t.Errorf("actor = %q, want agent-1", c.evt.ActorSubject)
	}
	if c.evt.At.IsZero() {
		t.Error("event At is zero — should be stamped")
	}
	for k, want := range map[string]any{
		"tenant_id":     tenant,
		"capability_id": cap,
		"op":            "get",
		"amount":        1.5,
		"unit_code":     "USD",
	} {
		if got := c.evt.Payload[k]; got != want {
			t.Errorf("payload[%q] = %v, want %v", k, got, want)
		}
	}
}

// TestChargeEmitter_DropsTenantless: a charge with no tenant binding has no
// fan-out target and must be dropped rather than emitting a malformed event
// with an empty tenant_id.
func TestChargeEmitter_DropsTenantless(t *testing.T) {
	for _, tenant := range []string{"", uuid.Nil.String()} {
		fake := &fakeEventDispatcher{}
		e := &chargeEmitter{dispatcher: fake, log: zap.NewNop()}
		e.EmitCharged(context.Background(), tenant, uuid.New().String(), "get", "a", 1, "USD")
		if len(fake.calls) != 0 {
			t.Errorf("tenant %q: Dispatch calls = %d, want 0 (dropped)", tenant, len(fake.calls))
		}
	}
}

// TestChargeEmitter_SwallowsDispatchError: fan-out is best-effort — a
// dispatch failure must not panic or propagate (the charge already
// committed; the caller's request returns success regardless).
func TestChargeEmitter_SwallowsDispatchError(t *testing.T) {
	fake := &fakeEventDispatcher{err: errors.New("outbox down")}
	e := &chargeEmitter{dispatcher: fake, log: zap.NewNop()}
	// Must not panic.
	e.EmitCharged(context.Background(), uuid.New().String(), uuid.New().String(), "get", "a", 1, "USD")
	if len(fake.calls) != 1 {
		t.Fatalf("Dispatch should still be attempted once; got %d", len(fake.calls))
	}
}

// TestNewChargeEmitter_NilDispatcher: events disabled (nil dispatcher) →
// nil emitter, so the charge path short-circuits with no overhead.
func TestNewChargeEmitter_NilDispatcher(t *testing.T) {
	if got := newChargeEmitter(nil, zap.NewNop()); got != nil {
		t.Fatalf("newChargeEmitter(nil) = %v, want nil", got)
	}
}
