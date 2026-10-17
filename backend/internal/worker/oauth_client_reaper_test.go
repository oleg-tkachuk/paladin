package worker

import (
	"context"
	"errors"
	"testing"
	"time"
)

type reapCall struct {
	createdBefore, now time.Time
	limit              int
}

// fakeClientReaper answers each call with the next count of counts, then 0.
type fakeClientReaper struct {
	calls  chan reapCall
	counts []int64
	err    error
}

func (f *fakeClientReaper) ReapIdleClients(_ context.Context, createdBefore, now time.Time, limit int) (int64, error) {
	record(f.calls, reapCall{createdBefore: createdBefore, now: now, limit: limit})
	if f.err != nil {
		return 0, f.err
	}
	if len(f.counts) == 0 {
		return 0, nil
	}
	n := f.counts[0]
	f.counts = f.counts[1:]
	return n, nil
}

const reaperCalls = 8

func TestOAuthClientReaper_CutoffIsMaxAgeBeforeNow(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	const maxAge = 7 * 24 * time.Hour
	store := &fakeClientReaper{calls: make(chan reapCall, reaperCalls)}
	r := &OAuthClientReaper{Store: store, MaxAge: maxAge, Now: func() time.Time { return now }}
	if err := r.reap(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := <-store.calls
	if !got.createdBefore.Equal(now.Add(-maxAge)) || !got.now.Equal(now) || got.limit != OAuthClientReapBatch {
		t.Errorf("call = %+v, want cutoff %s, now %s, limit %d", got, now.Add(-maxAge), now, OAuthClientReapBatch)
	}
}

// A full batch means there may be more; a short one means there are none.
func TestOAuthClientReaper_RunsBatchesUntilOneIsShort(t *testing.T) {
	store := &fakeClientReaper{calls: make(chan reapCall, reaperCalls),
		counts: []int64{OAuthClientReapBatch, OAuthClientReapBatch, 1}}
	r := &OAuthClientReaper{Store: store, MaxAge: time.Hour}
	if err := r.reap(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(store.calls); n != 3 {
		t.Errorf("batches = %d, want 3", n)
	}
}

func TestOAuthClientReaper_ReportsAFailedBatch(t *testing.T) {
	boom := errors.New("pool closed")
	store := &fakeClientReaper{calls: make(chan reapCall, reaperCalls), err: boom}
	r := &OAuthClientReaper{Store: store, MaxAge: time.Hour}
	if err := r.reap(context.Background()); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the store's", err)
	}
	if n := len(store.calls); n != 1 {
		t.Errorf("batches after a failure = %d, want 1", n)
	}
}

func TestOAuthClientReaper_TicksAtItsInterval(t *testing.T) {
	store := &fakeClientReaper{calls: make(chan reapCall, reaperCalls)}
	r := &OAuthClientReaper{Store: store, Interval: time.Millisecond, MaxAge: time.Hour}
	got := runUntilCalled(t, r.Run, store.calls)
	if got.limit != OAuthClientReapBatch {
		t.Errorf("limit = %d", got.limit)
	}
}

func TestOAuthClientReaper_DisabledByInterval(t *testing.T) {
	for name, interval := range map[string]time.Duration{"zero": 0, "negative": -time.Second} {
		t.Run(name, func(t *testing.T) {
			store := &fakeClientReaper{calls: make(chan reapCall, reaperCalls)}
			r := &OAuthClientReaper{Store: store, Interval: interval, MaxAge: time.Hour}
			if err := r.Run(context.Background()); err != nil {
				t.Fatalf("a disabled reaper must return nil, got %v", err)
			}
			if len(store.calls) != 0 {
				t.Error("a disabled reaper reached its store")
			}
		})
	}
}
