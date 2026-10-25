package worker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/health"
)

// A job interval the tick log tests run under.
const testInterval = time.Minute

// tickLogAt is a log whose clock reads *now.
func tickLogAt(now *time.Time) *TickLog {
	l := NewTickLog()
	l.started = *now
	l.now = func() time.Time { return *now }
	return l
}

// detailsOf is the jobs component's details, by group and name.
func detailsOf(c health.Check) map[string]string {
	out := map[string]string{}
	for _, d := range c.Describe(context.Background()) {
		out[d.Group+"/"+d.Name] = d.Value
	}
	return out
}

func TestJobsComponent(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	log := tickLogAt(&now)
	log.register("purger", testInterval)
	log.register("reaper", testInterval)
	c := JobsComponent(log)
	if c.Critical {
		t.Error("a stalled job takes the pod out of service")
	}

	// Before any tick: not yet, and not stalled until two intervals pass.
	if err := c.Func(context.Background()); err != nil {
		t.Errorf("fresh pod: %v", err)
	}
	if got := detailsOf(c)["purger/last run"]; got != "not yet" {
		t.Errorf("purger last run %q, want not yet", got)
	}

	log.record("purger", now, 30*time.Millisecond, nil)
	log.record("reaper", now, time.Second, errors.New(strings.Repeat("x", maxTickError+10)))
	now = now.Add(90 * time.Second)
	d := detailsOf(c)
	if d["purger/last run"] != "1m30s ago" || d["purger/took"] != "30ms" || d["purger/outcome"] != "ok" || d["purger/every"] != "1m0s" {
		t.Errorf("purger details %v", d)
	}
	if !strings.HasPrefix(d["reaper/outcome"], "failed: ") || !strings.HasSuffix(d["reaper/outcome"], "…") {
		t.Errorf("reaper outcome %q, want its error cut short", d["reaper/outcome"])
	}
	if err := c.Func(context.Background()); err != nil {
		t.Errorf("a failed tick is not a stall: %v", err)
	}

	// Past two intervals without a tick, the job has stalled.
	now = now.Add(stalledAfterIntervals * testInterval)
	err := c.Func(context.Background())
	if err == nil || !strings.Contains(err.Error(), "purger") || !strings.Contains(err.Error(), "reaper") {
		t.Errorf("stalled jobs: %v, want both named", err)
	}
}

// RunTicker records each tick it runs in Ticks, beside its metrics.
func TestRunTickerRecordsItsTicks(t *testing.T) {
	const name = "ticklog-test-job"
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = RunTicker(ctx, name, time.Millisecond, func(context.Context) error {
			cancel()
			return errors.New("boom")
		})
	}()
	<-done
	for _, j := range Ticks.Jobs() {
		if j.Name == name {
			if j.Last.IsZero() || j.Err != "boom" || j.Interval != time.Millisecond {
				t.Errorf("recorded %+v", j)
			}
			return
		}
	}
	t.Fatalf("%s not in the tick log", name)
}
