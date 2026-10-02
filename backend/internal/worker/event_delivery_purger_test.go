package worker

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeDeliveryPurgeRepo struct {
	results []int64 // rows each call reports, in order; then 0
	err     error
	calls   int
	cutoffs []time.Time
	batches []int32
}

func (f *fakeDeliveryPurgeRepo) PurgeTerminalBefore(_ context.Context, cutoff time.Time, batchSize int32) (int64, error) {
	f.calls++
	f.cutoffs = append(f.cutoffs, cutoff)
	f.batches = append(f.batches, batchSize)
	if f.err != nil {
		return 0, f.err
	}
	if f.calls <= len(f.results) {
		return f.results[f.calls-1], nil
	}
	return 0, nil
}

func TestEventDeliveryPurger_DrainsUntilAShortBatch(t *testing.T) {
	cases := []struct {
		name      string
		results   []int64
		wantCalls int
	}{
		{"nothing to purge", nil, 1},
		{"one short batch", []int64{3}, 1},
		{"full batches then a short one", []int64{eventDeliveryPurgeBatch, eventDeliveryPurgeBatch, 7}, 3},
		{"a full batch then an empty one", []int64{eventDeliveryPurgeBatch}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeDeliveryPurgeRepo{results: tc.results}
			p := &EventDeliveryPurger{Repo: repo, TTL: time.Hour}
			if err := p.drain(context.Background(), time.Now()); err != nil {
				t.Fatalf("drain: %v", err)
			}
			if repo.calls != tc.wantCalls {
				t.Errorf("calls = %d, want %d", repo.calls, tc.wantCalls)
			}
			for _, b := range repo.batches {
				if b != eventDeliveryPurgeBatch {
					t.Errorf("batch size = %d, want %d — drain decides 'more left' by it", b, eventDeliveryPurgeBatch)
				}
			}
		})
	}
}

func TestEventDeliveryPurger_StopsOnError(t *testing.T) {
	boom := errors.New("boom")
	repo := &fakeDeliveryPurgeRepo{err: boom}
	p := &EventDeliveryPurger{Repo: repo, TTL: time.Hour}
	if err := p.drain(context.Background(), time.Now()); !errors.Is(err, boom) {
		t.Fatalf("drain error = %v, want %v", err, boom)
	}
	if repo.calls != 1 {
		t.Errorf("calls = %d after an error, want 1", repo.calls)
	}
}

func TestEventDeliveryPurger_StopsWhenCancelledMidBacklog(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repo := &fakeDeliveryPurgeRepo{results: []int64{eventDeliveryPurgeBatch, eventDeliveryPurgeBatch}}
	p := &EventDeliveryPurger{Repo: repo, TTL: time.Hour}
	if err := p.drain(ctx, time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("drain error = %v, want context.Canceled", err)
	}
	if repo.calls != 1 {
		t.Errorf("calls = %d, want 1: a cancelled drain must not start another batch", repo.calls)
	}
}

func TestEventDeliveryPurger_DisabledWithoutTTL(t *testing.T) {
	repo := &fakeDeliveryPurgeRepo{}
	p := &EventDeliveryPurger{Repo: repo, Interval: time.Millisecond}
	if err := p.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if repo.calls != 0 {
		t.Errorf("a purger with no TTL purged %d times", repo.calls)
	}
}

// cutoffRecorder reports the cutoff of each purge on a channel, so a test can
// wait for Run's first tick without reading shared state.
type cutoffRecorder chan time.Time

func (c cutoffRecorder) PurgeTerminalBefore(_ context.Context, cutoff time.Time, _ int32) (int64, error) {
	select {
	case c <- cutoff:
	default:
	}
	return 0, nil
}

func TestEventDeliveryPurger_CutoffIsTTLAgo(t *testing.T) {
	const ttl = 3 * time.Hour
	rec := make(cutoffRecorder, 1)
	p := &EventDeliveryPurger{Repo: rec, TTL: ttl, Interval: time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	before := time.Now().UTC()
	go func() { done <- p.Run(ctx) }()
	var got time.Time
	select {
	case got = <-rec:
	case <-time.After(5 * time.Second):
		t.Fatal("the purger never ran")
	}
	cancel()
	<-done
	if got.Before(before.Add(-ttl)) || got.After(time.Now().UTC().Add(-ttl)) {
		t.Errorf("cutoff = %s, want now - %s", got, ttl)
	}
}
