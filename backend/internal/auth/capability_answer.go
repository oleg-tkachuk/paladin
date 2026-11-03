package auth

import (
	"errors"
	"slices"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// An Exception is how one operation answers a limes code otherwise than
// capabilityStatus does: a parent's revocation is the caller's precondition
// while delegating, and a record the request names is NotFound to the admin
// API. Reason is the one a client tells the answer by; unspecified keeps a
// refusal's own.
type Exception struct {
	Code   connect.Code
	Reason commonv1.ErrorReason
}

// Exceptions are an operation's rows over capabilityStatus, by limes code.
type Exceptions map[limes.Code]Exception

// Malformed answers each of codes — limes refusing the request as malformed
// — with the reason every other malformed request carries.
func Malformed(codes ...limes.Code) Exceptions {
	e := make(Exceptions, len(codes))
	for _, c := range codes {
		e[c] = Exception{Code: connect.CodeInvalidArgument, Reason: commonv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT}
	}
	return e
}

// ListExceptions answer a listing of capability records: a page token the
// store cannot read, or one handed out for another listing, is the caller's
// to fix.
var ListExceptions = Malformed(limes.CodeInvalidRequest, limes.CodeCursorMismatch)

// Undefined is every code e names that limes does not define, sorted; each
// operation's test holds its rows to none.
func (e Exceptions) Undefined() []limes.Code {
	defined := limes.Codes()
	var out []limes.Code
	for c := range e {
		if !slices.Contains(defined, c) {
			out = append(out, c)
		}
	}
	slices.Sort(out)
	return out
}

// capabilityUnanswered is all a caller is told of a dependency failing under
// an operation on capabilities. Unavailable is not scrubbed on the way out,
// so the store's own text stays the error's cause, which the failure log
// carries.
const capabilityUnanswered = "the capability store could not answer; retry"

// AnswerCapabilityError answers err, which an operation on capabilities met,
// by its limes code: the operation's row in except, else capabilityStatus,
// else capabilityUnknownCode — with Unauthenticated answered PermissionDenied,
// since the error is about a record, not the caller's credential. An error already answered — a *connect.Error —
// is returned as it is.
func AnswerCapabilityError(err error, except Exceptions) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*connect.Error](err); ok {
		return err
	}
	if code, ok := limes.CodeOf(err); ok {
		if row, ok := except[code]; ok {
			if row.Reason == commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED {
				return capabilityError(row.Code, err)
			}
			return withCapabilityInfo(connect.NewError(row.Code, err.Error()).WithCause(err), err, row.Reason)
		}
	}
	status := capabilityStatus(err)
	// A handler answers about the record its request names: the caller's own
	// credential the interceptor has already verified. A record revoked or
	// expired is the caller's to be refused, not a credential to replace.
	if status == connect.CodeUnauthenticated {
		status = connect.CodePermissionDenied
	}
	if _, refused := limes.ReasonOf(err); status == connect.CodeUnavailable && !refused {
		return withCapabilityInfo(connect.NewError(status, capabilityUnanswered).WithCause(err), err, commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED)
	}
	return capabilityError(status, err)
}
