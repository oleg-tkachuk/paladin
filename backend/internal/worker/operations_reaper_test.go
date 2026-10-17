package worker

import (
	"context"
	"errors"
	"testing"
	"time"
)

// batchPurger answers each call with the next count, then 0; err, when set,
// is returned on the call numbered failOn (1-based).
type batchPurger struct {
	counts  []int64
	err     error
	failOn  int
	calls   int
	cutoffs []time.Time
}

func (p *batchPurger) next(cutoff time.Time) (int64, error) {
	p.calls++
	p.cutoffs = append(p.cutoffs, cutoff)
	if p.err != nil && p.calls == p.failOn {
		return 0, p.err
	}
	if p.calls <= len(p.counts) {
		return p.counts[p.calls-1], nil
	}
	return 0, nil
}

type opsPurger struct{ batchPurger }

func (p *opsPurger) PurgeTerminalBefore(_ context.Context, cutoff time.Time) (int64, error) {
	return p.next(cutoff)
}

type erasuresPurger struct{ batchPurger }

func (p *erasuresPurger) PurgeErasedBefore(_ context.Context, cutoff time.Time) (int64, error) {
	return p.next(cutoff)
}

// One tick drains both tables on the same cutoff: a tombstone is kept exactly
// as long as the operation that might be reissued.
func TestOperationsReaperSweepDrainsTombstonesOnTheSameCutoff(t *testing.T) {
	ops := &opsPurger{batchPurger{counts: []int64{10_000, 3}}}
	erasures := &erasuresPurger{batchPurger{counts: []int64{10_000, 10_000, 7}}}
	r := &OperationsReaper{Repo: ops, Erasures: erasures}
	cutoff := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)

	if err := r.sweep(context.Background(), cutoff); err != nil {
		t.Fatalf("sweep = %v, want nil on a clean drain", err)
	}

	// Each drain stops on the call that purges nothing.
	if ops.calls != 3 {
		t.Errorf("operation purges = %d, want 3", ops.calls)
	}
	if erasures.calls != 4 {
		t.Errorf("tombstone purges = %d, want 4", erasures.calls)
	}
	for _, c := range erasures.cutoffs {
		if !c.Equal(cutoff) {
			t.Errorf("tombstone cutoff = %v, want %v", c, cutoff)
		}
	}
}

func TestOperationsReaperStopsDrainingTombstonesOnError(t *testing.T) {
	erasures := &erasuresPurger{batchPurger{counts: []int64{10_000, 10_000}, err: errors.New("db down"), failOn: 2}}
	r := &OperationsReaper{Repo: &opsPurger{}, Erasures: erasures}

	if err := r.sweep(context.Background(), time.Now()); !errors.Is(err, erasures.err) {
		t.Errorf("sweep = %v, want the tombstone purge failure", err)
	}

	if erasures.calls != 2 {
		t.Errorf("tombstone purges = %d, want 2: stop at the failing call", erasures.calls)
	}
}

func TestOperationsReaperRunsWithoutATombstoneRepo(t *testing.T) {
	ops := &opsPurger{}
	r := &OperationsReaper{Repo: ops}
	if err := r.sweep(context.Background(), time.Now()); err != nil {
		t.Fatalf("sweep = %v", err)
	}
	if ops.calls != 1 {
		t.Errorf("operation purges = %d, want 1", ops.calls)
	}
}

// A failed operations purge fails the tick, and the tombstones are still
// drained: the two tables are independent, so one outage does not stall both.
func TestOperationsReaperReturnsTheFirstFailure(t *testing.T) {
	opsFailure := errors.New("operations purge failed")
	erasuresFailure := errors.New("tombstone purge failed")
	ops := &opsPurger{batchPurger{err: opsFailure, failOn: 1}}
	erasures := &erasuresPurger{batchPurger{err: erasuresFailure, failOn: 1}}
	r := &OperationsReaper{Repo: ops, Erasures: erasures}

	if err := r.sweep(context.Background(), time.Now()); !errors.Is(err, opsFailure) {
		t.Errorf("sweep = %v, want the first failure (%v)", err, opsFailure)
	}
	if erasures.calls != 1 {
		t.Errorf("tombstone purges = %d, want 1: a failed operations drain must not skip them", erasures.calls)
	}
}
