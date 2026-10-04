package statemachine

import (
	"errors"
	"testing"
	"time"
)

// overdueRow answers PendingOverdue's count and age.
type overdueRow struct {
	count   int64
	seconds float64
	err     error
}

func (r overdueRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*int64) = r.count
	*dest[1].(*float64) = r.seconds
	return nil
}

func TestPendingOverdueReportsCountAndAge(t *testing.T) {
	pool := &fakePool{fakeExec: fakeExec{row: overdueRow{count: 29, seconds: 5400.5}}}
	sm := &Transitioner{pool: pool}

	count, oldest, err := sm.PendingOverdue(smCtx, 2*time.Hour)
	if err != nil {
		t.Fatalf("PendingOverdue: %v", err)
	}
	if count != 29 || oldest != 90*time.Minute+500*time.Millisecond {
		t.Errorf("got %d, %v; want 29, 1h30m0.5s", count, oldest)
	}
	// The grace travels as a Postgres interval string, as ScanPendingExpired's.
	if len(pool.queryArgs) != 1 || pool.queryArgs[0] != "2h0m0s" {
		t.Errorf("query args = %v, want [2h0m0s]", pool.queryArgs)
	}
}

func TestPendingOverdueWrapsTheError(t *testing.T) {
	boom := errors.New("boom")
	sm := &Transitioner{pool: &fakePool{fakeExec: fakeExec{row: overdueRow{err: boom}}}}

	if _, _, err := sm.PendingOverdue(smCtx, time.Hour); !errors.Is(err, boom) {
		t.Errorf("err = %v, want it wrapping boom", err)
	}
}
