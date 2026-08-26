package operations

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/operation"
)

// recordingRepo captures terminal writes and refuses any write whose context
// is already cancelled — exactly what Postgres does, and what made the bug
// invisible: the runner logged a warning and moved on.
type recordingRepo struct {
	mu     sync.Mutex
	states []operation.State
	claim  *operation.Operation
	touch  int
}

func (r *recordingRepo) ClaimNext(context.Context) (operation.Operation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.claim == nil {
		return operation.Operation{}, operation.ErrNoOperationToClaim
	}
	op := *r.claim
	r.claim = nil
	return op, nil
}

func (r *recordingRepo) UpdateState(ctx context.Context, _ uuid.UUID, st operation.State, _, _ []byte, _, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states = append(r.states, st)
	return nil
}

func (r *recordingRepo) Touch(ctx context.Context, _ uuid.UUID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.touch++
	return nil
}

func (r *recordingRepo) Create(context.Context, operation.Operation) error { return nil }
func (r *recordingRepo) Get(context.Context, uuid.UUID, uuid.UUID) (operation.Operation, error) {
	return operation.Operation{}, nil
}
func (r *recordingRepo) Cancel(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (r *recordingRepo) List(context.Context, uuid.UUID, *operation.State, uuid.UUID, int32, string) ([]operation.Operation, string, error) {
	return nil, "", nil
}

func (r *recordingRepo) terminal() []operation.State {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]operation.State, len(r.states))
	copy(out, r.states)
	return out
}

// execFunc adapts a function to Executor.
type execFunc func(ctx context.Context, op operation.Operation) ([]byte, error)

func (f execFunc) Execute(ctx context.Context, op operation.Operation) ([]byte, error) {
	return f(ctx, op)
}

// The bug: both terminal writes ran on the runner's own context, so on
// shutdown the write that records the outcome was refused by the very
// cancellation that had just stopped the executor. The row stayed RUNNING,
// and nothing in the system ever looks at a RUNNING row again — one sat that
// way for two days.
func TestRunOne_MarksFailedEvenWhenTheRunnerContextIsCancelled(t *testing.T) {
	repo := &recordingRepo{}
	r := &Runner{
		Repo: repo,
		Executors: map[string]Executor{
			"BatchUpdateTags": execFunc(func(ctx context.Context, _ operation.Operation) ([]byte, error) {
				<-ctx.Done() // the shutdown that cancels the executor
				return nil, ctx.Err()
			}),
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	op := operation.Operation{OperationID: uuid.New(), TenantID: uuid.New(), Type: "BatchUpdateTags"}
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.runOne(ctx, op)
	}()
	cancel()
	<-done

	got := repo.terminal()
	if len(got) != 1 || got[0] != operation.StateFailed {
		t.Fatalf("terminal writes = %v, want exactly [FAILED] — a cancelled runner "+
			"must still record the outcome, or the operation is stuck forever", got)
	}
}

// The success path had the same defect: a shutdown racing a completing
// executor lost the SUCCEEDED write and its response.
func TestRunOne_MarksSucceededEvenWhenTheRunnerContextIsCancelled(t *testing.T) {
	repo := &recordingRepo{}
	r := &Runner{
		Repo: repo,
		Executors: map[string]Executor{
			"BatchUpdateTags": execFunc(func(context.Context, operation.Operation) ([]byte, error) {
				return []byte(`{"total":1}`), nil
			}),
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already shutting down when the executor returns
	r.runOne(ctx, operation.Operation{
		OperationID: uuid.New(), TenantID: uuid.New(), Type: "BatchUpdateTags",
	})

	got := repo.terminal()
	if len(got) != 1 || got[0] != operation.StateSucceeded {
		t.Fatalf("terminal writes = %v, want exactly [SUCCEEDED]", got)
	}
}

// An unknown type is a terminal outcome too, and it must not depend on the
// runner still being alive either.
func TestRunOne_UnknownTypeFailsOnACancelledContext(t *testing.T) {
	repo := &recordingRepo{}
	r := &Runner{Repo: repo, Executors: map[string]Executor{}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.runOne(ctx, operation.Operation{
		OperationID: uuid.New(), TenantID: uuid.New(), Type: "NoSuchType",
	})

	if got := repo.terminal(); len(got) != 1 || got[0] != operation.StateFailed {
		t.Fatalf("terminal writes = %v, want exactly [FAILED]", got)
	}
}

// The heartbeat is what lets the reclaimer tell a long operation from a dead
// worker. Without a Toucher the runner must still work — the seam is
// optional — but with one it has to fire while the executor runs.
func TestRunOne_HeartbeatsWhileTheExecutorWorks(t *testing.T) {
	repo := &recordingRepo{}
	release := make(chan struct{})
	r := &Runner{
		Repo:      repo,
		Toucher:   repo,
		Heartbeat: time.Millisecond,
		Executors: map[string]Executor{
			"BatchUpdateTags": execFunc(func(context.Context, operation.Operation) ([]byte, error) {
				<-release
				return nil, nil
			}),
		},
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		close(release)
	}()
	r.runOne(context.Background(), operation.Operation{
		OperationID: uuid.New(), TenantID: uuid.New(), Type: "BatchUpdateTags",
	})

	repo.mu.Lock()
	n := repo.touch
	repo.mu.Unlock()
	if n == 0 {
		t.Error("no heartbeat during execution — the reclaimer cannot tell this " +
			"operation from one whose worker died")
	}
}
