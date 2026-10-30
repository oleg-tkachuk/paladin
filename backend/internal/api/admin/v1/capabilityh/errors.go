package capabilityh

import (
	"maps"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// Each operation answers a limes error through auth.AnswerCapabilityError:
// its rows below, then the table every capability error is answered by.
// TestExceptionsNameLimesCodes holds the rows to codes limes defines.

// malformed answers each of codes — limes refusing the request as malformed
// — with the reason every other malformed admin request carries.
func malformed(codes ...limes.Code) auth.Exceptions {
	e := make(auth.Exceptions, len(codes))
	for _, c := range codes {
		e[c] = auth.Exception{Code: connect.CodeInvalidArgument, Reason: commonv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT}
	}
	return e
}

// issuanceExceptions answer Issue.
var issuanceExceptions = func() auth.Exceptions {
	e := malformed(limes.CodeInvalidRequest, limes.CodeInvalidCaveats, limes.CodeInvalidAmount, limes.CodeEffectConflict)
	maps.Copy(e, auth.Exceptions{
		// NotFound, not FailedPrecondition: the request names a resource that
		// does not exist. The reason tells it from any other NotFound, so a
		// caller waiting on a tenant being provisioned can retry on it alone.
		limes.CodeUnknownTenant: {Code: connect.CodeNotFound, Reason: commonv1.ErrorReason_ERROR_REASON_TENANT_NOT_FOUND},
		limes.CodeTenantDeleted: {Code: connect.CodeFailedPrecondition, Reason: commonv1.ErrorReason_ERROR_REASON_TENANT_ALREADY_DELETED},
	})
	return e
}()

// delegationExceptions answer Delegate: issuance's, and the parent's. A
// parent revoked, expired, at the depth limit or in an erased tenant can no
// longer delegate,
// which is a state the caller must act on, not a refusal of this call; a
// child in another unit than its parent's is wider than the parent allows.
var delegationExceptions = func() auth.Exceptions {
	e := maps.Clone(issuanceExceptions)
	maps.Copy(e, auth.Exceptions{
		limes.CodeRevoked:           {Code: connect.CodeFailedPrecondition},
		limes.CodeExpired:           {Code: connect.CodeFailedPrecondition},
		limes.CodeDelegationTooDeep: {Code: connect.CodeFailedPrecondition},
		limes.CodeTenantErased:      {Code: connect.CodeFailedPrecondition},
		limes.CodeUnitCodeMismatch:  {Code: connect.CodePermissionDenied},
	})
	return e
}()

// recordExceptions answer an operation on a record the request names: one
// not there is NotFound, whether no such id or another tenant's, so the
// endpoint is no oracle.
var recordExceptions = auth.Exceptions{
	limes.CodeNotFound:      {Code: connect.CodeNotFound},
	meter.CodeUsageNotFound: {Code: connect.CodeNotFound},
}

// listExceptions answer List: a page token the store cannot read, or one
// handed out for another listing, is the caller's to fix.
var listExceptions = malformed(limes.CodeInvalidRequest, limes.CodeCursorMismatch)
