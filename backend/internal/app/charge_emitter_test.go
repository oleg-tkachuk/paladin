package app

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// fakeEventDispatcher records every Dispatch/DispatchTx call so the
// cross-cutting emitter tests can assert the produced worker.Event (and the
// drop guards) without a DB + NATS stack. Shared by charge + audit emitter
// tests. The charge + audit emitters use the transactional DispatchTx path;
// the fake ignores the tx (nil in these unit tests) and records into the same
// slice so assertions read uniformly.
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

func (f *fakeEventDispatcher) DispatchTx(_ context.Context, _ pgx.Tx, tenantID string, evt worker.Event) (int, error) {
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
	if err := e.EmitChargedTx(context.Background(), nil, tenant, cap, "get", "agent-1", limes.MustParseAmount("1.5"), "USD"); err != nil {
		t.Fatalf("EmitChargedTx: %v", err)
	}

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
		"amount":        map[string]any{"currency_code": "USD", "units": "1", "nanos": int32(500_000_000)},
	} {
		if got := c.evt.Payload[k]; !reflect.DeepEqual(got, want) {
			t.Errorf("payload[%q] = %v, want %v", k, got, want)
		}
	}
	for _, k := range []string{"amount_micros", "unit_code"} {
		if got, ok := c.evt.Payload[k]; ok {
			t.Errorf("payload[%q] = %v, want the key gone", k, got)
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
		if err := e.EmitChargedTx(context.Background(), nil, tenant, uuid.New().String(), "get", "a", limes.NanosPerUnit, "USD"); err != nil {
			t.Fatalf("tenant %q: EmitChargedTx: %v", tenant, err)
		}
		if len(fake.calls) != 0 {
			t.Errorf("tenant %q: Dispatch calls = %d, want 0 (dropped)", tenant, len(fake.calls))
		}
	}
}

// TestChargeEmitter_PropagatesDispatchError: the transactional fan-out is NOT
// best-effort — a DispatchTx failure must propagate so UsageStore.Charge rolls
// the whole charge back (no committed-charge-without-event window).
func TestChargeEmitter_PropagatesDispatchError(t *testing.T) {
	fake := &fakeEventDispatcher{err: errors.New("outbox down")}
	e := &chargeEmitter{dispatcher: fake, log: zap.NewNop()}
	err := e.EmitChargedTx(context.Background(), nil, uuid.New().String(), uuid.New().String(), "get", "a", limes.NanosPerUnit, "USD")
	if err == nil {
		t.Fatal("EmitChargedTx returned nil, want the dispatch error to propagate")
	}
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
