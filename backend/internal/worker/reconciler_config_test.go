package worker

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// A field left zero takes its default; a set one is kept. A zero batch would
// scan nothing, and a zero grace would treat every upload as overdue.
func TestNewReconcilerV2Defaults(t *testing.T) {
	r := NewReconcilerV2(nil, nil, ReconcilerV2Config{}, zap.NewNop())
	if r.cfg.PollInterval != DefaultReconcilerPollInterval ||
		r.cfg.PendingGraceTTL != DefaultReconcilerPendingGraceTTL ||
		r.cfg.BatchSize != DefaultReconcilerBatchSize {
		t.Errorf("defaults = %+v", r.cfg)
	}

	set := ReconcilerV2Config{PollInterval: time.Second, PendingGraceTTL: time.Minute, BatchSize: 7}
	if got := NewReconcilerV2(nil, nil, set, zap.NewNop()).cfg; got != set {
		t.Errorf("set config = %+v, want %+v", got, set)
	}
}

// A scan the database refuses is a failed tick: the error reaches RunTicker,
// which feeds the failing-worker alert, instead of only the log.
func TestReconcilerV2TickReturnsScanFailure(t *testing.T) {
	r := NewReconcilerV2(statemachine.New(unreachablePool(t)), nil, ReconcilerV2Config{}, zap.NewNop())
	ctx, cancel := context.WithTimeout(context.Background(), sweepDeadline)
	defer cancel()
	if err := r.tick(ctx); err == nil {
		t.Error("tick = nil against an unreachable database, want the scan failure")
	}
}
