package auth

import (
	"testing"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/limestest"
	"github.com/oleg-tkachuk/limes/meter"
)

// What reaches the wire for every code a capability's verification and
// metering can fail with: a code a release adds fails here until it is
// answered on purpose, and a change to how codes are answered shows here as
// a changed row. The exchange's codes are left out: its errors are answered
// by its own token endpoint, never by a Connect plane.
func TestCapabilityStatusOfEveryLimesCode(t *testing.T) {
	limestest.CheckMapping(t, map[limes.Code]connect.Code{
		limes.CodeMalformedAuthorization:      connect.CodeInvalidArgument,
		limes.CodeNoCredentials:               connect.CodeUnauthenticated,
		limes.CodeKeysUnavailable:             connect.CodeUnavailable,
		limes.CodeRevocationUnavailable:       connect.CodeUnavailable,
		limes.CodeUnrecorded:                  connect.CodeUnauthenticated,
		limes.CodeProofQuotaExceeded:          connect.CodeResourceExhausted,
		limes.CodeOutcomeUnknown:              connect.CodeUnavailable,
		limes.CodeReserveConflict:             connect.CodeAborted,
		limes.CodeRefundConflict:              connect.CodeAborted,
		limes.CodeNonceRequired:               connect.CodeUnauthenticated,
		limes.CodeProofRequired:               connect.CodeUnauthenticated,
		limes.CodeProofInvalid:                connect.CodeUnauthenticated,
		limes.CodeProofReplayed:               connect.CodeUnauthenticated,
		limes.CodeAuthSchemeMismatch:          connect.CodeUnauthenticated,
		limes.CodeProofOfPossession:           connect.CodeUnauthenticated,
		limes.CodeBiscuitAttenuation:          connect.CodeUnauthenticated,
		limes.CodeInvalidToken:                connect.CodeUnauthenticated,
		limes.CodeExpired:                     connect.CodeUnauthenticated,
		limes.CodeNotYetValid:                 connect.CodeUnauthenticated,
		limes.CodeTenantErased:                connect.CodeUnauthenticated,
		limes.CodeDelegationTooDeep:           connect.CodeUnauthenticated,
		limes.CodeDelegationDepthExceeded:     connect.CodePermissionDenied,
		limes.CodeTenantGone:                  connect.CodeFailedPrecondition,
		limes.CodeRevoked:                     connect.CodeUnauthenticated,
		limes.CodeAudienceMismatch:            connect.CodeUnauthenticated,
		limes.CodeUnauthenticated:             connect.CodeUnauthenticated,
		limes.CodeOpNotAllowed:                connect.CodePermissionDenied,
		limes.CodeResourceNotAllowed:          connect.CodePermissionDenied,
		limes.CodeSourceIPNotAllowed:          connect.CodePermissionDenied,
		limes.CodeIdempotencyKeyRequired:      connect.CodePermissionDenied,
		limes.CodeTaintedReadNotAllowed:       connect.CodePermissionDenied,
		limes.CodeCaveatViolation:             connect.CodePermissionDenied,
		limes.CodeRequestLimitExceeded:        connect.CodeResourceExhausted,
		limes.CodeBudgetExceeded:              connect.CodeResourceExhausted,
		limes.CodeTenantBudgetExceeded:        connect.CodeResourceExhausted,
		limes.CodeUnitCodeMismatch:            connect.CodeFailedPrecondition,
		limes.CodeAlreadyExists:               connect.CodeAlreadyExists,
		limes.CodeConflict:                    connect.CodeAborted,
		limes.CodeInvalidCaveats:              connect.CodeInvalidArgument,
		limes.CodeInvalidAmount:               connect.CodeInvalidArgument,
		limes.CodeEffectConflict:              connect.CodeInvalidArgument,
		limes.CodeCursorMismatch:              connect.CodeInvalidArgument,
		limes.CodeInvalidRequest:              connect.CodeInvalidArgument,
		limes.CodeDelegationTooWide:           connect.CodePermissionDenied,
		limes.CodeUnknownKID:                  connect.CodeUnauthenticated,
		limes.CodeNotFound:                    connect.CodeFailedPrecondition,
		limes.CodeUnknownTenant:               connect.CodeFailedPrecondition,
		limes.CodeTenantDeleted:               connect.CodeFailedPrecondition,
		limes.CodeScopeMismatch:               connect.CodeInternal,
		limes.CodeProofUnsupported:            connect.CodeInternal,
		limes.CodeNoRevocationLineage:         connect.CodeUnavailable,
		limes.CodeCopyCountersNotMetered:      connect.CodeInternal,
		limes.CodeCursorRepeated:              connect.CodeUnavailable,
		limes.CodeInvalidConfig:               connect.CodeInternal,
		limes.CodeInternal:                    connect.CodeInternal,
		meter.CodeCopyLimitExceeded:           connect.CodeResourceExhausted,
		meter.CodeCounterOverflow:             connect.CodeFailedPrecondition,
		meter.CodeChargeNotFound:              connect.CodeFailedPrecondition,
		meter.CodeRefundExceedsCharge:         connect.CodeFailedPrecondition,
		meter.CodeReservationNotFound:         connect.CodeFailedPrecondition,
		meter.CodeUsageNotFound:               connect.CodeFailedPrecondition,
		meter.CodeTenantBudgetNotFound:        connect.CodeFailedPrecondition,
		meter.CodeTenantBudgetVersionMismatch: connect.CodeAborted,
		meter.CodeExternalRefConflict:         connect.CodeAborted,
		meter.CodeSettleConflict:              connect.CodeAborted,
		meter.CodeLeaseExhausted:              connect.CodeResourceExhausted,
		meter.CodeLeaseRequestsExhausted:      connect.CodeResourceExhausted,
		meter.CodeLeaseNotFound:               connect.CodeFailedPrecondition,
		meter.CodeLeaseNested:                 connect.CodeFailedPrecondition,
		meter.CodeLeasingDisabled:             connect.CodeInternal,
		meter.CodeFeatureDisabled:             connect.CodeInternal,
	}, codeStatus, limestest.WithoutExchange())
}
