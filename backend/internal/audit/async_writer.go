// Async audit writer.
//
// The synchronous audit interceptor (internal/middleware/audit.go)
// pays Postgres-Insert latency on every mutating RPC's response
// path. Under load the audit row write becomes a tail-latency
// contributor: every UploadObject, CreateTenant, etc. waits one
// extra RTT to the audit DB before returning to the caller. The
// existing code already short-circuits Insert errors
// (`_ = a.write(...)`) so a write failure never fails the request —
// the only thing left to remove is the latency cost.
//
// This wrapper sits between the interceptor and the real
// AuditRepository. Insert enqueues to a bounded channel and returns
// immediately; a background goroutine pulls batches and forwards
// each entry to the inner writer. On shutdown the goroutine drains
// the remaining buffer with a deadline so a slow Postgres doesn't
// block process exit forever.
//
// Backpressure: when the buffer fills, Insert blocks. We
// deliberately prefer back-pressure to silent dropping — if audit
// volume outpaces Postgres write throughput the operator should see
// the symptom (request-path slowdown) rather than discover later
// that the audit_log table has holes. A "DropOnFull" policy is
// available for high-volume non-compliance regimes.
//
// Crash semantics: in-flight (queued-but-not-yet-flushed) entries
// are lost on process kill. For tighter durability the next step is
// a table-backed outbox — see BACKLOG. Until then, the bounded
// channel + linger ceiling means the worst-case loss is one batch
// (~32 rows or ~200ms of traffic, whichever fills first).

package audit

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/middleware"
)

// Inner is the underlying writer (typically the Postgres adapter).
// Same shape as middleware.AuditWriter — defined here to dodge the
// import cycle audit → middleware → audit.
type Inner interface {
	Insert(ctx context.Context, e admindomain.AuditEntry) error
}

// AsyncWriterConfig tunes the buffer + flush cadence.
type AsyncWriterConfig struct {
	// BufferSize is the channel capacity. When full, Insert blocks
	// the caller (the audit interceptor; the request handler has
	// already returned by then because the audit Insert happens in
	// a deferred goroutine — see middleware/audit.go's WrapUnary).
	// Default 1024.
	BufferSize int
	// MaxBatch is the soft cap on entries pulled before a flush.
	// Larger batches improve Postgres throughput but increase the
	// per-batch latency. Default 32.
	MaxBatch int
	// MaxLinger is the longest a partial batch sits in the buffer
	// before being flushed. Bounds the worst-case "row hasn't
	// landed in DB yet" window. Default 200ms.
	MaxLinger time.Duration
	// DropOnFull, when true, silently discards entries instead of
	// blocking when the buffer is full. Logs at WARN with a periodic
	// counter. Default false (prefer back-pressure).
	DropOnFull bool
	// Logger is required — drops, flush errors, and shutdown drains
	// all log here. Passing nil panics at construction.
	Logger *zap.Logger
}

func (c *AsyncWriterConfig) defaults() {
	if c.BufferSize <= 0 {
		c.BufferSize = 1024
	}
	if c.MaxBatch <= 0 {
		c.MaxBatch = 32
	}
	if c.MaxLinger <= 0 {
		c.MaxLinger = 200 * time.Millisecond
	}
}

// AsyncWriter is the wrapper. It satisfies middleware.AuditWriter
// (Insert) AND app.BackgroundJob (Run) — register it once, use it
// everywhere a synchronous writer was used.
type AsyncWriter struct {
	inner Inner
	cfg   AsyncWriterConfig
	in    chan admindomain.AuditEntry
	// drops counts entries discarded by DropOnFull. Flush loop logs
	// periodically when non-zero so the operator notices saturation
	// without per-drop log spam.
	drops    int64
	dropsMu  sync.Mutex
	done     chan struct{}
	stopOnce sync.Once
}

// NewAsyncWriter wraps `inner` with bounded buffering + batched
// background flush. Call Run from a BackgroundJob slot.
func NewAsyncWriter(inner Inner, cfg AsyncWriterConfig) *AsyncWriter {
	if cfg.Logger == nil {
		panic("audit.NewAsyncWriter: Logger required")
	}
	cfg.defaults()
	return &AsyncWriter{
		inner: inner,
		cfg:   cfg,
		in:    make(chan admindomain.AuditEntry, cfg.BufferSize),
		done:  make(chan struct{}),
	}
}

