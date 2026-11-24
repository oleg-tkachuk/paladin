package worker

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// A reconciler built with no event producer says so once, at Warn: its
// promotes and recorded writes then reach no subscriber. One wired with a
// producer is silent and announces.
func TestNewReconcilerV2WarnsWithoutAnEventProducer(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	if r := NewReconcilerV2(nil, refusingProbe{}, ReconcilerV2Config{}, zap.New(core)); r.Announces() {
		t.Error("a reconciler with no producer reports that it announces")
	}
	if entries := logs.All(); len(entries) != 1 || entries[0].Level != zapcore.WarnLevel {
		t.Fatalf("log = %v, want one Warn", entries)
	}

	core, logs = observer.New(zap.DebugLevel)
	if r := NewReconcilerV2(nil, refusingProbe{}, ReconcilerV2Config{Events: NewEventRecorder(nil)}, zap.New(core)); !r.Announces() {
		t.Error("a reconciler with a producer reports that it does not announce")
	}
	if entries := logs.All(); len(entries) != 0 {
		t.Errorf("log = %v, want nothing", entries)
	}
}
