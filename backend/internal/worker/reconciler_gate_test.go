package worker

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// refusingProbe answers every delete with err.
type refusingProbe struct{ err error }

func (refusingProbe) HeadByObjectID(context.Context, uuid.UUID) (string, int64, string, string, statemachine.Placement, bool, error) {
	return "", 0, "", "", statemachine.Placement{}, false, nil
}

func (p refusingProbe) DeleteByObjectID(context.Context, uuid.UUID) error { return p.err }

// A delete the bucket gate refuses is a deferral: no failure for the tick, a
// Warn with the gate's reason, and the row left alone — the transitioner is
// nil here, so reaching the discard would panic.
func TestDiscardMismatchedDefersAGateRefusal(t *testing.T) {
	for _, refusal := range []error{bucketgate.ErrBackendReadOnly, bucketgate.ErrBackendDisabled} {
		t.Run(refusal.Error(), func(t *testing.T) {
			core, logs := observer.New(zap.DebugLevel)
			r := NewReconcilerV2(nil, refusingProbe{err: refusal}, ReconcilerV2Config{Events: NewEventRecorder(nil)}, zap.New(core))
			if err := r.discardMismatched(context.Background(), statemachine.ExpiredAnchor{ObjectID: uuid.New()}, errors.New("size")); err != nil {
				t.Fatalf("discardMismatched = %v, want nil: a deferral is not a failure", err)
			}
			entries := logs.All()
			if len(entries) != 1 || entries[0].Level != zapcore.WarnLevel {
				t.Fatalf("log = %v, want one Warn", entries)
			}
			if got := entries[0].ContextMap()["reason"]; got != refusal.Error() {
				t.Errorf("reason = %v, want %q", got, refusal.Error())
			}
		})
	}
}

// Any other delete failure is still the tick's failure.
func TestDiscardMismatchedReturnsADeleteFailure(t *testing.T) {
	boom := errors.New("backend unreachable")
	r := NewReconcilerV2(nil, refusingProbe{err: boom}, ReconcilerV2Config{}, zap.NewNop())
	if err := r.discardMismatched(context.Background(), statemachine.ExpiredAnchor{ObjectID: uuid.New()}, errors.New("size")); !errors.Is(err, boom) {
		t.Fatalf("discardMismatched = %v, want %v", err, boom)
	}
}
