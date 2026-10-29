package auth

import (
	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
)

// capabilityCodes is the transport code of an error verifying or metering a
// presented capability, by its limes code (limes.CodeOf, meter.CodeOf): one
// row per code limes and its meter define,
// which TestCapabilityCodesCoverEveryLimesCode holds, so a code a release adds is
// answered on purpose. Unavailable is kept for what a retry can mend.
var capabilityCodes = map[limes.Code]connect.Code{
	// A spent request, budget or copy limit: the caller waits or stops.
	limes.CodeBudgetExceeded:       connect.CodeResourceExhausted,
	meter.CodeCopyLimitExceeded:    connect.CodeResourceExhausted,
	limes.CodeRequestLimitExceeded: connect.CodeResourceExhausted,
	limes.CodeTenantBudgetExceeded: connect.CodeResourceExhausted,

	// The capability, or the credential presenting it, does not allow the
	// call: told so with its reason, as a verifier's refusal is. No retry
	// mends it.
	limes.CodeAudienceMismatch:       connect.CodePermissionDenied,
	limes.CodeAuthSchemeMismatch:     connect.CodePermissionDenied,
	limes.CodeBiscuitAttenuation:     connect.CodePermissionDenied,
	limes.CodeCaveatViolation:        connect.CodePermissionDenied,
	limes.CodeDelegationTooDeep:      connect.CodePermissionDenied,
	limes.CodeDelegationTooWide:      connect.CodePermissionDenied,
	limes.CodeExpired:                connect.CodePermissionDenied,
	limes.CodeIdempotencyKeyRequired: connect.CodePermissionDenied,
	limes.CodeInvalidToken:           connect.CodePermissionDenied,
	limes.CodeNoCredentials:          connect.CodePermissionDenied,
	limes.CodeNonceRequired:          connect.CodePermissionDenied,
	limes.CodeNotYetValid:            connect.CodePermissionDenied,
	limes.CodeOpNotAllowed:           connect.CodePermissionDenied,
	limes.CodeProofInvalid:           connect.CodePermissionDenied,
	limes.CodeProofOfPossession:      connect.CodePermissionDenied,
	limes.CodeProofReplayed:          connect.CodePermissionDenied,
	limes.CodeProofRequired:          connect.CodePermissionDenied,
	limes.CodeResourceNotAllowed:     connect.CodePermissionDenied,
	limes.CodeRevoked:                connect.CodePermissionDenied,
	limes.CodeSourceIPNotAllowed:     connect.CodePermissionDenied,
	limes.CodeTaintedReadNotAllowed:  connect.CodePermissionDenied,
	limes.CodeTenantErased:           connect.CodePermissionDenied,
	limes.CodeUnauthenticated:        connect.CodePermissionDenied,
	limes.CodeUnknownKID:             connect.CodePermissionDenied,

	// A malformed request.
	limes.CodeCursorMismatch:         connect.CodeInvalidArgument,
	limes.CodeEffectConflict:         connect.CodeInvalidArgument,
	limes.CodeInvalidAmount:          connect.CodeInvalidArgument,
	limes.CodeInvalidCaveats:         connect.CodeInvalidArgument,
	limes.CodeInvalidRequest:         connect.CodeInvalidArgument,
	limes.CodeMalformedAuthorization: connect.CodeInvalidArgument,

	// A unit other than the counters', a counter with no room, a record or a
	// tenant no longer there; and a key-bound capability on a plane that
	// checks no proof, the deployment's fault, as the verify path answers it.
	meter.CodeChargeNotFound:       connect.CodeFailedPrecondition,
	meter.CodeCounterOverflow:      connect.CodeFailedPrecondition,
	limes.CodeNotFound:             connect.CodeFailedPrecondition,
	limes.CodeProofUnsupported:     connect.CodeFailedPrecondition,
	meter.CodeReservationNotFound:  connect.CodeFailedPrecondition,
	meter.CodeTenantBudgetNotFound: connect.CodeFailedPrecondition,
	limes.CodeTenantDeleted:        connect.CodeFailedPrecondition,
	limes.CodeUnitCodeMismatch:     connect.CodeFailedPrecondition,
	limes.CodeUnknownTenant:        connect.CodeFailedPrecondition,
	meter.CodeUsageNotFound:        connect.CodeFailedPrecondition,

	// A record the request would create is there already.
	limes.CodeAlreadyExists: connect.CodeAlreadyExists,

	// A request that disagrees with what is on record: a reservation settled
	// for another amount, a refund beyond its charge, an external reference
	// naming another charge.
	limes.CodeConflict:                    connect.CodeAborted,
	meter.CodeExternalRefConflict:         connect.CodeAborted,
	meter.CodeRefundExceedsCharge:         connect.CodeAborted,
	meter.CodeSettleConflict:              connect.CodeAborted,
	meter.CodeTenantBudgetVersionMismatch: connect.CodeAborted,

	// The server's own fault, which no retry mends: a misconfiguration, a
	// call scoped to the wrong tenant, a broken invariant.
	limes.CodeCopyCountersNotMetered: connect.CodeInternal,
	limes.CodeInternal:               connect.CodeInternal,
	limes.CodeInvalidConfig:          connect.CodeInternal,
	limes.CodeScopeMismatch:          connect.CodeInternal,

	// A dependency failing — the keys, the store, the revocation upstream —
	// which a retry may find recovered.
	limes.CodeCursorRepeated:      connect.CodeUnavailable,
	limes.CodeKeysUnavailable:     connect.CodeUnavailable,
	limes.CodeNoRevocationLineage: connect.CodeUnavailable,
}

// capabilityUnknownCode answers an error that carries no limes code, or a code
// capabilityCodes has no row for: the store failing, which the caller retries.
const capabilityUnknownCode = connect.CodeUnavailable

// capabilityStatus is err's row in table, or capabilityUnknownCode when err carries no
// limes code or its code has none.
func capabilityStatus(table map[limes.Code]connect.Code, err error) connect.Code {
	code, ok := meter.CodeOf(err)
	if !ok {
		return capabilityUnknownCode
	}
	if status, ok := table[code]; ok {
		return status
	}
	return capabilityUnknownCode
}
