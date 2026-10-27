package middleware

import (
	"context"
	"errors"
	"strings"
	"uuid"

	"connectrpc.com/connect/v2"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary"
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

// libraryPrefixes are what the server's own libraries begin their errors
// with. They name an implementation rather than anything the caller sent or
// can act on, so a message reaches the caller without them.
var libraryPrefixes = []string{"limes: "}

// ScrubInternal replaces the message of an error that reports the server
// failing — Internal, Unknown, DataLoss — with a generic one naming the
// request id, and keeps its code, its details and its metadata. Any other
// error keeps its message, without the names of the server's libraries. A request
// that arrives without an id is given one, set on the request before the
// inner interceptors run, so the failure log line and the caller's message
// carry the same id.
//
// Install it outermost: LogOutcome and the tracing interceptor inside it see
// the original error, which is where its detail belongs.
func ScrubInternal() connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			info := unary.Info(ctx)
			id := ensureRequestID(info.RequestHeader().Get(HeaderRequestID), info.RequestHeader().Set)
			err := scrub(next(ctx, spec, stream), id)
			if err != nil && scrubbedCodes[connect.CodeOf(err)] {
				info.ResponseHeader().Set(HeaderRequestID, id)
			}
			return err
		}
	}
}

// ensureRequestID is the request's id, minted and set when it carries none.
func ensureRequestID(id string, set func(key, value string)) string {
	if id == "" {
		id = uuid.New().String()
		set(HeaderRequestID, id)
	}
	return id
}

// scrub is err with its message replaced when its code reports the server
// failing, and with the server's library names taken out of it otherwise.
func scrub(err error, requestID string) error {
	if err == nil {
		return nil
	}
	code := connect.CodeOf(err)
	cerr := new(connect.Error)
	isConnect := errors.As(err, &cerr)
	if !scrubbedCodes[code] {
		if !isConnect {
			return err
		}
		msg := withoutLibraryNames(cerr.Message())
		if msg == cerr.Message() {
			return err
		}
		return withDetailsOf(connect.NewError(code, msg).WithCause(err), cerr)
	}
	out := connect.NewError(code, internalMessage+requestIDSep+requestID).WithCause(err)
	if isConnect {
		out = withDetailsOf(out, cerr)
	}
	return out
}

// withDetailsOf is out with from's details. Details are written for the
// caller — a reason, a retry delay — and carry no driver text. Metadata is
// the response's headers, on the call's CallInfo, and is left as it is.
func withDetailsOf(out, from *connect.Error) *connect.Error {
	for _, d := range from.Details() {
		out = out.WithDetail(d)
	}
	return out
}

// withoutLibraryNames is msg without the prefixes the server's libraries put
// on their errors, wherever a wrap left them.
func withoutLibraryNames(msg string) string {
	for _, prefix := range libraryPrefixes {
		msg = strings.ReplaceAll(msg, prefix, "")
	}
	return msg
}
