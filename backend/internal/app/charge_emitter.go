package app

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// eventDispatcher is the producer-side seam the cross-cutting emitters
// (charge, audit) depend on. *worker.Dispatcher satisfies it in production;
// unit tests substitute a fake to assert the emitted event + the drop guards
// without standing up a DB + NATS stack (the dispatcher → outbox → NATS path
// itself is covered by tests/integration/lifecycle_events_test.go).
//
// DispatchTx is the transactional variant: the outbox rows land on the
// caller's tx so the fan-out is atomic with the source write (ADR-0003).
// The charge + audit emitters use it exclusively — the pool-backed
// Dispatch is only for producers that own no transaction.
type eventDispatcher interface {
	Dispatch(ctx context.Context, tenantID string, evt worker.Event) (int, error)
	DispatchTx(ctx context.Context, tx pgx.Tx, tenantID string, evt worker.Event) (int, error)
}

// chargeEmitter adapts *worker.Dispatcher to auth.ChargeEventEmitter.
//
// Lives here, not in the auth package, because auth must not import
// worker (would invert the layering — worker depends on
// admindomain which is a sibling of auth) and worker must not
// import auth (the dispatcher is plumbing, not a request handler).
// app/ is the wiring layer; both are fair game.
//
// Transactional fan-out: EmitChargedTx enqueues the outbox rows on the
// charge's own transaction (dispatcher.DispatchTx), so the event is
// atomic with the spend — a crash can never leave a committed charge
// without its event, and a fan-out failure rolls the charge back
// (ADR-0003, no dual-write window).
type chargeEmitter struct {
	dispatcher eventDispatcher
	log        *zap.Logger
}

// newChargeEmitter wires the optional event emitter. Returns nil
// when events are disabled — callers stamp nil on ctx so
// chargeEventEmitterFromContext returns nil and ChargeCapability
// short-circuits with no overhead.
func newChargeEmitter(d *worker.Dispatcher, l *zap.Logger) auth.ChargeEventEmitter {
	if d == nil {
		return nil
	}
	return &chargeEmitter{dispatcher: d, log: l}
}

// EmitChargedTx fans out one paladin.capability.charged event into the
// outbox on the charge's transaction `tx`, so the event is atomic with
// the spend. tenantID may be uuid.Nil (capability without a tenant
// binding); we drop those rather than emit a malformed event with an
// empty tenant_id (a nil-tenant charge writes no ledger row either, so
// there's nothing to mirror). capabilityID, op, actor, amount, unitCode
// come from ChargeCapability's local view.
//
// A dispatch error propagates to the caller (UsageStore.Charge), which
// rolls the whole charge back — no committed-charge-without-event window.
func (e *chargeEmitter) EmitChargedTx(
	ctx context.Context,
	tx pgx.Tx,
	tenantID, capabilityID, op, actor string,
	amount float64,
	unitCode string,
) error {
	if tenantID == "" || tenantID == uuid.Nil.String() {
		return nil
	}
	queued, err := e.dispatcher.DispatchTx(ctx, tx, tenantID, worker.Event{
		Type:         "paladin.capability.charged",
		At:           time.Now().UTC(),
		TenantID:     tenantID,
		ResourceName: "capabilities/" + capabilityID,
		ActorSubject: actor,
		Payload: map[string]any{
			"tenant_id":     tenantID,
			"capability_id": capabilityID,
			"op":            op,
			"amount":        amount,
			"unit_code":     unitCode,
		},
	})
	if err != nil {
		if e.log != nil {
			e.log.Warn("capability.charged event fan-out failed",
				zap.String("tenant_id", tenantID),
				zap.String("capability_id", capabilityID),
				zap.Error(err),
			)
		}
		return err
	}
	if e.log != nil {
		e.log.Debug("capability.charged event queued",
			zap.String("tenant_id", tenantID),
			zap.Int("subscriptions_matched", queued),
		)
	}
	return nil
}
