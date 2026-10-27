package operations

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
)

// memRepo is a tiny in-memory operation.Repository sufficient to drive
// the Runner without Postgres. Only the methods the runner exercises
// (ClaimNext, UpdateState) need real behaviour; the rest are stubs.
type memRepo struct {
	mu      sync.Mutex
	pending []operationh.Operation
	final   map[uuid.UUID]operationh.Operation
}

func newMemRepo() *memRepo {
	return &memRepo{final: map[uuid.UUID]operationh.Operation{}}
}

func (m *memRepo) enqueue(op operationh.Operation) {
	m.mu.Lock()
	op.State = operationh.StatePending
	m.pending = append(m.pending, op)
	m.mu.Unlock()
}

func (m *memRepo) Create(_ context.Context, op operationh.Operation) error {
	m.enqueue(op)
	return nil
}
func (m *memRepo) Get(_ context.Context, id uuid.UUID, _ uuid.UUID) (operationh.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if op, ok := m.final[id]; ok {
		return op, nil
	}
	return operationh.Operation{}, errors.New("not found")
}
func (m *memRepo) UpdateState(_ context.Context, id uuid.UUID, ns operationh.State, resp []byte, failed int64, code, msg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.final[id]
	if !ok {
		op = operationh.Operation{OperationID: id}
	}
	op.State = ns
	op.Response = resp
	op.FailedCount = failed
	op.ErrorCode = code
	op.ErrorMessage = msg
	m.final[id] = op
	return nil
}
func (m *memRepo) ReportProgress(_ context.Context, id uuid.UUID, progress []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.final[id]
	if !ok {
		op = operationh.Operation{OperationID: id, State: operationh.StateRunning}
	}
	op.Progress = progress
	m.final[id] = op
	return nil
}
func (m *memRepo) Cancel(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (m *memRepo) Dismiss(context.Context, uuid.UUID, string, []uuid.UUID) (int64, error) {
	return 0, nil
}
func (m *memRepo) List(context.Context, operationh.ListArgs) ([]operationh.Operation, string, error) {
	return nil, "", nil
}
func (m *memRepo) CountFacets(context.Context, operationh.ListArgs) ([]operationh.FacetCount, error) {
	return nil, nil
}
func (m *memRepo) ClaimNext(_ context.Context) (operationh.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.pending) == 0 {
		return operationh.Operation{}, operationh.ErrNoOperationToClaim
	}
	op := m.pending[0]
	m.pending = m.pending[1:]
	op.State = operationh.StateRunning
	return op, nil
}

// stubExecutor is a configurable Executor for runner tests.
type stubExecutor struct {
	response []byte
	err      error
	calls    int
}

func (s *stubExecutor) Execute(context.Context, operationh.Operation) ([]byte, error) {
	s.calls++
	return s.response, s.err
}

// TestRunner_DispatchSucceeded confirms a successful execution flips
// the row to SUCCEEDED with the response payload attached.
func TestRunner_DispatchSucceeded(t *testing.T) {
	t.Parallel()
	repo := newMemRepo()
	exec := &stubExecutor{response: []byte(`{"ok":true}`)}
	r := &Runner{
		Repo:      repo,
		Executors: map[string]Executor{"X": exec},
		Interval:  time.Millisecond,
	}

	id := uuid.New()
	repo.enqueue(operationh.Operation{OperationID: id, TenantID: uuid.New(), Type: "X"})
	r.drain(context.Background())

	if exec.calls != 1 {
		t.Fatalf("expected 1 exec call, got %d", exec.calls)
	}
	got := repo.final[id]
	if got.State != operationh.StateSucceeded {
		t.Errorf("state: got %v, want SUCCEEDED", got.State)
	}
	if string(got.Response) != `{"ok":true}` {
		t.Errorf("response: got %q", got.Response)
	}
}

// TestRunner_DispatchFailure flips to FAILED with the executor's
// error preserved on ErrorMessage.
func TestRunner_DispatchFailure(t *testing.T) {
	t.Parallel()
	repo := newMemRepo()
	exec := &stubExecutor{err: errors.New("boom")}
	r := &Runner{
		Repo:      repo,
		Executors: map[string]Executor{"X": exec},
		Interval:  time.Millisecond,
	}
	id := uuid.New()
	repo.enqueue(operationh.Operation{OperationID: id, TenantID: uuid.New(), Type: "X"})
	r.drain(context.Background())

	got := repo.final[id]
	if got.State != operationh.StateFailed {
		t.Errorf("state: got %v, want FAILED", got.State)
	}
	if got.ErrorCode != "EXEC_FAILED" {
		t.Errorf("code: got %q, want EXEC_FAILED", got.ErrorCode)
	}
	if got.ErrorMessage != "boom" {
		t.Errorf("message: got %q, want boom", got.ErrorMessage)
	}
}

