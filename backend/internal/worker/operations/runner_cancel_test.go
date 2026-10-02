package operations

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
)

// cancelHeartbeat is how often the runner checks the row in these tests, and
// cancelWait how long one waits for the executor to notice.
const (
	cancelHeartbeat = 5 * time.Millisecond
	cancelWait      = 5 * time.Second
)

// finishedToucher reports the operation as no longer RUNNING, the way the
// repository does once it is cancelled or reclaimed.
type finishedToucher struct{}

func (finishedToucher) Touch(context.Context, uuid.UUID) error {
	return operationh.ErrOperationFinished
}

// A cancel used to stop nothing: the executor worked through the rest of the
// batch and its result was dropped. The heartbeat now sees the row is no
// longer RUNNING and stops the executor.
func TestRunOne_ACancelStopsTheExecutor(t *testing.T) {
	repo := &recordingRepo{}
	stopped := make(chan struct{})
	r := &Runner{
		Repo:      repo,
		Toucher:   finishedToucher{},
		Heartbeat: cancelHeartbeat,
		Executors: map[string]Executor{
			"BatchUpdateTags": execFunc(func(ctx context.Context, _ operationh.Operation) ([]byte, error) {
				select {
				case <-ctx.Done():
					close(stopped)
					return nil, ctx.Err()
				case <-time.After(cancelWait):
					return []byte(`{}`), nil
				}
			}),
		},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		r.runOne(context.Background(), operationh.Operation{
			OperationID: uuid.New(), TenantID: uuid.New(), Type: "BatchUpdateTags",
		})
	}()
	select {
	case <-stopped:
	case <-time.After(cancelWait):
		t.Fatal("the executor of a cancelled operation was never stopped")
	}
	<-done
	if got := repo.terminal(); len(got) != 0 {
		t.Errorf("terminal writes = %v, want none: the cancel is the outcome", got)
	}
}
