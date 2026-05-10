package app

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/worker"
)

// chargeEmitter adapts *worker.Dispatcher to auth.ChargeEventEmitter.
//
// Lives here, not in the auth package, because auth must not import
// worker (would invert the layering — worker depends on
// admindomain which is a sibling of auth) and worker must not
// import auth (the dispatcher is plumbing, not a request handler).
// app/ is the wiring layer; both are fair game.
//
// Best-effort fan-out: the running totals already committed when
// EmitCharged runs, so any failure to insert an outbox row is
// LOGGED + swallowed. The customer's request returns success
// regardless. This matches the semantics of the ledger-row insert
// in capability/postgres/usage.go::Charge.
type chargeEmitter struct {
	dispatcher *worker.Dispatcher
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

// EmitCharged fans out one paladin.capability.charged event into the
// outbox. tenantID may be uuid.Nil (capability without a tenant
// binding); we drop those rather than emit a malformed event with
// an empty tenant_id. capabilityID, op, actor, amount, unitCode
// come from ChargeCapability's local view.
func (e *chargeEmitter) EmitCharged(
	ctx context.Context,
	tenantID, capabilityID, op, actor string,
	amount float64,
	unitCode string,
) {
	if tenantID == "" || tenantID == uuid.Nil.String() {
		return
	}
	queued, err := e.dispatcher.Dispatch(ctx, tenantID, worker.Event{
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
		return
	}
	if e.log != nil {
		e.log.Debug("capability.charged event queued",
			zap.String("tenant_id", tenantID),
			zap.Int("subscriptions_matched", queued),
		)
	}
}
