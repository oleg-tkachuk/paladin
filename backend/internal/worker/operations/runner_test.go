package operations

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin-private/internal/api/v1/operation"
)

// memRepo is a tiny in-memory operation.Repository sufficient to drive
// the Runner without Postgres. Only the methods the runner exercises
// (ClaimNext, UpdateState) need real behaviour; the rest are stubs.
type memRepo struct {
	mu      sync.Mutex
	pending []operation.Operation
	final   map[uuid.UUID]operation.Operation
}

func newMemRepo() *memRepo {
	return &memRepo{final: map[uuid.UUID]operation.Operation{}}
}

func (m *memRepo) enqueue(op operation.Operation) {
	m.mu.Lock()
	op.State = operation.StatePending
	m.pending = append(m.pending, op)
	m.mu.Unlock()
}

func (m *memRepo) Create(_ context.Context, op operation.Operation) error {
	m.enqueue(op)
	return nil
}
func (m *memRepo) Get(_ context.Context, id uuid.UUID, _ uuid.UUID) (operation.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if op, ok := m.final[id]; ok {
		return op, nil
	}
	return operation.Operation{}, errors.New("not found")
}
func (m *memRepo) UpdateState(_ context.Context, id uuid.UUID, ns operation.State, md, resp []byte, code, msg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.final[id]
	if !ok {
		op = operation.Operation{OperationID: id, Metadata: md}
	}
	op.State = ns
	op.Response = resp
	op.ErrorCode = code
	op.ErrorMessage = msg
	m.final[id] = op
	return nil
}
func (m *memRepo) Cancel(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (m *memRepo) List(context.Context, uuid.UUID, *operation.State, uuid.UUID, int32) ([]operation.Operation, string, error) {
	return nil, "", nil
}
func (m *memRepo) ClaimNext(_ context.Context) (operation.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.pending) == 0 {
		return operation.Operation{}, operation.ErrNoOperationToClaim
	}
	op := m.pending[0]
	m.pending = m.pending[1:]
	op.State = operation.StateRunning
	return op, nil
}

// stubExecutor is a configurable Executor for runner tests.
type stubExecutor struct {
	response []byte
	err      error
	calls    int
}

func (s *stubExecutor) Execute(context.Context, operation.Operation) ([]byte, error) {
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
	repo.enqueue(operation.Operation{OperationID: id, TenantID: uuid.New(), Type: "X"})
	r.drain(context.Background())

	if exec.calls != 1 {
		t.Fatalf("expected 1 exec call, got %d", exec.calls)
	}
	got := repo.final[id]
	if got.State != operation.StateSucceeded {
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
	repo.enqueue(operation.Operation{OperationID: id, TenantID: uuid.New(), Type: "X"})
	r.drain(context.Background())

	got := repo.final[id]
	if got.State != operation.StateFailed {
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
	repo.enqueue(operation.Operation{OperationID: id, TenantID: uuid.New(), Type: "Mystery"})
	r.drain(context.Background())

	got := repo.final[id]
	if got.State != operation.StateFailed {
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
		repo.enqueue(operation.Operation{OperationID: uuid.New(), TenantID: uuid.New(), Type: "X"})
	}
	r.drain(context.Background())
	if exec.calls != 10 {
		t.Fatalf("expected 10 exec calls in one drain, got %d", exec.calls)
	}
}
