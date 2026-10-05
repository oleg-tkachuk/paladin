package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/capability"
)

// purgeOnlyStore answers PurgeExpired; any other Store method panics through
// the nil embedded interface, which the purger must never call.
type purgeOnlyStore struct {
	capability.Store
	err error
}

func (s purgeOnlyStore) PurgeExpired(context.Context, time.Duration) (int64, error) {
	return 0, s.err
}

// fakeReplayPurger hands out the batch sizes in batches, then 0.
type fakeReplayPurger struct {
	batches []int64
	err     error
	calls   int
}

func (f *fakeReplayPurger) PurgeExpired(context.Context) (int64, error) {
	f.calls++
	if f.err != nil {
		return 0, f.err
	}
	if len(f.batches) == 0 {
		return 0, nil
	}
	n := f.batches[0]
	f.batches = f.batches[1:]
	return n, nil
}

func TestCapabilityPurgerDrainsExpiredDPoPProofIDs(t *testing.T) {
	replay := &fakeReplayPurger{batches: []int64{10, 3}}
	p := &CapabilityPurger{Store: purgeOnlyStore{}, Replay: replay}
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if replay.calls != 3 {
		t.Fatalf("replay purge called %d times, want 3 (two batches, then the empty one)", replay.calls)
	}
}

func TestCapabilityPurgerReportsAFailedDPoPPurge(t *testing.T) {
	failure := errors.New("database unreachable")
	replay := &fakeReplayPurger{err: failure}
	p := &CapabilityPurger{Store: purgeOnlyStore{}, Replay: replay}
	if err := p.tick(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("tick: err = %v, want %v", err, failure)
	}
	if replay.calls != 1 {
		t.Fatalf("replay purge called %d times after a failure, want 1", replay.calls)
	}
}

func TestCapabilityPurgerWithoutReplayCache(t *testing.T) {
	p := &CapabilityPurger{Store: purgeOnlyStore{}}
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
}

// batches hands out the sizes in turn, then 0; err fails every call.
type batches struct {
	sizes []int64
	err   error
	calls int
}

func (b *batches) next() (int64, error) {
	b.calls++
	if b.err != nil {
		return 0, b.err
	}
	if len(b.sizes) == 0 {
		return 0, nil
	}
	n := b.sizes[0]
	b.sizes = b.sizes[1:]
	return n, nil
}

// sweepingUsage answers the two sweeps the purger makes; any other method
// panics through the nil embedded interface.
type sweepingUsage struct {
	capability.UsageStore[pgx.Tx]
	release, orphans batches
}

func (u *sweepingUsage) ReleaseExpired(context.Context) (int64, error) { return u.release.next() }
func (u *sweepingUsage) PurgeOrphans(context.Context) (int64, error)   { return u.orphans.next() }

// Both usage sweeps drain in batches until one comes back empty.
func TestCapabilityPurgerDrainsBothUsageSweeps(t *testing.T) {
	usage := &sweepingUsage{
		release: batches{sizes: []int64{5, 2}},
		orphans: batches{sizes: []int64{7}},
	}
	p := &CapabilityPurger{Store: purgeOnlyStore{}, Usage: usage}
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if usage.release.calls != 3 || usage.orphans.calls != 2 {
		t.Errorf("release calls = %d (want 3), orphan calls = %d (want 2)", usage.release.calls, usage.orphans.calls)
	}
}

// A failed sweep stops its own loop and is reported, and the other sweep
// still runs.
func TestCapabilityPurgerReportsAFailedUsageSweep(t *testing.T) {
	failure := errors.New("database unreachable")
	for name, usage := range map[string]*sweepingUsage{
		"release": {release: batches{err: failure}, orphans: batches{sizes: []int64{1}}},
		"orphans": {release: batches{sizes: []int64{1}}, orphans: batches{err: failure}},
	} {
		p := &CapabilityPurger{Store: purgeOnlyStore{}, Usage: usage}
		if err := p.tick(context.Background()); !errors.Is(err, failure) {
			t.Errorf("%s: err = %v, want the sweep's", name, err)
		}
		if usage.release.calls == 0 || usage.orphans.calls == 0 {
			t.Errorf("%s: a failed sweep stopped the other one", name)
		}
		if usage.release.calls > 2 || usage.orphans.calls > 2 {
			t.Errorf("%s: a failed sweep kept looping: %d / %d calls", name, usage.release.calls, usage.orphans.calls)
		}
	}
}

// countingStore reports n purged revocations.
type countingStore struct {
	capability.Store
	n int64
}

func (s countingStore) PurgeExpired(context.Context, time.Duration) (int64, error) { return s.n, nil }

// A pass that purged nothing logs nothing; one that purged rows says how many.
func TestCapabilityPurgerLogsOnlyWhatItPurged(t *testing.T) {
	const purgedLog = "purged expired capability revocations"
	for n, want := range map[int64]int{0: 0, 3: 1} {
		core, logs := observer.New(zap.InfoLevel)
		p := &CapabilityPurger{Store: countingStore{n: n}, Logger: zap.New(core)}
		if err := p.tick(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := logs.FilterMessage(purgedLog).Len(); got != want {
			t.Errorf("%d purged: %d log lines, want %d", n, got, want)
		}
	}
}

// Unset, the retention defaults before the loop starts. (Interval's own
// `<= 0` guard is equivalent to `< 0`: RunTicker refuses a zero interval the
// same way.)
func TestCapabilityPurgerDefaultsItsRetention(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &CapabilityPurger{Store: purgeOnlyStore{}, Interval: time.Hour}
	_ = p.Run(ctx)
	if p.ExpiredFor != DefaultCapabilityExpiredFor {
		t.Errorf("ExpiredFor = %v, want %v", p.ExpiredFor, DefaultCapabilityExpiredFor)
	}
}
