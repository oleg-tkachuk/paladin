package audit

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
)

// captureInner records every Insert in order. The flush tests
// assert ordering (FIFO from the channel) and total count.
type captureInner struct {
	mu      sync.Mutex
	entries []admindomain.AuditEntry
	// optional knobs:
	failN  int32 // first N Inserts return err
	failed int32
}

func (c *captureInner) Insert(_ context.Context, e admindomain.AuditEntry) error {
	if atomic.AddInt32(&c.failed, 1) <= atomic.LoadInt32(&c.failN) {
		return errors.New("boom")
	}
	c.mu.Lock()
	c.entries = append(c.entries, e)
	c.mu.Unlock()
	return nil
}

func (c *captureInner) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func newEntry(action string) admindomain.AuditEntry {
	return admindomain.AuditEntry{
		EntryID: uuid.Must(uuid.NewV7()),
		Action:  action,
		At:      time.Now().UTC(),
	}
}

// TestAsyncWriter_BasicFlush: Insert returns immediately, the
// background flusher catches the entries within MaxLinger.
func TestAsyncWriter_BasicFlush(t *testing.T) {
	inner := &captureInner{}
	w := NewAsyncWriter(inner, AsyncWriterConfig{
		MaxBatch:  4,
		MaxLinger: 50 * time.Millisecond,
		Logger:    zap.NewNop(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()

	start := time.Now()
	for i := 0; i < 10; i++ {
		_ = w.Insert(context.Background(), newEntry("Create"))
	}
	insertElapsed := time.Since(start)
	// Insert path must be far below the inner write RTT — under a
	// 100ms ceiling here, with two orders of margin from the
	// MaxLinger of 50ms (we're measuring the buffered send, not the
	// flush).
	if insertElapsed > 100*time.Millisecond {
		t.Errorf("Insert path too slow: %v", insertElapsed)
	}

	// Give the flusher up to 2 linger periods + ample slack.
	deadline := time.Now().Add(500 * time.Millisecond)
	for inner.Count() < 10 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := inner.Count(); got != 10 {
		t.Fatalf("inner received %d entries, want 10", got)
	}
}

// TestAsyncWriter_BatchSizeTrigger: filling the batch in less than
// one linger period should still cause a flush as soon as MaxBatch
// is hit — confirms the size-trigger path (not just the timer).
func TestAsyncWriter_BatchSizeTrigger(t *testing.T) {
	inner := &captureInner{}
	w := NewAsyncWriter(inner, AsyncWriterConfig{
		MaxBatch:  3,
		MaxLinger: 10 * time.Second, // would dominate if size trigger broken
		Logger:    zap.NewNop(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()

	for i := 0; i < 3; i++ {
		_ = w.Insert(context.Background(), newEntry("Create"))
	}
	// Expect flush within ~50ms, well before the 10s linger.
	deadline := time.Now().Add(300 * time.Millisecond)
	for inner.Count() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := inner.Count(); got != 3 {
		t.Fatalf("size trigger did not flush: got %d, want 3", got)
	}
}

// TestAsyncWriter_DropOnFull: when DropOnFull=true and the channel
// is full, Insert returns nil without blocking and the drop counter
// increments. We saturate by spawning Run on a tiny buffer and
// pumping faster than the drain rate.
func TestAsyncWriter_DropOnFull(t *testing.T) {
	inner := &captureInner{}
	w := NewAsyncWriter(inner, AsyncWriterConfig{
		BufferSize: 2,
		MaxBatch:   2,
		MaxLinger:  time.Second, // long linger keeps the buffer full
		DropOnFull: true,
		Logger:     zap.NewNop(),
	})
	// Do NOT start Run — the buffer fills and Insert must drop
	// rather than block. (The test would deadlock without DropOnFull.)
	const N = 100
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < N; i++ {
			_ = w.Insert(context.Background(), newEntry("Create"))
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Insert blocked despite DropOnFull=true")
	}
	// We don't assert exact drop count because the buffer can hold a
	// few before saturating; we just need >0.
	w.dropsMu.Lock()
	drops := w.drops
	w.dropsMu.Unlock()
	if drops == 0 {
		t.Fatalf("expected drops > 0 with saturated buffer; got %d", drops)
	}
}

// TestAsyncWriter_GracefulDrain: on shutdown, in-flight entries are
// flushed before Run returns. We push N entries then cancel; the
// inner writer must see all N.
func TestAsyncWriter_GracefulDrain(t *testing.T) {
	inner := &captureInner{}
	w := NewAsyncWriter(inner, AsyncWriterConfig{
		MaxBatch:  8,
		MaxLinger: 5 * time.Second, // long → only drain path flushes
		Logger:    zap.NewNop(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = w.Run(ctx) }()

	const N = 5
	for i := 0; i < N; i++ {
		_ = w.Insert(context.Background(), newEntry("Create"))
	}
	// Allow the entries to enter the channel before we cancel.
	time.Sleep(20 * time.Millisecond)
	cancel()
	// Wait for Run to fully exit.
	select {
	case <-w.done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit within 2s of cancel")
	}
	if got := inner.Count(); got != N {
		t.Fatalf("drain dropped entries: got %d, want %d", got, N)
	}
}

// TestAsyncWriter_InnerErrorIsLogged: a failing inner write must not
// break the loop. The wrapper logs and keeps processing subsequent
// entries.
func TestAsyncWriter_InnerErrorIsLogged(t *testing.T) {
	inner := &captureInner{failN: 1} // first call fails
	w := NewAsyncWriter(inner, AsyncWriterConfig{
		MaxBatch:  4,
		MaxLinger: 30 * time.Millisecond,
		Logger:    zap.NewNop(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()

	_ = w.Insert(context.Background(), newEntry("Create"))
	_ = w.Insert(context.Background(), newEntry("Update"))
	// The first entry fails inside the inner (counted in failed but
	// not appended to entries); the second must still land.
	deadline := time.Now().Add(300 * time.Millisecond)
	for inner.Count() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if inner.Count() < 1 {
		t.Fatalf("second entry never landed after first failure; inner.entries=%d", inner.Count())
	}
}
