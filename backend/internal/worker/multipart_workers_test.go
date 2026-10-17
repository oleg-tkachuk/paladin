package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
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

// errDBDown stands in for any statement the database refuses.
var errDBDown = errors.New("database unavailable")

// sweepDeadline bounds a sweep against an unreachable pool: the dial is
// refused at once, so this only guards against a hang.
const sweepDeadline = 10 * time.Second

// stubDBTX answers every query sqlc issues: with err when set, otherwise with
// no rows. It lets a Sweep over *sqlc.Queries run without a database.
type stubDBTX struct{ err error }

func (s stubDBTX) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, s.err
}

func (s stubDBTX) Query(context.Context, string, ...any) (pgx.Rows, error) {
	if s.err != nil {
		return nil, s.err
	}
	return noRows{}, nil
}

func (s stubDBTX) QueryRow(context.Context, string, ...any) pgx.Row { return noRows(s) }

// noRows is an empty result set.
type noRows struct{ err error }

func (noRows) Close()                                       {}
func (r noRows) Err() error                                 { return r.err }
func (noRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (noRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (noRows) Next() bool                                   { return false }
func (r noRows) Scan(...any) error {
	if r.err != nil {
		return r.err
	}
	return pgx.ErrNoRows
}
func (noRows) Values() ([]any, error) { return nil, nil }
func (noRows) RawValues() [][]byte    { return nil }
func (noRows) Conn() *pgx.Conn        { return nil }
func (noRows) TypeMap() *pgtype.Map   { return nil }

// A sweep whose listing fails is a failed tick: the error reaches RunTicker,
// which is what the failing-worker alert reads.
func TestMultipartReaperSweepReturnsListFailure(t *testing.T) {
	r := &MultipartReaper{Q: sqlc.New(stubDBTX{err: errDBDown}), TTL: time.Hour, BatchSize: DefaultMultipartReaperBatchSize}
	if err := r.Sweep(context.Background()); !errors.Is(err, errDBDown) {
		t.Errorf("Sweep = %v, want %v", err, errDBDown)
	}
}

func TestMultipartReaperSweepWithNothingStaleSucceeds(t *testing.T) {
	r := &MultipartReaper{Q: sqlc.New(stubDBTX{}), TTL: time.Hour, BatchSize: DefaultMultipartReaperBatchSize}
	if err := r.Sweep(context.Background()); err != nil {
		t.Errorf("Sweep = %v, want nil", err)
	}
}

// The claim runs on a transaction, so an unreachable pool fails the tick at
// Begin.
func TestMultipartAbortDrainerSweepReturnsBeginFailure(t *testing.T) {
	pool := unreachablePool(t)
	w := &MultipartAbortDrainer{Pool: pool, Q: sqlc.New(pool), BatchSize: DefaultMultipartAbortBatchSize}
	ctx, cancel := context.WithTimeout(context.Background(), sweepDeadline)
	defer cancel()
	if err := w.Sweep(ctx); err == nil {
		t.Error("Sweep = nil against an unreachable database, want the error")
	}
}
