package auth

import (
	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
)

// capabilityCategoryCodes is the transport code of an error verifying or
// metering a presented capability, by the category limes files its code
// under (limes.CategoryOf, meter.CategoryOf). Every code belongs to one, so a
// code a release adds is answered by its category; capabilityCodeOverrides
// holds the codes Paladin answers otherwise. Unavailable is kept for what a
// retry can mend.
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
	// A sound request the state it needs is not there for — the store's
	// configuration, the ledger, another capability's lease: no retry of
	// the same call mends it.
	limes.CategoryPrecondition: connect.CodeFailedPrecondition,
	// A dependency failing, which a retry may find recovered.
	limes.CategoryUnavailable: connect.CodeUnavailable,
	// The server's own fault, which no retry mends.
	limes.CategoryInternal: connect.CodeInternal,
}

// capabilityCodeOverrides are the codes Paladin answers apart from their
// category.
var capabilityCodeOverrides = map[limes.Code]connect.Code{
	// A credential the request could not even read: the request is malformed.
	limes.CodeMalformedAuthorization: connect.CodeInvalidArgument,
	// A refund beyond its charge disagrees with the charge on record.
	meter.CodeRefundExceedsCharge: connect.CodeAborted,
	// A record the request would create is there already.
	limes.CodeAlreadyExists: connect.CodeAlreadyExists,
}

// capabilityUnknownCode answers an error that carries no limes code, or a
// category capabilityCategoryCodes has no row for: the store failing, which
// the caller retries.
const capabilityUnknownCode = connect.CodeUnavailable

// capabilityStatus is err's transport code: its limes code's, or
// capabilityUnknownCode when err carries none.
func capabilityStatus(err error) connect.Code {
	code, ok := meter.CodeOf(err)
	if !ok {
		return capabilityUnknownCode
	}
	return codeStatus(code)
}

// codeStatus is code's override, else its category's row, else
// capabilityUnknownCode.
func codeStatus(code limes.Code) connect.Code {
	if status, ok := capabilityCodeOverrides[code]; ok {
		return status
	}
	if category, ok := meter.CategoryOfCode(code); ok {
		if status, ok := capabilityCategoryCodes[category]; ok {
			return status
		}
	}
	return capabilityUnknownCode
}
