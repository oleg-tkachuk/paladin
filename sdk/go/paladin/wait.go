package paladin

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/proto"
)

// Polling bounds for Wait: the pause starts at the first and doubles to the
// second.
const (
	DefaultPollInterval    = 500 * time.Millisecond
	DefaultMaxPollInterval = 10 * time.Second
)

// Operation is what both planes' long-running operations share.
type Operation interface {
	proto.Message
	GetName() string
	GetDone() bool
	GetError() *status.Status
}

// OperationError is an operation that finished with an error.
type OperationError struct {
	Name   string
	Status *status.Status
}

func (e *OperationError) Error() string {
	return fmt.Sprintf("paladin: operation %s failed: %s: %s",
		e.Name, connect.Code(e.Status.GetCode()), e.Status.GetMessage())
}

// Code is the operation's error as a Connect code.
func (e *OperationError) Code() connect.Code { return connect.Code(e.Status.GetCode()) }

// Wait polls get until the operation is done and returns it. An operation
// that finished with an error returns it as well, with an *OperationError.
// The pause between polls starts at DefaultPollInterval and doubles to
// DefaultMaxPollInterval; ctx bounds the whole wait.
//
//	op, err := paladin.Wait(ctx, func(ctx context.Context) (*datav1.Operation, error) {
//		r, err := p.Data.Operation.GetOperation(ctx, connect.NewRequest(&datav1.GetOperationRequest{Name: name}))
//		if err != nil {
//			return nil, err
//		}
//		return r.Msg, nil
//	})
//
// get must not read a response when the call failed: a Connect client returns
// a nil response with its error. ExampleWait is the same, compiled.
func Wait[Op Operation](ctx context.Context, get func(context.Context) (Op, error)) (Op, error) {
	return waitWith(ctx, get, DefaultPollInterval, DefaultMaxPollInterval)
}

func waitWith[Op Operation](ctx context.Context, get func(context.Context) (Op, error), first, ceiling time.Duration) (Op, error) {
	pause := first
	for {
		op, err := get(ctx)
		if err != nil {
			return op, err
		}
		if op.GetDone() {
			if op.GetError() != nil {
				return op, &OperationError{Name: op.GetName(), Status: op.GetError()}
			}
			return op, nil
		}
		timer := time.NewTimer(pause)
		select {
		case <-ctx.Done():
			timer.Stop()
			return op, ctx.Err()
		case <-timer.C:
		}
		pause = min(pause*2, ceiling)
	}
}
