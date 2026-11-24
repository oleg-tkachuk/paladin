package statemachine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// The transition bodies take a dbExec rather than reaching for t.pool, which
// is exactly so they can run on either the pool or a caller's tx. That same
// seam lets these tests drive every branch with no database; the tx
// orchestrators are covered in tx_test.go via the dbPool seam.

type fakeRow struct {
	state string
	err   error
	// unanchored answers the registration read with no anchor: the row's
	// URL has expired and settled. Otherwise it is anchored to
	// anchorPlacement.
	unanchored bool
}

// anchorPlacement is the bucket every fake object's upload is anchored to.
var anchorPlacement = Placement{BackendID: "be-1", Bucket: "shared"}

// registrationColumns is how many columns the registration read scans.
const registrationColumns = 5

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) > 0 {
		if p, ok := dest[0].(*string); ok {
			*p = r.state
		}
	}
	if len(dest) == registrationColumns && !r.unanchored {
		scanAnchor(dest)
	}
	return nil
}

// scanAnchor answers the registration read's anchor columns with
// anchorPlacement.
func scanAnchor(dest []any) {
	backend, bucket := anchorPlacement.BackendID, anchorPlacement.Bucket
	*dest[3].(**string) = &backend
	*dest[4].(**string) = &bucket
}

// tenantRow answers the fence's read of the object's tenant.
type tenantRow struct{ err error }

func (r tenantRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*pgtype.UUID) = pgtype.UUID{Bytes: fenceTenant, Valid: true}
	return nil
}

// fenceTenant is the tenant every fake object belongs to.
var fenceTenant = uuid.MustParse("11111111-2222-3333-4444-555555555555")

type fakeExec struct {
	tag     pgconn.CommandTag
	execErr error
	row     pgx.Row
	// lockRow answers the registration check's SELECT … FOR UPDATE; nil
	// answers it with row, whose state then decides whether it checks.
	lockRow pgx.Row
	// bindingRow answers the placement check's read of the collection's
	// binding (checkPlacement).
	bindingRow pgx.Row
	// tenantErr fails the fence's read of the object's tenant.
	tenantErr error

	execSQL string
	// execs is every statement Exec ran, in order.
	execs        []string
	execArgs     []any
	querySQL     string
	queryArgs    []any
	execCalls    int
	lockCalls    int
	bindingCalls int
	tenantCalls  int
}

func (f *fakeExec) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.execCalls++
	f.execSQL, f.execArgs = sql, args
	f.execs = append(f.execs, sql)
	return f.tag, f.execErr
}

func (f *fakeExec) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, "FOR UPDATE") {
		f.lockCalls++
		if f.lockRow != nil {
			return f.lockRow
		}
		return f.row
	}
	if strings.Contains(sql, "SELECT tenant_id FROM objects") {
		f.tenantCalls++
		return tenantRow{err: f.tenantErr}
	}
	if strings.Contains(sql, "JOIN storage_backends") {
		f.bindingCalls++
		return f.bindingRow
	}
	f.querySQL, f.queryArgs = sql, args
	return f.row
}

func tag(s string) pgconn.CommandTag { return pgconn.NewCommandTag(s) }

var (
	smCtx = context.Background()
	objID = uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
)

// ─── promote ───────────────────────────────────────────────────────────────

func TestPromoteReportsChangedOnUpdate(t *testing.T) {
	sm := &Transitioner{}
	ex := &fakeExec{row: fakeRow{state: string(StateAvailable)}}

	changed, err := sm.promote(smCtx, ex, objID, nil, "etag-1", 42, "chk-1", "seq-9", SourceEvent)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if !changed {
		t.Error("a row that came back must report changed=true")
	}
	// The guarded UPDATE's parameters are the whole contract: $1..$6 in order.
	if len(ex.queryArgs) != 6 {
		t.Fatalf("want 6 query args, got %d: %v", len(ex.queryArgs), ex.queryArgs)
	}
	for i, want := range []any{objID, "etag-1", int64(42), "chk-1", "seq-9", false} {
		if ex.queryArgs[i] != want {
			t.Errorf("arg $%d = %v, want %v", i+1, ex.queryArgs[i], want)
		}
	}
}

// A stale storage event (sequencer older than the stored one) matches no row.
// That is a routine no-op, NOT an error — surfacing it would re-queue the
// object on every consumer tick.
func TestPromoteTreatsNoRowsAsNoOp(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"bare sentinel", pgx.ErrNoRows},
		{"wrapped sentinel", fmt.Errorf("scan: %w", pgx.ErrNoRows)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sm := &Transitioner{}
			ex := &fakeExec{row: fakeRow{err: tc.err}}

			changed, err := sm.promote(smCtx, ex, objID, nil, "", 0, "", "seq-1", SourceEvent)
			if err != nil {
				t.Fatalf("no-rows must not surface an error, got %v", err)
			}
			if changed {
				t.Error("a no-op promote must report changed=false")
			}
		})
	}
}

func TestPromoteWrapsRealErrors(t *testing.T) {
	sm := &Transitioner{}
	boom := errors.New("connection refused")
	ex := &fakeExec{row: fakeRow{err: boom}}

	changed, err := sm.promote(smCtx, ex, objID, nil, "", 0, "", "", SourceRPC)
	if err == nil {
		t.Fatal("a genuine scan error must surface")
	}
	if !errors.Is(err, boom) {
		t.Errorf("cause must be wrapped, got %v", err)
	}
	if changed {
		t.Error("changed must be false on error")
	}
}

