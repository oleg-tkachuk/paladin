package paladin

import (
	"context"

	"connectrpc.com/connect"
)

// Ensure makes a resource exist and returns it, for provisioning that runs at
// every boot: get it; when it is NotFound, create it; when the create meets
// AlreadyExists — another process got there first — get it again. created
// reports whether this call created it. Any other error from either call is
// returned as it is.
//
// Give create a stable idempotency key (WithIdempotencyKey, derived from what
// is being created), so a create retried after a lost response is answered
// from the first rather than meeting AlreadyExists.
func Ensure[T any](ctx context.Context, get, create func(context.Context) (T, error)) (_ T, created bool, _ error) {
	got, err := get(ctx)
	if err == nil {
		return got, false, nil
	}
	var zero T
	if connect.CodeOf(err) != connect.CodeNotFound {
		return zero, false, err
	}
	made, err := create(ctx)
	if err == nil {
		return made, true, nil
	}
	if connect.CodeOf(err) != connect.CodeAlreadyExists {
		return zero, false, err
	}
	got, err = get(ctx)
	if err != nil {
		return zero, false, err
	}
	return got, false, nil
}
