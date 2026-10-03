package statemachine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// Covers the ADR-0003 orchestrators: the state change and the caller's outbox
// writes must land in ONE transaction, so a crash can never leave the row
// promoted with no event (or vice versa). The invariants under test are which
// callback runs, and whether Commit or only Rollback happens.

// ─── fakes ─────────────────────────────────────────────────────────────────

// fakeTx implements pgx.Tx. Only Exec/QueryRow/Commit/Rollback carry behaviour;
// the rest are unused by the Transitioner and panic if that ever changes.
type fakeTx struct {
	fakeExec
	commitErr    error
	commitCalls  int
	rollbackCall int
}

func (tx *fakeTx) Commit(context.Context) error {
	tx.commitCalls++
	return tx.commitErr
}

func (tx *fakeTx) Rollback(context.Context) error {
	tx.rollbackCall++
	return nil
}

func (tx *fakeTx) Begin(context.Context) (pgx.Tx, error) { return tx, nil }

func (tx *fakeTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("statemachine never calls Query on a tx")
}

func (tx *fakeTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	panic("unused")
}
func (tx *fakeTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { panic("unused") }
func (tx *fakeTx) LargeObjects() pgx.LargeObjects                         { panic("unused") }
func (tx *fakeTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	panic("unused")
}
func (tx *fakeTx) Conn() *pgx.Conn { return nil }

// fakePool implements dbPool: statement surface from fakeExec, plus Begin and
// Query.
type fakePool struct {
	fakeExec
	tx         *fakeTx
	beginErr   error
	beginCalls int
	rows       pgx.Rows
	queryErr   error
	querySQLs  []string
	queryArgsP []any
}

func (p *fakePool) Begin(context.Context) (pgx.Tx, error) {
	p.beginCalls++
	if p.beginErr != nil {
		return nil, p.beginErr
	}
	return p.tx, nil
}

func (p *fakePool) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	p.querySQLs = append(p.querySQLs, sql)
	p.queryArgsP = args
	if p.queryErr != nil {
		return nil, p.queryErr
	}
	return p.rows, nil
}

// fakeRows implements pgx.Rows over a fixed list of ids.
type fakeRows struct {
	ids      []uuid.UUID
	i        int
	scanErr  error
	finalErr error
	closed   bool
}

func (r *fakeRows) Next() bool {
	if r.i >= len(r.ids) {
		return false
	}
	r.i++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	if p, ok := dest[0].(*uuid.UUID); ok {
		*p = r.ids[r.i-1]
	}
	return nil
}

func (r *fakeRows) Close()                                       { r.closed = true }
func (r *fakeRows) Err() error                                   { return r.finalErr }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Values() ([]any, error)                       { return nil, nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }

// TypeMap joined pgx.Rows in pgx v5.11.0. nil is what the interface documents
// for a Rows that carries no values, which is this one: Scan above writes the
// id straight into dest and decodes nothing through pgtype.
func (r *fakeRows) TypeMap() *pgtype.Map { return nil }

// promotingPool returns a pool whose tx reports a successful promote.
func promotingPool() (*fakePool, *fakeTx) {
	tx := &fakeTx{fakeExec: fakeExec{row: fakeRow{state: string(StateAvailable)}}}
	return &fakePool{tx: tx}, tx
}

// ─── PromoteToAvailableInTx ────────────────────────────────────────────────

func TestPromoteInTxRunsCallbackAndCommits(t *testing.T) {
	pool, tx := promotingPool()
	sm := &Transitioner{pool: pool}

	var gotTx pgx.Tx
	changed, err := sm.PromoteToAvailableInTx(smCtx, objID, "e", 1, "c", "seq", SourceEvent,
		func(_ context.Context, tx pgx.Tx) error { gotTx = tx; return nil })
	if err != nil {
		t.Fatalf("PromoteToAvailableInTx: %v", err)
	}
	if !changed {
		t.Error("want changed=true")
	}
	// The callback must receive the SAME tx the promote ran on — that is what
	// makes the outbox write atomic with the state change.
	if gotTx != pgx.Tx(tx) {
		t.Error("onPromoted must run on the promoting tx")
	}
	if tx.commitCalls != 1 {
		t.Errorf("commit calls = %d, want 1", tx.commitCalls)
	}
}

