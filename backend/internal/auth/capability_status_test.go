package auth

import (
	"testing"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/dpop"
	"github.com/oleg-tkachuk/limes/jwks"
	"github.com/oleg-tkachuk/limes/meter"
)

// What reaches the wire for every error limes and its meter name a code for:
// one sentinel per code, so a code a release adds fails here until it is
// answered on purpose, and a change to how codes are answered shows here as
// a changed row.
func TestCapabilityStatusOfEveryLimesCode(t *testing.T) {
	cases := []struct {
		err  error
		want connect.Code
	}{
		{limes.ErrMalformedAuthorization, connect.CodeInvalidArgument},
		{limes.ErrNoCredentials, connect.CodePermissionDenied},
		{jwks.ErrUnavailable, connect.CodeUnavailable},
		{dpop.ErrNonceRequired, connect.CodePermissionDenied},
		{dpop.ErrRequired, connect.CodePermissionDenied},
		{dpop.ErrInvalid, connect.CodePermissionDenied},
		{dpop.ErrReplayed, connect.CodePermissionDenied},
		{limes.ErrAuthSchemeMismatch, connect.CodePermissionDenied},
		{limes.ErrProofOfPossession, connect.CodePermissionDenied},
		{limes.ErrBiscuitAttenuation, connect.CodePermissionDenied},
		{limes.ErrInvalidToken, connect.CodePermissionDenied},
		{limes.ErrExpired, connect.CodePermissionDenied},
		{limes.ErrNotYetValid, connect.CodePermissionDenied},
		{limes.ErrTenantErased, connect.CodePermissionDenied},
		{limes.ErrDelegationTooDeep, connect.CodePermissionDenied},
		{limes.ErrRevoked, connect.CodePermissionDenied},
		{limes.ErrAudienceMismatch, connect.CodePermissionDenied},
		{limes.ErrUnauthenticated, connect.CodePermissionDenied},
		{limes.ErrOpNotAllowed, connect.CodePermissionDenied},
		{limes.ErrResourceNotAllowed, connect.CodePermissionDenied},
		{limes.ErrSourceIPNotAllowed, connect.CodePermissionDenied},
		{limes.ErrIdempotencyKeyRequired, connect.CodePermissionDenied},
		{limes.ErrTaintedReadNotAllowed, connect.CodePermissionDenied},
		{limes.ErrCaveatViolation, connect.CodePermissionDenied},
		{limes.ErrRequestLimitExceeded, connect.CodeResourceExhausted},
		{limes.ErrBudgetExceeded, connect.CodeResourceExhausted},
		{limes.ErrTenantBudgetExceeded, connect.CodeResourceExhausted},
		{limes.ErrUnitCodeMismatch, connect.CodeFailedPrecondition},
		{limes.ErrAlreadyExists, connect.CodeAlreadyExists},
		{limes.ErrConflict, connect.CodeAborted},
		{limes.ErrInvalidCaveats, connect.CodeInvalidArgument},
		{limes.ErrInvalidAmount, connect.CodeInvalidArgument},
		{limes.ErrEffectConflict, connect.CodeInvalidArgument},
		{limes.ErrCursorMismatch, connect.CodeInvalidArgument},
		{limes.ErrInvalidRequest, connect.CodeInvalidArgument},
		{limes.ErrDelegationTooWide, connect.CodePermissionDenied},
		{jwks.ErrUnknownKID, connect.CodePermissionDenied},
		{limes.ErrNotFound, connect.CodeFailedPrecondition},
		{limes.ErrUnknownTenant, connect.CodeFailedPrecondition},
		{limes.ErrTenantDeleted, connect.CodeFailedPrecondition},
		{limes.ErrScopeMismatch, connect.CodeInternal},
		{limes.ErrProofUnsupported, connect.CodeFailedPrecondition},
		{limes.ErrNoRevocationLineage, connect.CodeUnavailable},
		{limes.ErrCopyCountersNotMetered, connect.CodeInternal},
		{limes.ErrCursorRepeated, connect.CodeUnavailable},
		{limes.ErrInvalidConfig, connect.CodeInternal},
		{limes.ErrInternal, connect.CodeInternal},
		{meter.ErrCopyLimitExceeded, connect.CodeResourceExhausted},
		{meter.ErrCounterOverflow, connect.CodeFailedPrecondition},
		{meter.ErrChargeNotFound, connect.CodeFailedPrecondition},
		{meter.ErrRefundExceedsCharge, connect.CodeAborted},
		{meter.ErrReservationNotFound, connect.CodeFailedPrecondition},
		{meter.ErrUsageNotFound, connect.CodeFailedPrecondition},
		{meter.ErrTenantBudgetNotFound, connect.CodeFailedPrecondition},
		{meter.ErrTenantBudgetVersionMismatch, connect.CodeAborted},
		{meter.ErrExternalRefConflict, connect.CodeAborted},
		{meter.ErrSettleConflict, connect.CodeAborted},
		{meter.ErrLeaseExhausted, connect.CodeResourceExhausted},
		{meter.ErrLeaseRequestsExhausted, connect.CodeResourceExhausted},
		{meter.ErrLeaseNotFound, connect.CodeFailedPrecondition},
		{meter.ErrLeaseNested, connect.CodeFailedPrecondition},
		{meter.ErrLeasingDisabled, connect.CodeFailedPrecondition},
	}
	covered := map[limes.Code]bool{}
	for _, tc := range cases {
		code, ok := meter.CodeOf(tc.err)
		if !ok {
			t.Errorf("%v carries no limes code", tc.err)
			continue
		}
		covered[code] = true
		if got := capabilityStatus(tc.err); got != tc.want {
			t.Errorf("%s answers %v, want %v", code, got, tc.want)
		}
	}
	for _, c := range append(limes.Codes(), meter.Codes()...) {
		if !covered[c] {
			t.Errorf("limes code %q has no case here", c)
		}
	}
}
