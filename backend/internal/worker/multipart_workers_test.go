package worker

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// unreachablePool is a pool that never connects: pgxpool dials lazily, and
// these tests stop before any statement.
func unreachablePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://nobody@127.0.0.1:1/none")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func cancelled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// The retry delay doubles from the first backoff and stops at MaxBackoff.
// (`d >= MaxBackoff` against `>`: at equality both return MaxBackoff.)
func TestMultipartAbortBackoff(t *testing.T) {
	w := &MultipartAbortDrainer{MaxBackoff: 10 * multipartAbortFirstBackoff}
	for attempts, want := range map[int32]time.Duration{
		0: multipartAbortFirstBackoff,
		1: 2 * multipartAbortFirstBackoff,
		3: 8 * multipartAbortFirstBackoff,
		4: 10 * multipartAbortFirstBackoff,
		9: 10 * multipartAbortFirstBackoff,
	} {
		if got := w.backoff(attempts); got != want {
			t.Errorf("backoff(%d) = %v, want %v", attempts, got, want)
		}
	}
}

func TestMultipartAbortDrainerDefaults(t *testing.T) {
	w := &MultipartAbortDrainer{Pool: unreachablePool(t), Interval: time.Hour}
	_ = w.Run(cancelled())
	if w.BatchSize != DefaultMultipartAbortBatchSize || w.MaxBackoff != DefaultMultipartAbortMaxBackoff {
		t.Errorf("defaults = batch %d, max backoff %v", w.BatchSize, w.MaxBackoff)
	}

	// Disabled either way, and it returns at once rather than ticking.
	for name, d := range map[string]*MultipartAbortDrainer{
		"no interval": {Pool: unreachablePool(t)},
		"no pool":     {Interval: time.Hour},
	} {
		if err := d.Run(context.Background()); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if d.BatchSize != 0 {
			t.Errorf("%s: a disabled drainer took defaults", name)
		}
	}
}

func TestMultipartReaperDefaults(t *testing.T) {
	r := &MultipartReaper{TTL: time.Hour}
	_ = r.Run(cancelled())
	if r.Interval != DefaultMultipartReaperInterval || r.BatchSize != DefaultMultipartReaperBatchSize {
		t.Errorf("defaults = interval %v, batch %d", r.Interval, r.BatchSize)
	}

	// No TTL disables it: Run returns at once instead of ticking until the
	// context ends.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := (&MultipartReaper{}).Run(ctx); err != nil {
		t.Errorf("a reaper without a TTL ran: %v", err)
	}
}
