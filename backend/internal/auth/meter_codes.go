package auth

import (
	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
)

// meterCodes is the transport code of a metering error, by its limes code
// (limes.CodeOf, meter.CodeOf): one row per code limes and its meter define,
// which TestMeterCodesCoverEveryLimesCode holds, so a code a release adds is
// answered on purpose. An error that carries no code is the store failing,
// answered as meterUnknownCode.
var meterCodes = map[limes.Code]connect.Code{
	// A spent request, budget or copy limit: the caller waits or stops.
	limes.CodeBudgetExceeded:       connect.CodeResourceExhausted,
	meter.CodeCopyLimitExceeded:    connect.CodeResourceExhausted,
	limes.CodeRequestLimitExceeded: connect.CodeResourceExhausted,
	limes.CodeTenantBudgetExceeded: connect.CodeResourceExhausted,

	// The capability is no longer valid: told so with its reason, as a
	// verifier's refusal is.
	limes.CodeDelegationTooDeep: connect.CodePermissionDenied,
	limes.CodeExpired:           connect.CodePermissionDenied,
	limes.CodeRevoked:           connect.CodePermissionDenied,
	limes.CodeTenantErased:      connect.CodePermissionDenied,

	// A malformed request.
	limes.CodeCursorMismatch:         connect.CodeInvalidArgument,
	limes.CodeEffectConflict:         connect.CodeInvalidArgument,
	limes.CodeInvalidAmount:          connect.CodeInvalidArgument,
	limes.CodeInvalidCaveats:         connect.CodeInvalidArgument,
	limes.CodeInvalidRequest:         connect.CodeInvalidArgument,
	limes.CodeMalformedAuthorization: connect.CodeInvalidArgument,

	// A unit other than the counters', a counter with no room, a record no
	// longer there.
	meter.CodeChargeNotFound:       connect.CodeFailedPrecondition,
	meter.CodeCounterOverflow:      connect.CodeFailedPrecondition,
	limes.CodeNotFound:             connect.CodeFailedPrecondition,
	meter.CodeReservationNotFound:  connect.CodeFailedPrecondition,
	meter.CodeTenantBudgetNotFound: connect.CodeFailedPrecondition,
	limes.CodeUnitCodeMismatch:     connect.CodeFailedPrecondition,
	meter.CodeUsageNotFound:        connect.CodeFailedPrecondition,

	// A request that disagrees with what is on record: a reservation settled
	// for another amount, an external reference naming another charge.
	limes.CodeAlreadyExists:               connect.CodeAborted,
	limes.CodeConflict:                    connect.CodeAborted,
	meter.CodeExternalRefConflict:         connect.CodeAborted,
	meter.CodeSettleConflict:              connect.CodeAborted,
	meter.CodeTenantBudgetVersionMismatch: connect.CodeAborted,

	// What the meter does not answer with on this path, and the server's own
	// failures: the caller retries rather than drops a capability that may be
	// sound.
	limes.CodeAudienceMismatch:       connect.CodeUnavailable,
	limes.CodeAuthSchemeMismatch:     connect.CodeUnavailable,
	limes.CodeBiscuitAttenuation:     connect.CodeUnavailable,
	limes.CodeCaveatViolation:        connect.CodeUnavailable,
	limes.CodeCopyCountersNotMetered: connect.CodeUnavailable,
	limes.CodeCursorRepeated:         connect.CodeUnavailable,
	limes.CodeDelegationTooWide:      connect.CodeUnavailable,
	limes.CodeIdempotencyKeyRequired: connect.CodeUnavailable,
	limes.CodeInternal:               connect.CodeUnavailable,
	limes.CodeInvalidConfig:          connect.CodeUnavailable,
	limes.CodeInvalidToken:           connect.CodeUnavailable,
	limes.CodeKeysUnavailable:        connect.CodeUnavailable,
	limes.CodeNoCredentials:          connect.CodeUnavailable,
	limes.CodeNoRevocationLineage:    connect.CodeUnavailable,
	limes.CodeNonceRequired:          connect.CodeUnavailable,
	limes.CodeNotYetValid:            connect.CodeUnavailable,
	limes.CodeOpNotAllowed:           connect.CodeUnavailable,
	limes.CodeProofInvalid:           connect.CodeUnavailable,
	limes.CodeProofOfPossession:      connect.CodeUnavailable,
	limes.CodeProofReplayed:          connect.CodeUnavailable,
	limes.CodeProofRequired:          connect.CodeUnavailable,
	limes.CodeProofUnsupported:       connect.CodeUnavailable,
	meter.CodeRefundExceedsCharge:    connect.CodeUnavailable,
	limes.CodeResourceNotAllowed:     connect.CodeUnavailable,
	limes.CodeScopeMismatch:          connect.CodeUnavailable,
	limes.CodeSourceIPNotAllowed:     connect.CodeUnavailable,
	limes.CodeTaintedReadNotAllowed:  connect.CodeUnavailable,
	limes.CodeTenantDeleted:          connect.CodeUnavailable,
	limes.CodeUnauthenticated:        connect.CodeUnavailable,
	limes.CodeUnknownKID:             connect.CodeUnavailable,
	limes.CodeUnknownTenant:          connect.CodeUnavailable,
}

// meterUnknownCode answers a metering error that carries no limes code: the
// store failing, which the caller retries.
const meterUnknownCode = connect.CodeUnavailable
