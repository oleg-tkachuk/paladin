// Package pgsync counts rate-limit windows in this process and shares them
// through Postgres once per sync interval (ADR-0046, decision 4). A request
// is decided against the totals the last sync read plus what this process
// counted since, so Postgres takes one round trip per replica per interval
// instead of a write per request. Replicas do not see each other's requests
// for up to one interval, which bounds the overshoot to
// (replicas − 1) × rate × interval.
package pgsync

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/ratecount"
)

// finalSyncTimeout bounds the sync Run makes on its way out, so a process
// that stops hands its last counts on without waiting on a database that
// stopped answering.
const finalSyncTimeout = 2 * time.Second

// Delta is what one process counted in one bucket since its last sync.
type Delta struct {
	Key   string
	Start time.Time
	Count int64
}

// Total is one bucket's count across every process, as of a sync.
type Total struct {
	Key   string
	Start time.Time
	Count int64
}

// Store is where processes share their counts.
type Store interface {
	// Exchange adds deltas to the shared buckets and returns the totals of
	// every bucket of keys that started at or after since.
	Exchange(ctx context.Context, deltas []Delta, keys []string, since time.Time) ([]Total, error)
}

// Counter is a ratecount.Counter that syncs through a Store.
type Counter struct {
	store    Store
	interval time.Duration
	fallback *ratecount.Fallback
	log      *zap.Logger
	now      func() time.Time

	mu   sync.Mutex
	keys map[string]*window
}

// window is one key as this process knows it, by bucket index.
type window struct {
	// shared is what the last sync read: every process's counts.
	shared map[int64]int64
	// inflight is what the sync under way is sending; the totals it reads
	// back will include it.
	inflight map[int64]int64
	// pending is what this process counted since the sync under way began.
	pending map[int64]int64
	// seen is the last bucket a call touched, a read included; a key idle
	// for a whole window stops being synced.
	seen int64
}

func newWindow() *window {
	return &window{shared: map[int64]int64{}, inflight: map[int64]int64{}, pending: map[int64]int64{}}
}

// count is every request this process knows of in bucket index.
func (w *window) count(index int64) int64 {
	return w.shared[index] + w.inflight[index] + w.pending[index]
}

// New counts through store, syncing every interval once Run is started.
func New(store Store, interval time.Duration, log *zap.Logger) *Counter {
	return &Counter{
		store:    store,
		interval: interval,
		fallback: ratecount.NewFallback("postgres", log),
		log:      log,
		now:      time.Now,
		keys:     map[string]*window{},
	}
}

type mode int

const (
	readOnly mode = iota
	bumpAlways
	bumpIfFits
)

func (c *Counter) decide(ctx context.Context, key string, m mode, capacity int) ratecount.Counts {
	c.mu.Lock()
	defer c.mu.Unlock()
	index, elapsed := ratecount.BucketIndex(c.now())
	w, ok := c.keys[key]
	if !ok {
		w = newWindow()
		c.keys[key] = w
	}
	// A read marks the key live too, so a window someone watches is synced
	// and shows every replica's counts.
	w.seen = index
	cur, prev := w.count(index), w.count(index-1)
	admitted := false
	switch m {
	case bumpAlways:
		admitted = true
	case bumpIfFits:
		admitted = ratecount.Fits(cur, prev, elapsed, capacity)
	}
	if admitted {
		w.pending[index]++
		cur++
	}
	if m != readOnly && c.fallback.Degraded() {
		c.fallback.CountLocal(ctx)
	}
	return ratecount.Counts{
		Current: cur, Previous: prev, BucketStart: ratecount.BucketStart(index),
		Elapsed: elapsed, Admitted: admitted,
	}
}

// Bump implements ratecount.Counter. It never fails: the decision is this
// process's, and a failed sync only leaves it on its own counts longer.
func (c *Counter) Bump(ctx context.Context, key string) (ratecount.Counts, error) {
	return c.decide(ctx, key, bumpAlways, 0), nil
}

// BumpIfFits implements ratecount.Counter.
func (c *Counter) BumpIfFits(ctx context.Context, key string, capacity int) (ratecount.Counts, error) {
	return c.decide(ctx, key, bumpIfFits, capacity), nil
}

// Read implements ratecount.Counter.
func (c *Counter) Read(ctx context.Context, key string) (ratecount.Counts, error) {
	return c.decide(ctx, key, readOnly, 0), nil
}

// take moves every live key's pending counts in flight and returns them with
// the keys to read back. A key idle for a whole window is forgotten: both its
// buckets have left the window, so nothing it holds is weighed any more.
func (c *Counter) take(index int64) ([]Delta, []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var deltas []Delta
	var keys []string
	for key, w := range c.keys {
		if w.seen < index-1 {
			delete(c.keys, key)
			continue
		}
		keys = append(keys, key)
		for b, n := range w.pending {
			if b >= index-1 && n > 0 {
				deltas = append(deltas, Delta{Key: key, Start: ratecount.BucketStart(b), Count: n})
				w.inflight[b] += n
			}
		}
		clear(w.pending)
	}
	return deltas, keys
}

// land settles the sync of keys: on success the totals replace what each key
// had shared and in flight; on failure what was in flight is pending again,
// to go with the next sync.
func (c *Counter) land(index int64, keys []string, totals []Total, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	byKey := map[string]map[int64]int64{}
	for _, t := range totals {
		b, _ := ratecount.BucketIndex(t.Start)
		if byKey[t.Key] == nil {
			byKey[t.Key] = map[int64]int64{}
		}
		byKey[t.Key][b] = t.Count
	}
	for _, key := range keys {
		w, ok := c.keys[key]
		if !ok {
			continue
		}
		if err != nil {
			for b, n := range w.inflight {
				if b >= index-1 {
					w.pending[b] += n
				}
			}
		} else {
			w.shared = byKey[key]
			if w.shared == nil {
				w.shared = map[int64]int64{}
			}
		}
		clear(w.inflight)
	}
}

// Sync sends what this process counted and reads every live key's totals
// back. A failure keeps the counts for the next sync.
func (c *Counter) Sync(ctx context.Context) error {
	index, _ := ratecount.BucketIndex(c.now())
	deltas, keys := c.take(index)
	if len(keys) == 0 {
		return nil
	}
	totals, err := c.store.Exchange(ctx, deltas, keys, ratecount.BucketStart(index-1))
	c.land(index, keys, totals, err)
	c.fallback.Settle(err)
	if err != nil {
		c.log.Debug("rate limit sync failed; counts kept for the next one",
			zap.Int("keys", len(keys)), zap.Int("deltas", len(deltas)), zap.Error(err))
		return err
	}
	return nil
}

// Run syncs every interval until ctx is done, then once more so the last
// counts are not lost with the process.
func (c *Counter) Run(ctx context.Context) {
	c.log.Info("rate limit sync started", zap.Duration("interval", c.interval))
	t := time.NewTicker(c.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			final, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalSyncTimeout)
			err := c.Sync(final)
			cancel()
			c.log.Info("rate limit sync stopped", zap.Bool("final_sync_ok", err == nil))
			return
		case <-t.C:
			tick, cancel := context.WithTimeout(ctx, c.interval)
			_ = c.Sync(tick) // a failure is logged and kept for the next tick
			cancel()
		}
	}
}