// The two promote regimes differ only by whether a sequencer is supplied; both
// must reach the same guarded statement with the sequencer passed through
// verbatim, since the SQL itself branches on whether that parameter is empty.
func TestPromoteForwardsBothRegimes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		sequencer string
		source    Source
	}{
		{"HEAD-driven (no sequencer)", "", SourceRPC},
		{"event-driven (sequencer)", "seq-42", SourceEvent},
		{"reconciler", "", SourceReconciler},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sm := &Transitioner{}
			ex := &fakeExec{row: fakeRow{state: string(StateAvailable)}}

			if _, err := sm.promote(smCtx, ex, objID, nil, "e", 1, "c", tc.sequencer, tc.source); err != nil {
				t.Fatalf("promote: %v", err)
			}
			if got := ex.queryArgs[4]; got != tc.sequencer {
				t.Errorf("sequencer arg = %v, want %q", got, tc.sequencer)
			}
		})
	}
}

// ─── softDelete ────────────────────────────────────────────────────────────

func TestSoftDeleteSucceedsWhenARowMatched(t *testing.T) {
	sm := &Transitioner{}
	ex := &fakeExec{tag: tag("UPDATE 1")}

	if err := sm.softDelete(smCtx, ex, objID, 7); err != nil {
		t.Fatalf("softDelete: %v", err)
	}
	if len(ex.execArgs) != 2 {
		t.Fatalf("want 2 exec args, got %v", ex.execArgs)
	}
	if ex.execArgs[0] != objID {
		t.Errorf("object id arg = %v, want %v", ex.execArgs[0], objID)
	}
	// The resource version drives the optimistic-concurrency guard.
	if ex.execArgs[1] != int64(7) {
		t.Errorf("resourceVersion arg = %v, want 7", ex.execArgs[1])
	}
}

// Zero rows means the row was not AVAILABLE or the version moved on — the
// caller must see a conflict, not a silent success.
func TestSoftDeleteZeroRowsIsConflict(t *testing.T) {
	sm := &Transitioner{}
	ex := &fakeExec{tag: tag("UPDATE 0")}

	err := sm.softDelete(smCtx, ex, objID, 7)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}

func TestSoftDeleteVersionZeroSkipsTheCheck(t *testing.T) {
	sm := &Transitioner{}
	ex := &fakeExec{tag: tag("UPDATE 1")}

	if err := sm.softDelete(smCtx, ex, objID, 0); err != nil {
		t.Fatalf("softDelete: %v", err)
	}
	if ex.execArgs[1] != int64(0) {
		t.Errorf("version 0 must reach SQL as 0 (the skip sentinel), got %v", ex.execArgs[1])
	}
}

func TestSoftDeleteWrapsExecErrors(t *testing.T) {
	sm := &Transitioner{}
	boom := errors.New("deadlock detected")
	ex := &fakeExec{execErr: boom}

	err := sm.softDelete(smCtx, ex, objID, 0)
	if !errors.Is(err, boom) {
		t.Fatalf("cause must be wrapped, got %v", err)
	}
	// A driver error must never be mistaken for the 0-row conflict.
	if errors.Is(err, ErrConflict) {
		t.Error("an exec error must not classify as ErrConflict")
	}
}

// ─── restore ───────────────────────────────────────────────────────────────

func TestRestoreSucceedsWhenARowMatched(t *testing.T) {
	sm := &Transitioner{}
	ex := &fakeExec{tag: tag("UPDATE 1")}

	if err := sm.restore(smCtx, ex, objID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(ex.execArgs) != 1 || ex.execArgs[0] != objID {
		t.Errorf("restore must key on the object id alone, got %v", ex.execArgs)
	}
}

// Restore only matches DELETED rows; anything else is "not found", which is
// what the handler maps to a 404 rather than a conflict.
func TestRestoreZeroRowsIsNotFound(t *testing.T) {
	sm := &Transitioner{}
	ex := &fakeExec{tag: tag("UPDATE 0")}

	err := sm.restore(smCtx, ex, objID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if errors.Is(err, ErrConflict) {
		t.Error("restore must not report a version conflict")
	}
}

func TestRestoreWrapsExecErrors(t *testing.T) {
	sm := &Transitioner{}
	boom := errors.New("connection reset")
	ex := &fakeExec{execErr: boom}

	err := sm.restore(smCtx, ex, objID)
	if !errors.Is(err, boom) {
		t.Fatalf("cause must be wrapped, got %v", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Error("an exec error must not classify as ErrNotFound")
	}
}

// ─── sentinels ─────────────────────────────────────────────────────────────

// The two sentinels drive different RPC status codes, so they must stay
// distinguishable from each other and from a wrapped driver failure.
func TestSentinelsAreDistinct(t *testing.T) {
	if errors.Is(ErrConflict, ErrNotFound) || errors.Is(ErrNotFound, ErrConflict) {
		t.Error("ErrConflict and ErrNotFound must not alias")
	}
}

func TestStateAndSourceConstants(t *testing.T) {
	// These strings are the SQL enum labels and the metric dimension values —
	// a rename here silently breaks the UPDATE guards and the dashboards.
	if StatePending != "PENDING" || StateAvailable != "AVAILABLE" ||
		StateDeleted != "DELETED" {
		t.Error("State constants must match the object_state SQL enum")
	}
	if SourceEvent != "event" || SourceRPC != "rpc" || SourceReconciler != "reconciler" {
		t.Error("Source constants must match the metric dimension values")
	}
}

// recordTransition runs on the happy path of every promote; with no meter
// provider configured it must be a harmless no-op rather than a nil deref.
func TestRecordTransitionIsSafeWithoutAMeterProvider(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("recordTransition panicked: %v", r)
		}
	}()
	recordTransition(smCtx, string(SourceEvent), "AVAILABLE")
}