// A stale event promotes nothing; emitting an outbox row for it would publish
// a state change that never happened.
func TestPromoteInTxSkipsCallbackWhenNothingChanged(t *testing.T) {
	tx := &fakeTx{fakeExec: fakeExec{row: fakeRow{err: pgx.ErrNoRows}}}
	sm := &Transitioner{pool: &fakePool{tx: tx}}

	called := false
	changed, err := sm.PromoteToAvailableInTx(smCtx, objID, "", 0, "", "seq", SourceEvent,
		func(context.Context, pgx.Tx) error { called = true; return nil })
	if err != nil {
		t.Fatalf("a no-op promote must not error: %v", err)
	}
	if changed {
		t.Error("want changed=false")
	}
	if called {
		t.Error("onPromoted must not run when nothing changed")
	}
	// The tx still commits — there is simply nothing in it.
	if tx.commitCalls != 1 {
		t.Errorf("commit calls = %d, want 1", tx.commitCalls)
	}
}

func TestPromoteInTxRollsBackOnCallbackError(t *testing.T) {
	pool, tx := promotingPool()
	sm := &Transitioner{pool: pool}
	boom := errors.New("outbox insert failed")

	changed, err := sm.PromoteToAvailableInTx(smCtx, objID, "e", 1, "c", "seq", SourceEvent,
		func(context.Context, pgx.Tx) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("callback error must surface, got %v", err)
	}
	if changed {
		t.Error("changed must be false when the tx is abandoned")
	}
	// This is the crux: the promotion must NOT be committed.
	if tx.commitCalls != 0 {
		t.Error("a failed callback must prevent the commit")
	}
	if tx.rollbackCall == 0 {
		t.Error("the deferred rollback must run")
	}
}

func TestPromoteInTxSurfacesPromoteError(t *testing.T) {
	boom := errors.New("connection refused")
	tx := &fakeTx{fakeExec: fakeExec{row: fakeRow{err: boom}}}
	sm := &Transitioner{pool: &fakePool{tx: tx}}

	called := false
	_, err := sm.PromoteToAvailableInTx(smCtx, objID, "", 0, "", "", SourceRPC,
		func(context.Context, pgx.Tx) error { called = true; return nil })
	if !errors.Is(err, boom) {
		t.Fatalf("want the promote error, got %v", err)
	}
	if called || tx.commitCalls != 0 {
		t.Error("neither the callback nor the commit may run after a promote error")
	}
}

func TestPromoteInTxWrapsBeginError(t *testing.T) {
	boom := errors.New("pool exhausted")
	sm := &Transitioner{pool: &fakePool{beginErr: boom}}

	_, err := sm.PromoteToAvailableInTx(smCtx, objID, "", 0, "", "", SourceRPC, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("want the begin error wrapped, got %v", err)
	}
}

func TestPromoteInTxWrapsCommitError(t *testing.T) {
	pool, tx := promotingPool()
	tx.commitErr = errors.New("serialization failure")
	sm := &Transitioner{pool: pool}

	changed, err := sm.PromoteToAvailableInTx(smCtx, objID, "e", 1, "c", "seq", SourceEvent, nil)
	if !errors.Is(err, tx.commitErr) {
		t.Fatalf("want the commit error wrapped, got %v", err)
	}
	// A failed commit means nothing happened, whatever the promote reported.
	if changed {
		t.Error("changed must be false when the commit failed")
	}
}

func TestPromoteInTxAcceptsNilCallback(t *testing.T) {
	pool, tx := promotingPool()
	sm := &Transitioner{pool: pool}

	changed, err := sm.PromoteToAvailableInTx(smCtx, objID, "e", 1, "c", "seq", SourceEvent, nil)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v, want true/nil", changed, err)
	}
	if tx.commitCalls != 1 {
		t.Error("a nil callback must still commit")
	}
}

// ─── SoftDeleteInTx / RestoreInTx (transitionInTx) ─────────────────────────

func TestSoftDeleteInTxRunsCallbackAndCommits(t *testing.T) {
	tx := &fakeTx{fakeExec: fakeExec{tag: tag("UPDATE 1")}}
	sm := &Transitioner{pool: &fakePool{tx: tx}}

	called := false
	err := sm.SoftDeleteInTx(smCtx, objID, 3, func(context.Context, pgx.Tx) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("SoftDeleteInTx: %v", err)
	}
	if !called {
		t.Error("onDeleted must run after a successful delete")
	}
	if tx.commitCalls != 1 {
		t.Errorf("commit calls = %d, want 1", tx.commitCalls)
	}
}