// Insert satisfies middleware.AuditWriter. Returns immediately on
// the happy path. Under DropOnFull=false it blocks when the buffer
// is full; under DropOnFull=true it logs+drops and returns nil. The
// ctx is intentionally ignored — the caller's request ctx will be
// cancelled long before we flush, so we keep the entry alive via the
// channel and use Background() at flush time.
func (w *AsyncWriter) Insert(_ context.Context, e admindomain.AuditEntry) error {
	if w.cfg.DropOnFull {
		select {
		case w.in <- e:
			return nil
		case <-w.done:
			return ErrClosed
		default:
			w.dropsMu.Lock()
			w.drops++
			w.dropsMu.Unlock()
			return nil
		}
	}
	// Back-pressure: block on send. The audit interceptor calls
	// Insert from a deferred goroutine so the request has already
	// returned — blocking here only delays the audit row, not the
	// caller's response. Also select on w.done: once Run has exited
	// nothing drains w.in, so a late Insert would otherwise block
	// forever (a goroutine leak / shutdown hang once the buffer fills).
	select {
	case w.in <- e:
		return nil
	case <-w.done:
		return ErrClosed
	}
}

// Run is the flush loop. Pulls entries off the channel, accumulates
// up to MaxBatch (or MaxLinger time), then drains the batch through
// the inner writer. Returns when ctx is done — at which point we
// drain whatever's left in the buffer with a bounded grace period.
func (w *AsyncWriter) Run(ctx context.Context) error {
	timer := time.NewTimer(w.cfg.MaxLinger)
	defer timer.Stop()
	batch := make([]admindomain.AuditEntry, 0, w.cfg.MaxBatch)
	dropsTicker := time.NewTicker(30 * time.Second)
	defer dropsTicker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		// Use Background() at flush time. The request ctx has long
		// since been cancelled; the audit Insert lives in the
		// process lifetime, not the request's.
		for i := range batch {
			if err := w.inner.Insert(context.Background(), batch[i]); err != nil {
				w.cfg.Logger.Warn("audit: async insert failed",
					zap.String("action", batch[i].Action),
					zap.Error(err))
			}
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-ctx.Done():
			// Graceful drain: pull anything remaining in the buffer
			// up to a short deadline, then exit. Background() ctx
			// for the same reason as the steady-state path.
			drainCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			w.drain(drainCtx, &batch)
			cancel()
			w.stopOnce.Do(func() { close(w.done) })
			return nil

		case e, ok := <-w.in:
			if !ok {
				flush()
				w.stopOnce.Do(func() { close(w.done) })
				return nil
			}
			batch = append(batch, e)
			if len(batch) >= w.cfg.MaxBatch {
				flush()
				if !timer.Stop() {
					<-timer.C
				}
				timer.Reset(w.cfg.MaxLinger)
			}

		case <-timer.C:
			flush()
			timer.Reset(w.cfg.MaxLinger)

		case <-dropsTicker.C:
			w.dropsMu.Lock()
			n := w.drops
			w.drops = 0
			w.dropsMu.Unlock()
			if n > 0 {
				w.cfg.Logger.Warn("audit: dropped entries (DropOnFull saturated)",
					zap.Int64("count", n))
			}
		}
	}
}

// drain pulls remaining entries on shutdown. Bounded by ctx so a
// stuck Postgres can't deadlock process exit.
func (w *AsyncWriter) drain(ctx context.Context, batch *[]admindomain.AuditEntry) {
	for {
		select {
		case <-ctx.Done():
			w.cfg.Logger.Warn("audit: shutdown drain timed out; entries lost",
				zap.Int("buffered", len(w.in)+len(*batch)))
			return
		case e, ok := <-w.in:
			if !ok {
				return
			}
			*batch = append(*batch, e)
			if len(*batch) >= w.cfg.MaxBatch {
				w.flushBatch(ctx, *batch)
				*batch = (*batch)[:0]
			}
		default:
			// No more pending → flush what we have and return.
			w.flushBatch(ctx, *batch)
			*batch = (*batch)[:0]
			return
		}
	}
}

func (w *AsyncWriter) flushBatch(ctx context.Context, batch []admindomain.AuditEntry) {
	for i := range batch {
		if err := w.inner.Insert(ctx, batch[i]); err != nil {
			w.cfg.Logger.Warn("audit: shutdown insert failed",
				zap.String("action", batch[i].Action),
				zap.Error(err))
		}
	}
}

// Compile-time interface guard. If middleware.AuditWriter ever
// changes shape, this fails at build-time rather than producing a
// silently-wrong runtime.
var _ middleware.AuditWriter = (*AsyncWriter)(nil)

// ErrClosed is returned by Insert when Run has already exited (shutdown)
// — the entry could not be enqueued. Callers (the audit interceptor)
// already ignore Insert's error, so this just unblocks a late writer
// instead of leaking the goroutine.
var ErrClosed = errors.New("audit: writer closed")
