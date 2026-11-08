package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/eventtype"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// fakeEventDispatcher records every Dispatch/RecordTx call so the
// cross-cutting emitter tests can assert the produced worker.Event (and the
// drop guards) without a DB + NATS stack. Shared by charge + audit emitter
// tests. The charge + audit emitters use the transactional RecordTx path;
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

func (f *fakeEventDispatcher) RecordTx(_ context.Context, _ pgx.Tx, tenantID string, evt worker.Event) error {
	f.calls = append(f.calls, dispatchCall{tenantID: tenantID, evt: evt})
	if f.err != nil {
		return f.err
	}
	return nil
}

// emitFunc is one of the emitter's two entry points.
type emitFunc func(*chargeEmitter, context.Context, pgx.Tx, auth.ChargeEvent) error

// chargeEmits names each entry point by the event type it must produce.
var chargeEmits = map[string]emitFunc{
	eventtype.CapabilityCharged:  (*chargeEmitter).EmitChargedTx,
	eventtype.CapabilityRefunded: (*chargeEmitter).EmitRefundedTx,
}

// Each entry point fans out exactly one event of its own type, carrying the
// tenant, the capabilities/<id> resource name (for subscriber routing), the
// actor, and the payload: the charge it is about, the op and the amount.
func TestChargeEmitter_EmitsEventOfItsType(t *testing.T) {
	for wantType, emit := range chargeEmits {
		t.Run(wantType, func(t *testing.T) {
			fake := &fakeEventDispatcher{}
			e := &chargeEmitter{dispatcher: fake, log: zap.NewNop()}
			ev := auth.ChargeEvent{
				TenantID:     uuid.New(),
				CapabilityID: uuid.New(),
				ChargeID:     uuid.New(),
				Op:           "get",
				Actor:        "agent-1",
				Amount:       limes.MustParseAmount("1.5"),
				UnitCode:     "USD",
			}
			if err := emit(e, context.Background(), nil, ev); err != nil {
				t.Fatalf("emit: %v", err)
			}

			if len(fake.calls) != 1 {
				t.Fatalf("Dispatch calls = %d, want 1", len(fake.calls))
			}
			c := fake.calls[0]
			tenant := ev.TenantID.String()
			if c.tenantID != tenant {
				t.Errorf("Dispatch tenantID = %q, want %q", c.tenantID, tenant)
			}
			if c.evt.Type != wantType {
				t.Errorf("event type = %q, want %s", c.evt.Type, wantType)
			}
			if want := "capabilities/" + ev.CapabilityID.String(); c.evt.ResourceName != want {
				t.Errorf("resource name = %q, want %s", c.evt.ResourceName, want)
			}
			if c.evt.TenantID != tenant {
				t.Errorf("event TenantID = %q, want %q", c.evt.TenantID, tenant)
			}
			if c.evt.ActorSubject != ev.Actor {
				t.Errorf("actor = %q, want %s", c.evt.ActorSubject, ev.Actor)
			}
			if c.evt.At.IsZero() {
				t.Error("event At is zero — should be stamped")
			}
			for k, want := range map[string]any{
				"tenant_id":     tenant,
				"capability_id": ev.CapabilityID.String(),
				"charge_id":     ev.ChargeID.String(),
				"op":            ev.Op,
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
		})
	}
}

// A charge or refund with no tenant binding has no fan-out target and is
// dropped rather than emitted with an empty tenant_id.
func TestChargeEmitter_DropsTenantless(t *testing.T) {
	for typ, emit := range chargeEmits {
		fake := &fakeEventDispatcher{}
		e := &chargeEmitter{dispatcher: fake, log: zap.NewNop()}
		ev := auth.ChargeEvent{CapabilityID: uuid.New(), ChargeID: uuid.New(), Amount: limes.NanosPerUnit, UnitCode: "USD"}
		if err := emit(e, context.Background(), nil, ev); err != nil {
			t.Fatalf("%s: emit: %v", typ, err)
		}
		if len(fake.calls) != 0 {
			t.Errorf("%s: Dispatch calls = %d, want 0 (dropped)", typ, len(fake.calls))
		}
	}
}

// The transactional fan-out is NOT best-effort — a RecordTx failure
// propagates so the store rolls the whole charge or refund back (no
// committed-charge-without-event window).
func TestChargeEmitter_PropagatesDispatchError(t *testing.T) {
	for typ, emit := range chargeEmits {
		outboxDown := errors.New("outbox down")
		fake := &fakeEventDispatcher{err: outboxDown}
		e := &chargeEmitter{dispatcher: fake, log: zap.NewNop()}
		ev := auth.ChargeEvent{TenantID: uuid.New(), CapabilityID: uuid.New(), ChargeID: uuid.New(), Amount: limes.NanosPerUnit, UnitCode: "USD"}
		if err := emit(e, context.Background(), nil, ev); !errors.Is(err, outboxDown) {
			t.Fatalf("%s: emit = %v, want the dispatch error", typ, err)
		}
		if len(fake.calls) != 1 {
			t.Fatalf("%s: Dispatch should still be attempted once; got %d", typ, len(fake.calls))
		}
	}
}

// TestNewChargeEmitter_NilDispatcher: events disabled (nil dispatcher) →
// nil emitter, so the charge path short-circuits with no overhead.
func TestNewChargeEmitter_NilDispatcher(t *testing.T) {
	if got := NewChargeEmitter(nil, zap.NewNop()); got != nil {
		t.Fatalf("NewChargeEmitter(nil) = %v, want nil", got)
	}
}
