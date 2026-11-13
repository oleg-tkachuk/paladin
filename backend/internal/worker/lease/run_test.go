package lease

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const (
	// runTestLease is the lease the fake claims, so each line can be
	// checked for naming it.
	runTestLease = "reaper.test"
	// refusedClaims is how many claims in a row the fake database refuses.
	refusedClaims = 2
	runTestPoll   = time.Millisecond
	runTestWait   = 2 * time.Second
)

// A claim the database refuses names its lease and how many in a row have
// failed, and the first claim it answers again says the outage ended and how
// many it lasted: with a dozen leases backing off at once, a line without the
// name says nothing about which worker stopped.
func TestRunLogsAClaimOutageFromStartToEnd(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	l := &Lease{cfg: Config{
		Name: runTestLease, HolderID: uuid.New(), Logger: zap.New(core),
		TTL: time.Second, RenewInterval: time.Second / 3, PollInterval: runTestPoll,
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	claim := func(context.Context) (int64, time.Time, bool, error) {
		calls++
		if calls <= refusedClaims {
			return 0, time.Time{}, false, errors.New("canceling statement due to lock timeout")
		}
		// Another holder leads; the outage is over all the same.
		cancel()
		return 0, time.Time{}, false, nil
	}
	done := make(chan error, 1)
	go func() {
		done <- l.run(ctx, func(context.Context, int64) error { t.Error("ran work without the lease"); return nil }, claim)
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("run = %v, want the cancellation", err)
		}
	case <-time.After(runTestWait):
		t.Fatal("run did not return")
	}

	failures := logs.FilterMessage("claim failed; backing off").AllUntimed()
	if len(failures) != refusedClaims {
		t.Fatalf("%d claim failures logged, want %d", len(failures), refusedClaims)
	}
	for i, e := range failures {
		fields := e.ContextMap()
		if fields["name"] != runTestLease || fields["consecutive"] != int64(i+1) || e.Level != zapcore.WarnLevel {
			t.Errorf("failure %d logged %v at %v, want the lease name and consecutive=%d at WARN", i, fields, e.Level, i+1)
		}
	}
	recovered := logs.FilterMessage("claim answered again").AllUntimed()
	if len(recovered) != 1 {
		t.Fatalf("%d recovery lines, want 1", len(recovered))
	}
	if f := recovered[0].ContextMap(); f["name"] != runTestLease || f["failed_claims"] != int64(refusedClaims) || f["leader"] != false {
		t.Errorf("recovery logged %v, want the lease, %d failed claims and leader=false", f, refusedClaims)
	}
}

// A claim that never failed logs no recovery: the line marks an outage's end
// and nothing else.
func TestRunLogsNoRecoveryWithoutAnOutage(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	l := &Lease{cfg: Config{
		Name: runTestLease, HolderID: uuid.New(), Logger: zap.New(core),
		TTL: time.Second, RenewInterval: time.Second / 3, PollInterval: runTestPoll,
	}}
	ctx, cancel := context.WithCancel(context.Background())
	claim := func(context.Context) (int64, time.Time, bool, error) {
		cancel()
		return 0, time.Time{}, false, nil
	}
	if err := l.run(ctx, func(context.Context, int64) error { return nil }, claim); !errors.Is(err, context.Canceled) {
		t.Fatalf("run = %v, want the cancellation", err)
	}
	if n := logs.FilterMessage("claim answered again").Len(); n != 0 {
		t.Errorf("%d recovery lines with no outage", n)
	}
}
