package paladin

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/proto"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// The kinds of failure a caller acts on, matched with errors.Is on any error
// a Paladin call returns: errors.Is(err, paladin.ErrNotFound). They follow
// the Connect code, so they hold for an error the server sent no reason
// with; the reason, when there is one, is on the *Error.
var (
	ErrNotFound           = errors.New("paladin: not found")
	ErrAlreadyExists      = errors.New("paladin: already exists")
	ErrPermissionDenied   = errors.New("paladin: permission denied")
	ErrFailedPrecondition = errors.New("paladin: failed precondition")
	// ErrVersionConflict is a write against a resource that changed since it
	// was read: read it again and retry the change.
	ErrVersionConflict   = errors.New("paladin: version conflict")
	ErrResourceExhausted = errors.New("paladin: resource exhausted")
	ErrUnauthenticated   = errors.New("paladin: unauthenticated")
	// ErrContractSkew is a call the server does not implement: a server older
	// than the SDK. The *Error names the procedure and both versions.
	ErrContractSkew = errors.New("paladin: the server does not implement this call")
)

// kinds maps a Connect code to the kind of failure it is.
var kinds = map[connect.Code]error{
	connect.CodeNotFound:           ErrNotFound,
	connect.CodeAlreadyExists:      ErrAlreadyExists,
	connect.CodePermissionDenied:   ErrPermissionDenied,
	connect.CodeFailedPrecondition: ErrFailedPrecondition,
	connect.CodeAborted:            ErrVersionConflict,
	connect.CodeResourceExhausted:  ErrResourceExhausted,
	connect.CodeUnauthenticated:    ErrUnauthenticated,
	connect.CodeUnimplemented:      ErrContractSkew,
}

// Error is a failed Paladin call. It wraps the *connect.Error it came from,
// so connect.CodeOf and errors.As(err, **connect.Error) still work, and it
// matches the kind of failure it is with errors.Is.
type Error struct {
	// Procedure is the RPC that failed, e.g. /paladin.data.v1.ObjectService/GetObject.
	Procedure string
	// Reason is why, from the server's google.rpc.ErrorInfo;
	// ERROR_REASON_UNSPECIFIED when it sent none, or one this SDK predates.
	Reason commonv1.ErrorReason
	// RetryAfter is how long the server asked to wait, from Retry-After;
	// zero when it did not.
	RetryAfter time.Duration
	// ServerVersion is the server's release, from HeaderServerVersion;
	// SDKVersion is this module's. They name both sides of a contract skew.
	ServerVersion string
	SDKVersion    string
	// Details are the error details the server attached, decoded.
	Details []proto.Message

	cause *connect.Error
}

func (e *Error) Error() string {
	if errors.Is(e, ErrContractSkew) {
		return fmt.Sprintf("paladin: %s is not implemented by the server (server %s, sdk %s): %v",
			e.Procedure, orUnknown(e.ServerVersion), e.SDKVersion, e.cause)
	}
	return e.cause.Error()
}

func orUnknown(v string) string {
	if v == "" {
		return "unknown version"
	}
	return v
}

// Unwrap returns the *connect.Error.
func (e *Error) Unwrap() error { return e.cause }

// Is matches the kind of failure, by the error's code.
func (e *Error) Is(target error) bool {
	kind, ok := kinds[e.cause.Code()]
	return ok && kind == target
}

// Code is the Connect code.
func (e *Error) Code() connect.Code { return e.cause.Code() }

// Reason returns the server's reason for err, ERROR_REASON_UNSPECIFIED when
// it is not a Paladin error or carries none.
func Reason(err error) commonv1.ErrorReason {
	var pe *Error
	if errors.As(err, &pe) {
		return pe.Reason
	}
	return commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED
}

// newError wraps a Connect error from procedure; anything else is returned
// as it is.
func newError(procedure string, err error) error {
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		return err
	}
	if errors.As(err, new(*Error)) {
		return err
	}
	e := &Error{
		Procedure:     procedure,
		ServerVersion: cerr.Meta().Get(HeaderServerVersion),
		SDKVersion:    moduleVersion(debug.ReadBuildInfo),
		cause:         cerr,
	}
	if after, ok := retryAfter(err); ok {
		e.RetryAfter = after
	}
	for _, d := range cerr.Details() {
		v, derr := d.Value()
		if derr != nil {
			continue // a detail of a type this binary has not linked
		}
		e.Details = append(e.Details, v)
		if info, ok := v.(*errdetails.ErrorInfo); ok && info.GetDomain() == ErrorDomain {
			e.Reason = commonv1.ErrorReason(commonv1.ErrorReason_value[info.GetReason()])
		}
	}
	return e
}

// errorInterceptor turns every failed unary call's Connect error into an
// *Error. Outermost, so retries and token refresh see the Connect error
// they always have.
type errorInterceptor struct{}

func (errorInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		resp, err := next(ctx, req)
		if err != nil {
			return resp, newError(req.Spec().Procedure, err)
		}
		return resp, nil
	}
}

func (errorInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (errorInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}
