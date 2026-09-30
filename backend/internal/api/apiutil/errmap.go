package apiutil

import (
	"errors"
	"sync"

	"connectrpc.com/connect"
)

// Canonical domain error sentinels. Store / domain layers wrap these
// (fmt.Errorf("...: %w", apiutil.ErrConflict)) so a single mapping
// table — not per-handler if/else chains — decides the Connect code.
// Handlers return MapError(err); the code is consistent across every
// RPC instead of one RPC calling a version mismatch InvalidArgument and
// another FailedPrecondition.
var (
	ErrNotFound           = errors.New("not found")
	ErrConflict           = errors.New("conflict") // optimistic-concurrency / version mismatch
	ErrAlreadyExists      = errors.New("already exists")
	ErrInvalidArgument    = errors.New("invalid argument")
	ErrPermissionDenied   = errors.New("permission denied")
	ErrFailedPrecondition = errors.New("failed precondition") // e.g. disabled backend, active lock
	ErrUnauthenticated    = errors.New("unauthenticated")
)

// canonical maps each sentinel to its Connect code.
var canonical = map[error]connect.Code{
	ErrNotFound:           connect.CodeNotFound,
	ErrConflict:           connect.CodeAborted,
	ErrAlreadyExists:      connect.CodeAlreadyExists,
	ErrInvalidArgument:    connect.CodeInvalidArgument,
	ErrPermissionDenied:   connect.CodePermissionDenied,
	ErrFailedPrecondition: connect.CodeFailedPrecondition,
	ErrUnauthenticated:    connect.CodeUnauthenticated,
}

// registry holds package-local sentinels registered at init time. A
// package that keeps its own sentinel (object.ErrVersionMismatch, …)
// registers it here instead of forcing apiutil to import — and depend
// on — every handler package (which would cycle).
var (
	registryMu sync.RWMutex
	registry   = map[error]connect.Code{}
)

// RegisterError records that `sentinel` (matched via errors.Is) maps to
// `code`. Call from a package init(). Idempotent; last write wins.
func RegisterError(sentinel error, code connect.Code) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[sentinel] = code
}

// MapError converts a domain error into a *connect.Error with the right
// code. Resolution order:
//   - nil → nil
//   - an err that is already a *connect.Error → returned unchanged (a
//     handler that already chose a precise code wins)
//   - errors.Is against the canonical sentinels, then the registry
//   - default → CodeInternal
//
// The original error is preserved as the connect.Error message/cause so
// no context is lost.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	if connErr := new(connect.Error); errors.As(err, &connErr) {
		return err
	}
	for sentinel, code := range canonical {
		if errors.Is(err, sentinel) {
			return connect.NewError(code, err)
		}
	}
	registryMu.RLock()
	defer registryMu.RUnlock()
	for sentinel, code := range registry {
		if errors.Is(err, sentinel) {
			return connect.NewError(code, err)
		}
	}
	return connect.NewError(connect.CodeInternal, err)
}