// TestRunner_UnknownType marks UNKNOWN_TYPE-coded FAILED for ops the
// registry doesn't have an executor for. Critical: without this an
// unrecognised op type would stay PENDING forever (the original
// "stuck operation" bug this whole package fixes).
func TestRunner_UnknownType(t *testing.T) {
	t.Parallel()
	repo := newMemRepo()
	r := &Runner{
		Repo:      repo,
		Executors: map[string]Executor{},
		Interval:  time.Millisecond,
	}
	id := uuid.New()
	repo.enqueue(operationh.Operation{OperationID: id, TenantID: uuid.New(), Type: "Mystery"})
	r.drain(context.Background())

	got := repo.final[id]
	if got.State != operationh.StateFailed {
		t.Fatalf("state: got %v, want FAILED", got.State)
	}
	if got.ErrorCode != "UNKNOWN_TYPE" {
		t.Errorf("code: got %q, want UNKNOWN_TYPE", got.ErrorCode)
	}
	// The response payload is JSON with the same code/message — check
	// shape so polling clients can rely on it.
	var resp map[string]string
	if err := json.Unmarshal(got.Response, &resp); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}
	if resp["code"] != "UNKNOWN_TYPE" {
		t.Errorf("response code: got %q", resp["code"])
	}
}

// TestRunner_DrainBatch confirms drain processes all pending ops in a
// single tick (not just one) — so a backlog burns down quickly.
func TestRunner_DrainBatch(t *testing.T) {
	t.Parallel()
	repo := newMemRepo()
	exec := &stubExecutor{response: []byte(`ok`)}
	r := &Runner{
		Repo:      repo,
		Executors: map[string]Executor{"X": exec},
		Interval:  time.Millisecond,
	}
	for i := 0; i < 10; i++ {
		repo.enqueue(operationh.Operation{OperationID: uuid.New(), TenantID: uuid.New(), Type: "X"})
	}
	r.drain(context.Background())
	if exec.calls != 10 {
		t.Fatalf("expected 10 exec calls in one drain, got %d", exec.calls)
	}
}

// progressExecutor reports one snapshot and finishes.
type progressExecutor struct{ processed, total int }

func (p progressExecutor) Execute(ctx context.Context, _ operationh.Operation) ([]byte, error) {
	ReportProgress(ctx, p.processed, p.total)
	return []byte(`{}`), nil
}

// Progress goes to its own column through ReportProgress. It used to be an
// UpdateState that overwrote metadata — the executor's arguments — with the
// snapshot.
func TestRunner_ProgressIsReportedApartFromState(t *testing.T) {
	t.Parallel()
	repo := newMemRepo()
	r := &Runner{
		Repo:      repo,
		Executors: map[string]Executor{"X": progressExecutor{processed: 2, total: 2}},
		Interval:  time.Millisecond,
	}
	id := uuid.New()
	repo.enqueue(operationh.Operation{OperationID: id, TenantID: uuid.New(), Type: "X"})
	r.drain(context.Background())

	got := repo.final[id]
	if string(got.Progress) != `{"processed":2,"total":2}` {
		t.Errorf("progress = %q, want the executor's snapshot", got.Progress)
	}
	if got.State != operationh.StateSucceeded {
		t.Errorf("state = %s, want SUCCEEDED", got.State)
	}
}

// A batch that runs to its end succeeds whatever it could not do: the runner
// records the objects its response says it could not act on beside it, and
// a response that is no batch's counts none.
func TestRunner_RecordsTheObjectsABatchCouldNotActOn(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		response string
		want     int64
	}{
		"a batch with failures": {`{"total":2,"succeeded":1,"failed":1}`, 1},
		"a clean batch":         {`{"total":2,"succeeded":2,"failed":0}`, 0},
		"not a batch":           {`{"ok":true}`, 0},
	} {
		t.Run(name, func(t *testing.T) {
			repo := newMemRepo()
			r := &Runner{Repo: repo, Executors: map[string]Executor{"X": &stubExecutor{response: []byte(tc.response)}}, Interval: time.Millisecond}
			id := uuid.New()
			repo.enqueue(operationh.Operation{OperationID: id, TenantID: uuid.New(), Type: "X"})
			r.drain(context.Background())
			if got := repo.final[id]; got.State != operationh.StateSucceeded || got.FailedCount != tc.want {
				t.Errorf("state %v, failed %d; want SUCCEEDED, %d", got.State, got.FailedCount, tc.want)
			}
		})
	}
}
