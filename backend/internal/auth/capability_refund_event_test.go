package auth

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
)

// countingEmitter counts the events it is asked to enqueue.
type countingEmitter struct{ charged, refunded int }

func (e *countingEmitter) EmitChargedTx(context.Context, pgx.Tx, ChargeEvent) error {
	e.charged++
	return nil
}

func (e *countingEmitter) EmitRefundedTx(context.Context, pgx.Tx, ChargeEvent) error {
	e.refunded++
	return nil
}

// A refund with charge events on runs its event on the store's transaction.
// A store that hands it none (memstore) has nowhere to enqueue it, so the
// refund is refused and returns nothing, rather than committing without the
// event a subscriber summing charges needs; with events off, the same refund
// goes through.
func TestRefundLastCharge_RefusesARefundItCannotEmit(t *testing.T) {
	const refundAmount limes.Nanos = limes.NanosPerUnit / 2
	m, usage := newMeteredMinter(t)
	c, _ := m.Mint(t, limes.Capability{
		Subject: limes.Principal{TenantID: uuid.New(), Subject: "agent:a"},
		Caveats: limes.Caveats{
			Requests: limes.RequestCeiling{Unlimited: true},
			Budget:   limes.BudgetCeiling{Unlimited: true, UnitCode: captest.Unit},
		},
	})
	ctx := withLastOpHolder(WithChargeStore(WithCapability(t.Context(), c), usage))
	if err := ChargeCapability(ctx, refundAmount); err != nil {
		t.Fatalf("ChargeCapability: %v", err)
	}

	emitter := &countingEmitter{}
	err := RefundLastCharge(WithChargeEventEmitter(ctx, emitter), refundAmount)
	if !errors.Is(err, errRefundWithoutTx) {
		t.Fatalf("RefundLastCharge with events on = %v, want errRefundWithoutTx", err)
	}
	if emitter.refunded != 0 {
		t.Errorf("refunded events = %d, want 0 without a transaction", emitter.refunded)
	}

	// The whole charge is still there to refund: the refused one returned
	// nothing, or this would exceed what is left.
	if err := RefundLastCharge(ctx, refundAmount); err != nil {
		t.Fatalf("RefundLastCharge with events off: %v", err)
	}
}
