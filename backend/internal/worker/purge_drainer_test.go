package worker

import (
	"context"
	"errors"
	"testing"
	"time"
)

// PurgeDrainer normalises its own zero fields at the top of Run, before the
// ticker starts. A configured zero is never a usable value here: a zero batch
// size claims no rows, so the byte debt this worker exists to settle is never
// touched, nothing errors, and the worker looks alive while doing nothing.
//
// Observable without a database: Run writes the defaults onto the struct
// before handing off to RunTicker, and a cancelled context leaves that loop
// before the first tick — so Sweep never runs and the nil Pool is never
// dereferenced.
func TestPurgeDrainer_NormalisesZeroFieldsBeforeTicking(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	w := &PurgeDrainer{Interval: time.Minute} // BatchSize and MaxBackoff zero
	if err := w.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}

	if w.BatchSize != 100 {
		t.Errorf("BatchSize = %d, want the default 100 — a zero batch claims no "+
			"rows, so the debt is never settled and nothing reports it",
			w.BatchSize)
	}
	if w.MaxBackoff != time.Hour {
		t.Errorf("MaxBackoff = %v, want the default 1h — a zero cap retries a "+
			"failing row with no delay between attempts", w.MaxBackoff)
	}
}

// A zero Interval disables the drainer outright, which is a deliberate
// deployment choice (see the comment on Run). It must not be confused with
// the zero fields above, which mean "unset, use the default".
func TestPurgeDrainer_ZeroIntervalDisables(t *testing.T) {
	w := &PurgeDrainer{}
	if err := w.Run(context.Background()); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if w.BatchSize != 0 {
		t.Errorf("a disabled drainer normalised its fields (BatchSize=%d); "+
			"nothing ran, so nothing should have been written", w.BatchSize)
	}
}
