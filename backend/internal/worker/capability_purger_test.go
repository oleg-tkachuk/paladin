package worker

import (
	"context"
	"errors"
	"testing"
	"time"

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
