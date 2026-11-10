package auth

import (
	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
)

// capabilityCategoryCodes is the transport code of an error verifying or
// metering a presented capability, by the category limes files its code
// under (limes.CategoryOf). Every code belongs to one, so a
// code a release adds is answered by its category. Unavailable is kept for
// what a retry can mend.
var capabilityCategoryCodes = map[limes.Category]connect.Code{
	// The credential presented is missing, malformed, unproven or no longer
	// good — expired, revoked, past its delegation depth: present another.
	limes.CategoryUnauthenticated: connect.CodeUnauthenticated,
	// The credential is good and does not allow the call: told so with its
	// reason. No retry mends it.
	limes.CategoryDenied: connect.CodePermissionDenied,
	// A spent request, budget, copy or lease limit: the caller waits or stops.
	limes.CategoryExhausted: connect.CodeResourceExhausted,
	// A malformed request.
	limes.CategoryInvalid: connect.CodeInvalidArgument,
	// A record or a tenant no longer there: the request is sound, the state
	// it needs is gone.
	limes.CategoryNotFound: connect.CodeFailedPrecondition,
	// A request that disagrees with what is on record: read it again.
	limes.CategoryConflict: connect.CodeAborted,
	// A record the request would create is there already.
	limes.CategoryAlreadyExists: connect.CodeAlreadyExists,
	// A sound request the state it needs is not there for — the store's
	// configuration, the ledger, another capability's lease: no retry of
	// the same call mends it.
	limes.CategoryPrecondition: connect.CodeFailedPrecondition,
	// A dependency failing, which a retry may find recovered.
	limes.CategoryUnavailable: connect.CodeUnavailable,
	// The server's own fault, which no retry mends.
	limes.CategoryInternal: connect.CodeInternal,
}

// capabilityUnknownCode answers an error that carries no limes code, or a
// category capabilityCategoryCodes has no row for: the server's own fault.
// limes codes its store's failures itself (store_unavailable,
// store_timeout), so an error with no code is no dependency a retry mends.
const capabilityUnknownCode = connect.CodeInternal

// capabilityStatus is err's transport code: its limes code's, or
// capabilityUnknownCode when err carries none.
func capabilityStatus(err error) connect.Code {
	code, ok := limes.CodeOf(err)
	if !ok {
		return capabilityUnknownCode
	}
	return codeStatus(code)
}

// codeStatus is code's category's row, else capabilityUnknownCode.
func codeStatus(code limes.Code) connect.Code {
	if category, ok := limes.CategoryOfCode(code); ok {
		if status, ok := capabilityCategoryCodes[category]; ok {
			return status
		}
	}
	return capabilityUnknownCode
}