// A version conflict must short-circuit before the outbox write, or a delete
// that never happened would be published.
func TestSoftDeleteInTxConflictSkipsCallback(t *testing.T) {
	tx := &fakeTx{fakeExec: fakeExec{tag: tag("UPDATE 0")}}
	sm := &Transitioner{pool: &fakePool{tx: tx}}

	called := false
	err := sm.SoftDeleteInTx(smCtx, objID, 3, func(context.Context, pgx.Tx) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
	if called {
		t.Error("onDeleted must not run on conflict")
	}
	if tx.commitCalls != 0 {
		t.Error("a conflict must not commit")
	}
}

func TestSoftDeleteInTxRollsBackOnCallbackError(t *testing.T) {
	tx := &fakeTx{fakeExec: fakeExec{tag: tag("UPDATE 1")}}
	sm := &Transitioner{pool: &fakePool{tx: tx}}
	boom := errors.New("outbox failed")

	err := sm.SoftDeleteInTx(smCtx, objID, 0, func(context.Context, pgx.Tx) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("want the callback error, got %v", err)
	}
	if tx.commitCalls != 0 {
		t.Error("the delete must not commit when the outbox write failed")
	}
	if tx.rollbackCall == 0 {
		t.Error("the deferred rollback must run")
	}
}

func TestRestoreInTxRunsCallbackAndCommits(t *testing.T) {
	tx := &fakeTx{fakeExec: fakeExec{tag: tag("UPDATE 1")}}
	sm := &Transitioner{pool: &fakePool{tx: tx}}

	called := false
	if err := sm.RestoreInTx(smCtx, objID, func(context.Context, pgx.Tx) error {
		called = true
		return nil
	}); err != nil {
		t.Fatalf("RestoreInTx: %v", err)
	}
	if !called || tx.commitCalls != 1 {
		t.Errorf("called=%v commits=%d, want true/1", called, tx.commitCalls)
	}
}

func TestRestoreInTxNotFoundSkipsCallback(t *testing.T) {
	tx := &fakeTx{fakeExec: fakeExec{tag: tag("UPDATE 0")}}
	sm := &Transitioner{pool: &fakePool{tx: tx}}

	called := false
	err := sm.RestoreInTx(smCtx, objID, func(context.Context, pgx.Tx) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if called || tx.commitCalls != 0 {
		t.Error("a missing row must neither run the callback nor commit")
	}
}

func TestTransitionInTxWrapsBeginAndCommitErrors(t *testing.T) {
	t.Run("begin", func(t *testing.T) {
		boom := errors.New("pool exhausted")
		sm := &Transitioner{pool: &fakePool{beginErr: boom}}
		if err := sm.RestoreInTx(smCtx, objID, nil); !errors.Is(err, boom) {
			t.Fatalf("want the begin error, got %v", err)
		}
	})
	t.Run("commit", func(t *testing.T) {
		tx := &fakeTx{fakeExec: fakeExec{tag: tag("UPDATE 1")}, commitErr: errors.New("commit failed")}
		sm := &Transitioner{pool: &fakePool{tx: tx}}
		if err := sm.RestoreInTx(smCtx, objID, nil); !errors.Is(err, tx.commitErr) {
			t.Fatalf("want the commit error, got %v", err)
		}
	})
	t.Run("nil callback still commits", func(t *testing.T) {
		tx := &fakeTx{fakeExec: fakeExec{tag: tag("UPDATE 1")}}
		sm := &Transitioner{pool: &fakePool{tx: tx}}
		if err := sm.SoftDeleteInTx(smCtx, objID, 0, nil); err != nil {
			t.Fatalf("SoftDeleteInTx: %v", err)
		}
		if tx.commitCalls != 1 {
			t.Error("a nil callback must still commit")
		}
	})
}

// ─── pool-backed wrappers ──────────────────────────────────────────────────

// The plain wrapper opens a transaction too: the registration check locks the
// row, and the lock must last until the promote that depends on it.
func TestPromoteToAvailableRunsInATransaction(t *testing.T) {
	tx := &fakeTx{fakeExec: fakeExec{row: fakeRow{state: string(StateAvailable)}}}
	pool := &fakePool{tx: tx}
	sm := &Transitioner{pool: pool}

	changed, err := sm.PromoteToAvailable(smCtx, objID, "e", 5, "c", "seq", SourceReconciler)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v, want true/nil", changed, err)
	}
	if pool.beginCalls != 1 || tx.commitCalls != 1 {
		t.Errorf("begin=%d commit=%d, want one transaction committed", pool.beginCalls, tx.commitCalls)
	}
	if tx.lockCalls != 1 {
		t.Errorf("the row was locked %d times, want once", tx.lockCalls)
	}
}

func TestSoftDeleteAndRestoreRunOnThePool(t *testing.T) {
	pool := &fakePool{fakeExec: fakeExec{tag: tag("UPDATE 1")}}
	sm := &Transitioner{pool: pool}

	if err := sm.SoftDelete(smCtx, objID, 2); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if err := sm.Restore(smCtx, objID); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if pool.beginCalls != 0 {
		t.Error("the non-tx wrappers must not open a transaction")
	}
}

func TestMarkFailed(t *testing.T) {
	pool := &fakePool{fakeExec: fakeExec{tag: tag("UPDATE 1")}}
	sm := &Transitioner{pool: pool}

	if err := sm.MarkFailed(smCtx, objID, "presign expired"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	if len(pool.execArgs) != 1 || pool.execArgs[0] != objID {
		t.Errorf("MarkFailed must key on the object id, got %v", pool.execArgs)
	}
}

// MarkFailed is idempotent by SQL guard, so zero rows is a success, not an
// error — the reaper re-runs over the same ids on every tick.
func TestMarkFailedIsIdempotent(t *testing.T) {
	sm := &Transitioner{pool: &fakePool{fakeExec: fakeExec{tag: tag("UPDATE 0")}}}

	if err := sm.MarkFailed(smCtx, objID, "already failed"); err != nil {
		t.Fatalf("a zero-row MarkFailed must not error, got %v", err)
	}
}

func TestMarkFailedWrapsErrors(t *testing.T) {
	boom := errors.New("connection reset")
	sm := &Transitioner{pool: &fakePool{fakeExec: fakeExec{execErr: boom}}}

	if err := sm.MarkFailed(smCtx, objID, "x"); !errors.Is(err, boom) {
		t.Fatalf("cause must be wrapped, got %v", err)
	}
}

// New keeps taking the concrete *pgxpool.Pool even though the field is now the
// dbPool interface, so existing wiring compiles unchanged.
func TestNewStoresThePool(t *testing.T) {
	if sm := New(nil); sm == nil {
		t.Fatal("New returned nil")
	}
}

// ─── ScanPendingExpired ────────────────────────────────────────────────────

func TestScanPendingExpiredCollectsIDs(t *testing.T) {
	want := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	rows := &fakeRows{ids: want}
	pool := &fakePool{rows: rows}
	sm := &Transitioner{pool: pool}

	got, err := sm.ScanPendingExpired(smCtx, 90*time.Second, 50)
	if err != nil {
		t.Fatalf("ScanPendingExpired: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d ids, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("id[%d] = %v, want %v", i, got[i], want[i])
		}
	}
	// The grace period travels as a Postgres interval string.
	if len(pool.queryArgsP) != 2 {
		t.Fatalf("want 2 query args, got %v", pool.queryArgsP)
	}
	if pool.queryArgsP[0] != "1m30s" {
		t.Errorf("interval arg = %v, want 1m30s", pool.queryArgsP[0])
	}
	if pool.queryArgsP[1] != 50 {
		t.Errorf("limit arg = %v, want 50", pool.queryArgsP[1])
	}
	if !rows.closed {
		t.Error("rows must be closed")
	}
}

func TestScanPendingExpiredEmpty(t *testing.T) {
	sm := &Transitioner{pool: &fakePool{rows: &fakeRows{}}}

	got, err := sm.ScanPendingExpired(smCtx, time.Minute, 10)
	if err != nil {
		t.Fatalf("ScanPendingExpired: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want no ids, got %v", got)
	}
}

func TestScanPendingExpiredQueryError(t *testing.T) {
	boom := errors.New("query failed")
	sm := &Transitioner{pool: &fakePool{queryErr: boom}}

	if _, err := sm.ScanPendingExpired(smCtx, time.Minute, 10); !errors.Is(err, boom) {
		t.Fatalf("cause must be wrapped, got %v", err)
	}
}

func TestScanPendingExpiredScanError(t *testing.T) {
	boom := errors.New("bad scan")
	sm := &Transitioner{pool: &fakePool{rows: &fakeRows{ids: []uuid.UUID{uuid.New()}, scanErr: boom}}}

	if _, err := sm.ScanPendingExpired(smCtx, time.Minute, 10); !errors.Is(err, boom) {
		t.Fatalf("want the scan error, got %v", err)
	}
}

// A mid-iteration failure surfaces only via rows.Err(); dropping it would
// silently truncate the reaper's work list.
func TestScanPendingExpiredSurfacesRowsErr(t *testing.T) {
	boom := errors.New("connection lost mid-iteration")
	sm := &Transitioner{pool: &fakePool{rows: &fakeRows{ids: []uuid.UUID{uuid.New()}, finalErr: boom}}}

	if _, err := sm.ScanPendingExpired(smCtx, time.Minute, 10); !errors.Is(err, boom) {
		t.Fatalf("want the rows error, got %v", err)
	}
}
