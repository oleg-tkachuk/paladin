package worker

import (
	"testing"
	"time"

	"go.uber.org/zap"
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
