package middleware

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/google/uuid"
)

// internalMessage is all a caller is told of a failure on the server's side.
const internalMessage = "internal error"

// requestIDSep joins internalMessage and the request id a caller quotes back.
const requestIDSep = "; request id "

// scrubbedCodes are the codes that report the server failing, whose messages
// carry what the caller has no use for and must not see: a driver's error with
// its table and constraint names, a path, a backend's own answer.
var scrubbedCodes = map[connect.Code]bool{
	connect.CodeInternal: true,
	connect.CodeUnknown:  true,
	connect.CodeDataLoss: true,
}

// ScrubInternal replaces the message of an error that reports the server
// failing — Internal, Unknown, DataLoss — with a generic one naming the
// request id, and keeps its code, its details and its metadata. A request
// that arrives without an id is given one, set on the request before the
// inner interceptors run, so the failure log line and the caller's message
// carry the same id.
//
// Install it outermost: LogOutcome and the tracing interceptor inside it see
// the original error, which is where its detail belongs.
func ScrubInternal() connect.Interceptor { return scrubInterceptor{} }

type scrubInterceptor struct{}

func (scrubInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		id := ensureRequestID(req.Header().Get(HeaderRequestID), req.Header().Set)
		res, err := next(ctx, req)
		return res, scrub(err, id)
	}
}

func (scrubInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (scrubInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		id := ensureRequestID(conn.RequestHeader().Get(HeaderRequestID), conn.RequestHeader().Set)
		return scrub(next(ctx, conn), id)
	}
}

// ensureRequestID is the request's id, minted and set when it carries none.
func ensureRequestID(id string, set func(key, value string)) string {
	if id == "" {
		id = uuid.NewString()
		set(HeaderRequestID, id)
	}
	return id
}

// scrub is err with its message replaced when its code reports the server
// failing, and err unchanged otherwise.
func scrub(err error, requestID string) error {
	if err == nil {
		return nil
	}
	code := connect.CodeOf(err)
	if !scrubbedCodes[code] {
		return err
	}
	out := connect.NewError(code, errors.New(internalMessage+requestIDSep+requestID))
	if cerr := new(connect.Error); errors.As(err, &cerr) {
		// Details are written for the caller — a reason, a retry delay — and
		// carry no driver text; metadata is the response's headers.
		for _, d := range cerr.Details() {
			out.AddDetail(d)
		}
		for k, v := range cerr.Meta() {
			out.Meta()[k] = v
		}
	}
	out.Meta().Set(HeaderRequestID, requestID)
	return out
}
