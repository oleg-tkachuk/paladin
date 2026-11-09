package ratecount

import (
	"context"
	"sync"
	"time"
)

// pruneEvery is how many calls pass between sweeps of keys whose buckets
// have both rolled out of the window. A sweep walks every key, so it runs
// once per this many calls rather than on each.
const pruneEvery = 1024

// Local counts windows in this process alone. It is the fallback a shared
// store degrades to, and what each replica counts in between Postgres syncs.
// Its windows sit on the process clock.
type Local struct {
	mu    sync.Mutex
	now   func() time.Time
	keys  map[string]*localWindow
	calls int
}

// localWindow is one key's two buckets; index numbers the current one.
type localWindow struct {
	index     int64
	cur, prev int64
}

// NewLocal returns an empty in-process counter on the wall clock.
func NewLocal() *Local { return newLocal(time.Now) }

func newLocal(now func() time.Time) *Local {
	return &Local{now: now, keys: map[string]*localWindow{}}
}

// BucketIndex numbers the bucket t falls in, and how far into it t is.
func BucketIndex(t time.Time) (int64, time.Duration) {
	us := t.UnixMicro()
	width := Window.Microseconds()
	return us / width, time.Duration(us%width) * time.Microsecond
}

// roll moves w to bucket index: the current bucket becomes the previous one
// when index is the next bucket, and both empty when more than one passed.
func (w *localWindow) roll(index int64) {
	switch index {
	case w.index:
	case w.index + 1:
		w.prev, w.cur = w.cur, 0
	default:
		w.prev, w.cur = 0, 0
	}
	w.index = index
}

// window returns key's window rolled to now, creating it when absent.
func (l *Local) window(key string, index int64) *localWindow {
	w, ok := l.keys[key]
	if !ok {
		w = &localWindow{index: index}
		l.keys[key] = w
	}
	w.roll(index)
	l.calls++
	if l.calls%pruneEvery == 0 {
		l.prune(index)
	}
	return w
}

// prune drops every key whose buckets are both older than the window.
func (l *Local) prune(index int64) {
	for k, w := range l.keys {
		if w.index < index-1 {
			delete(l.keys, k)
		}
	}
}

func (l *Local) decide(key string, capacity int, mode bumpMode) Counts {
	l.mu.Lock()
	defer l.mu.Unlock()
	index, elapsed := BucketIndex(l.now())
	w := l.window(key, index)
	c := Counts{Previous: w.prev, BucketStart: BucketStart(index), Elapsed: elapsed}
	switch mode {
	case bumpAlways:
		w.cur++
		c.Admitted = true
	case bumpIfFits:
		if Fits(w.cur, w.prev, elapsed, capacity) {
			w.cur++
			c.Admitted = true
		}
	}
	c.Current = w.cur
	return c
}

// Bump implements Counter.
func (l *Local) Bump(_ context.Context, key string) (Counts, error) {
	return l.decide(key, 0, bumpAlways), nil
}

// BumpIfFits implements Counter.
func (l *Local) BumpIfFits(_ context.Context, key string, capacity int) (Counts, error) {
	return l.decide(key, capacity, bumpIfFits), nil
}

// Read implements Counter.
func (l *Local) Read(_ context.Context, key string) (Counts, error) {
	return l.decide(key, 0, readOnly), nil
}

// bumpMode is what one call does to the current bucket.
type bumpMode int

const (
	readOnly bumpMode = iota
	bumpAlways
	bumpIfFits
)

// BucketStart is when bucket index began.
func BucketStart(index int64) time.Time {
	return time.UnixMicro(index * Window.Microseconds()).UTC()
}

// Fits reports whether one more request fits capacity over the trailing
// window, with cur and prev counted elapsed into the current bucket.
func Fits(cur, prev int64, elapsed time.Duration, capacity int) bool {
	share := float64(prev) * (1 - elapsed.Seconds()/Window.Seconds())
	return float64(cur+1)+share <= float64(capacity)
}
