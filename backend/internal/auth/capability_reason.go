package auth

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// capabilityReasonPrefix spells a limes.Reason as an ErrorReason value
// name: "budget_exceeded" is ERROR_REASON_CAPABILITY_BUDGET_EXCEEDED. One rule
// rather than a table, so a reason the module adds cannot be mapped wrong —
// only left unmapped, which TestEveryCapabilityReasonHasAnErrorReason refuses.
const capabilityReasonPrefix = "ERROR_REASON_CAPABILITY_"

// capabilityErrorReason is r's ErrorReason; ERROR_REASON_UNSPECIFIED when the
// proto has no value for it.
func capabilityErrorReason(r limes.Reason) commonv1.ErrorReason {
	return commonv1.ErrorReason(commonv1.ErrorReason_value[capabilityReasonPrefix+strings.ToUpper(string(r))])
}

// capabilityUnchecked is what a caller is told when its capability could not
// be checked at all: the cause is the server's, names its internals, and is
// logged instead.
const capabilityUnchecked = "the capability could not be checked; retry"

// capabilityServerFault is what a caller is told when the server's own fault
// — its configuration, a broken invariant — kept the capability from being
// checked: no retry mends it, and the cause is logged instead.
const capabilityServerFault = "the capability could not be checked: the server is at fault"

// proofUnchecked is what a caller is told when it presents a key-bound
// capability to a plane that checks no proof: the deployment's fault, which
// no retry mends.
const proofUnchecked = "this plane checks no proof of possession; a key-bound capability cannot be used here"

// verifyError maps a failed verification to its transport status, as
// capabilityFailure answers every error about a presented capability.
func verifyError(ctx context.Context, err error) error {
	return capabilityFailure(ctx, "verification", err)
}

// capabilityFailure answers an error verifying or metering a presented
// capability — stage names which — through capabilityCodes, so both answer one
// caller alike: a spent limit is ResourceExhausted, a refusal of the
// capability PermissionDenied with its reason, the server's own fault
// Internal, and only a failing dependency Unavailable. A key-bound capability
// on a plane with no proof check is FailedPrecondition, the deployment's
// fault. Those last three are logged with their cause, and unless the error
// is a refusal, whose reason the caller acts on, the caller is told a fixed
// sentence rather than the server's internals.
func capabilityFailure(ctx context.Context, stage string, err error) error {
	status := capabilityStatus(capabilityCodes, err)
	_, refused := limes.ReasonOf(err)
	switch {
	case errors.Is(err, limes.ErrProofUnsupported):
		logger.FromContext(ctx).Error("capability: a key-bound capability reached a verifier with no DPoP check", zap.String("stage", stage), zap.Error(err))
		return connect.NewError(status, proofUnchecked).WithCause(err)
	case status == connect.CodeInternal, status == connect.CodeUnavailable:
		logger.FromContext(ctx).Error("capability: failed on the server's side", zap.String("stage", stage), zap.Error(err))
		if !refused {
			told := capabilityUnchecked
			if status == connect.CodeInternal {
				told = capabilityServerFault
			}
			return connect.NewError(status, told).WithCause(err)
		}
	}
	return capabilityError(status, err)
}

// capabilityError is connect.NewError carrying, when err is a refusal of the
// capability, a google.rpc.ErrorInfo naming its reason — so a client tells a
// spent or revoked capability, which it should stop using, from a request it
// can fix, without reading the message.
func capabilityError(code connect.Code, err error) *connect.Error {
	e := connect.NewError(code, err.Error()).WithCause(err)
	r, ok := limes.ReasonOf(err)
	if !ok {
		return e
	}
	reason := capabilityErrorReason(r)
	if reason == commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED {
		return e
	}
	detail, dErr := connectproto.NewErrorDetail(&errdetails.ErrorInfo{Reason: reason.String(), Domain: paladin.ErrorDomain})
	if dErr != nil { // only a message that cannot be marshalled fails, and this one can
		return e
	}
	return e.WithDetail(detail)
}
