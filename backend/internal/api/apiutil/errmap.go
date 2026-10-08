package apiutil

import (
	"errors"
	"fmt"
	"sync"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
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

// mapping is what a sentinel becomes on the wire: its Connect code, and the
// reason in the google.rpc.ErrorInfo attached to it.
type mapping struct {
	code   connect.Code
	reason commonv1.ErrorReason
}

// canonical maps each sentinel to its code and reason.
var canonical = map[error]mapping{
	ErrNotFound:           {connect.CodeNotFound, commonv1.ErrorReason_ERROR_REASON_NOT_FOUND},
	ErrConflict:           {connect.CodeAborted, commonv1.ErrorReason_ERROR_REASON_VERSION_CONFLICT},
	ErrAlreadyExists:      {connect.CodeAlreadyExists, commonv1.ErrorReason_ERROR_REASON_ALREADY_EXISTS},
	ErrInvalidArgument:    {connect.CodeInvalidArgument, commonv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT},
	ErrPermissionDenied:   {connect.CodePermissionDenied, commonv1.ErrorReason_ERROR_REASON_PERMISSION_DENIED},
	ErrFailedPrecondition: {connect.CodeFailedPrecondition, commonv1.ErrorReason_ERROR_REASON_FAILED_PRECONDITION},
	ErrUnauthenticated:    {connect.CodeUnauthenticated, commonv1.ErrorReason_ERROR_REASON_UNAUTHENTICATED},
}

// registry holds package-local sentinels registered at init time. A
// package that keeps its own sentinel (object.ErrVersionMismatch, …)
// registers it here instead of forcing apiutil to import — and depend
// on — every handler package (which would cycle).
var (
	registryMu sync.RWMutex
	registry   = map[error]mapping{}
)

// RegisterError records that `sentinel` (matched via errors.Is) maps to
// `code`, with `reason` in its ErrorInfo. Call from a package init().
// Idempotent; last write wins.
func RegisterError(sentinel error, code connect.Code, reason commonv1.ErrorReason) {
	if reason == commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED {
		// At init: a sentinel without a reason fails every test of its package.
		panic(fmt.Sprintf("apiutil: %v registered with no reason", sentinel))
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[sentinel] = mapping{code, reason}
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
// no context is lost. A mapped error carries a google.rpc.ErrorInfo with
// the sentinel's reason, so a client need not match on the message.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	if connErr := new(connect.Error); errors.As(err, &connErr) {
		return err
	}
	for sentinel, m := range canonical {
		if errors.Is(err, sentinel) {
			return withReason(connect.NewError(m.code, err.Error()).WithCause(err), m.reason)
		}
	}
	registryMu.RLock()
	defer registryMu.RUnlock()
	for sentinel, m := range registry {
		if errors.Is(err, sentinel) {
			return withReason(connect.NewError(m.code, err.Error()).WithCause(err), m.reason)
		}
	}
	return connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
}

// withReason attaches the ErrorInfo for reason to e.
func withReason(e *connect.Error, reason commonv1.ErrorReason) *connect.Error {
	detail, err := connectproto.NewErrorDetail(&errdetails.ErrorInfo{Reason: reason.String(), Domain: paladin.ErrorDomain})
	if err != nil { // only a message that cannot be marshalled fails, and this one can
		return e
	}
	return e.WithDetail(detail)
}
