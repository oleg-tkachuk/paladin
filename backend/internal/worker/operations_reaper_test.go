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

	r.sweep(context.Background(), cutoff)

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

	r.sweep(context.Background(), time.Now())

	if erasures.calls != 2 {
		t.Errorf("tombstone purges = %d, want 2: stop at the failing call", erasures.calls)
	}
}

func TestOperationsReaperRunsWithoutATombstoneRepo(t *testing.T) {
	ops := &opsPurger{}
	r := &OperationsReaper{Repo: ops}
	r.sweep(context.Background(), time.Now())
	if ops.calls != 1 {
		t.Errorf("operation purges = %d, want 1", ops.calls)
	}
}
