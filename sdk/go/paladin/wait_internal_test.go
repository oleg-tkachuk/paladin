package paladin

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/genproto/googleapis/rpc/status"

	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
)

// testPoll keeps Wait's polling fast.
const testPoll = time.Millisecond

// script answers each poll with the next operation, repeating the last.
func script(ops ...*datav1.Operation) (func(context.Context) (*datav1.Operation, error), *int) {
	calls := 0
	return func(context.Context) (*datav1.Operation, error) {
		op := ops[min(calls, len(ops)-1)]
		calls++
		return op, nil
	}, &calls
}

func TestWaitPollsUntilDone(t *testing.T) {
	get, calls := script(&datav1.Operation{Name: "op"}, &datav1.Operation{Name: "op"}, &datav1.Operation{Name: "op", Done: true})
	op, err := waitWith(context.Background(), get, testPoll, testPoll)
	if err != nil || !op.GetDone() {
		t.Fatalf("Wait = %v, %v", op, err)
	}
	if *calls != 3 {
		t.Errorf("polls = %d, want 3", *calls)
	}
}

func TestWaitReturnsTheOperationsError(t *testing.T) {
	failed := &datav1.Operation{Name: "op", Done: true, Result: &datav1.Operation_Error{
		Error: &status.Status{Code: int32(connect.CodeFailedPrecondition), Message: "bucket is not empty"},
	}}
	get, _ := script(failed)
	op, err := waitWith(context.Background(), get, testPoll, testPoll)
	var opErr *OperationError
	if !errors.As(err, &opErr) || opErr.Code() != connect.CodeFailedPrecondition || op != failed {
		t.Fatalf("Wait = %v, %v; want the operation and its FailedPrecondition", op, err)
	}
}

func TestWaitStopsWithTheContext(t *testing.T) {
	get, _ := script(&datav1.Operation{Name: "op"})
	ctx, cancel := context.WithTimeout(context.Background(), 20*testPoll)
	defer cancel()
	if _, err := waitWith(ctx, get, testPoll, testPoll); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
}

func TestWaitPassesThePollError(t *testing.T) {
	boom := connect.NewError(connect.CodeNotFound, "no such operation")
	get := func(context.Context) (*datav1.Operation, error) { return nil, boom }
	if _, err := waitWith(context.Background(), get, testPoll, testPoll); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the poll's error", err)
	}
}
