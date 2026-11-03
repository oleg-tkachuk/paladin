package auth

import (
	"testing"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
)

// What reaches the wire for every code limes and its meter define: a code a
// release adds fails here until it is answered on purpose, and a change to
// how codes are answered shows here as a changed row.
func TestCapabilityStatusOfEveryLimesCode(t *testing.T) {
	cases := []struct {
		code limes.Code
		want connect.Code
	}{
		{limes.CodeMalformedAuthorization, connect.CodeInvalidArgument},
		{limes.CodeNoCredentials, connect.CodeUnauthenticated},
		{limes.CodeKeysUnavailable, connect.CodeUnavailable},
		{limes.CodeNonceRequired, connect.CodeUnauthenticated},
		{limes.CodeProofRequired, connect.CodeUnauthenticated},
		{limes.CodeProofInvalid, connect.CodeUnauthenticated},
		{limes.CodeProofReplayed, connect.CodeUnauthenticated},
		{limes.CodeAuthSchemeMismatch, connect.CodeUnauthenticated},
		{limes.CodeProofOfPossession, connect.CodeUnauthenticated},
		{limes.CodeBiscuitAttenuation, connect.CodeUnauthenticated},
		{limes.CodeInvalidToken, connect.CodeUnauthenticated},
		{limes.CodeExpired, connect.CodeUnauthenticated},
		{limes.CodeNotYetValid, connect.CodeUnauthenticated},
		{limes.CodeTenantErased, connect.CodeUnauthenticated},
		{limes.CodeDelegationTooDeep, connect.CodeUnauthenticated},
		{limes.CodeRevoked, connect.CodeUnauthenticated},
		{limes.CodeAudienceMismatch, connect.CodeUnauthenticated},
		{limes.CodeUnauthenticated, connect.CodeUnauthenticated},
		{limes.CodeOpNotAllowed, connect.CodePermissionDenied},
		{limes.CodeResourceNotAllowed, connect.CodePermissionDenied},
		{limes.CodeSourceIPNotAllowed, connect.CodePermissionDenied},
		{limes.CodeIdempotencyKeyRequired, connect.CodePermissionDenied},
		{limes.CodeTaintedReadNotAllowed, connect.CodePermissionDenied},
		{limes.CodeCaveatViolation, connect.CodePermissionDenied},
		{limes.CodeRequestLimitExceeded, connect.CodeResourceExhausted},
		{limes.CodeBudgetExceeded, connect.CodeResourceExhausted},
		{limes.CodeTenantBudgetExceeded, connect.CodeResourceExhausted},
		{limes.CodeUnitCodeMismatch, connect.CodeFailedPrecondition},
		{limes.CodeAlreadyExists, connect.CodeAlreadyExists},
		{limes.CodeConflict, connect.CodeAborted},
		{limes.CodeInvalidCaveats, connect.CodeInvalidArgument},
		{limes.CodeInvalidAmount, connect.CodeInvalidArgument},
		{limes.CodeEffectConflict, connect.CodeInvalidArgument},
		{limes.CodeCursorMismatch, connect.CodeInvalidArgument},
		{limes.CodeInvalidRequest, connect.CodeInvalidArgument},
		{limes.CodeDelegationTooWide, connect.CodePermissionDenied},
		{limes.CodeUnknownKID, connect.CodeUnauthenticated},
		{limes.CodeNotFound, connect.CodeFailedPrecondition},
		{limes.CodeUnknownTenant, connect.CodeFailedPrecondition},
		{limes.CodeTenantDeleted, connect.CodeFailedPrecondition},
		{limes.CodeScopeMismatch, connect.CodeInternal},
		{limes.CodeProofUnsupported, connect.CodeFailedPrecondition},
		{limes.CodeNoRevocationLineage, connect.CodeUnavailable},
		{limes.CodeCopyCountersNotMetered, connect.CodeInternal},
		{limes.CodeCursorRepeated, connect.CodeUnavailable},
		{limes.CodeInvalidConfig, connect.CodeInternal},
		{limes.CodeInternal, connect.CodeInternal},
		{meter.CodeCopyLimitExceeded, connect.CodeResourceExhausted},
		{meter.CodeCounterOverflow, connect.CodeFailedPrecondition},
		{meter.CodeChargeNotFound, connect.CodeFailedPrecondition},
		{meter.CodeRefundExceedsCharge, connect.CodeAborted},
		{meter.CodeReservationNotFound, connect.CodeFailedPrecondition},
		{meter.CodeUsageNotFound, connect.CodeFailedPrecondition},
		{meter.CodeTenantBudgetNotFound, connect.CodeFailedPrecondition},
		{meter.CodeTenantBudgetVersionMismatch, connect.CodeAborted},
		{meter.CodeExternalRefConflict, connect.CodeAborted},
		{meter.CodeSettleConflict, connect.CodeAborted},
		{meter.CodeLeaseExhausted, connect.CodeResourceExhausted},
		{meter.CodeLeaseRequestsExhausted, connect.CodeResourceExhausted},
		{meter.CodeLeaseNotFound, connect.CodeFailedPrecondition},
		{meter.CodeLeaseNested, connect.CodeFailedPrecondition},
		{meter.CodeLeasingDisabled, connect.CodeFailedPrecondition},
		{meter.CodeFeatureDisabled, connect.CodeFailedPrecondition},
	}
	covered := map[limes.Code]bool{}
	for _, tc := range cases {
		covered[tc.code] = true
		if got := codeStatus(tc.code); got != tc.want {
			t.Errorf("%s answers %v, want %v", tc.code, got, tc.want)
		}
	}
	for _, c := range append(limes.Codes(), meter.Codes()...) {
		if !covered[c] {
			t.Errorf("limes code %q has no case here", c)
		}
	}
}

// An override answers its code otherwise than the code's category does;
// one that agrees with it is dead weight that hides a category's change.
func TestCapabilityCodeOverridesDifferFromTheirCategory(t *testing.T) {
	for code, status := range capabilityCodeOverrides {
		category, ok := meter.CategoryOfCode(code)
		if !ok {
			t.Errorf("override %q is no limes code", code)
			continue
		}
		if capabilityCategoryCodes[category] == status {
			t.Errorf("override %q answers %v, as its category %q does", code, status, category)
		}
	}
}
