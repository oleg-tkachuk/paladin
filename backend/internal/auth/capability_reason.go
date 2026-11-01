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
	"github.com/oleg-tkachuk/limes/meter"
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
		return withCapabilityInfo(connect.NewError(status, proofUnchecked).WithCause(err), err, commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED)
	case status == connect.CodeInternal, status == connect.CodeUnavailable:
		logger.FromContext(ctx).Error("capability: failed on the server's side", zap.String("stage", stage), zap.Error(err))
		if !refused {
			told := capabilityUnchecked
			if status == connect.CodeInternal {
				told = capabilityServerFault
			}
			return withCapabilityInfo(connect.NewError(status, told).WithCause(err), err, commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED)
		}
	}
	return capabilityError(status, err)
}

// capabilityError is connect.NewError carrying the ErrorInfo of err: when it
// is a refusal of the capability, its reason — so a client tells a spent or
// revoked capability, which it should stop using, from a request it can fix,
// without reading the message — and the limes code of any error of the
// library's.
func capabilityError(code connect.Code, err error) *connect.Error {
	reason := commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED
	if r, ok := limes.ReasonOf(err); ok {
		reason = capabilityErrorReason(r)
	}
	return withCapabilityInfo(connect.NewError(code, err.Error()).WithCause(err), err, reason)
}

// withCapabilityInfo is e carrying the ErrorInfo an error of the capability
// library answers with: reason, when it is one, and err's limes code under
// paladin.ErrorMetadataLimesCode — which of the library's errors it was,
// whatever Connect code it was answered with. e as it is when err is no
// error of the library's and reason is unspecified.
func withCapabilityInfo(e *connect.Error, err error, reason commonv1.ErrorReason) *connect.Error {
	code, _ := meter.CodeOf(err)
	info := capabilityErrorInfo(reason, code)
	if info == nil {
		return e
	}
	detail, dErr := connectproto.NewErrorDetail(info)
	if dErr != nil { // only a message that cannot be marshalled fails, and this one can
		return e
	}
	return e.WithDetail(detail)
}

// capabilityErrorInfo is the ErrorInfo of a reason and a limes code, either
// of which may be absent; nil when both are. An unspecified reason is left
// empty rather than spelled out, which both SDKs read as no reason.
func capabilityErrorInfo(reason commonv1.ErrorReason, code limes.Code) *errdetails.ErrorInfo {
	if reason == commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED && code == "" {
		return nil
	}
	info := &errdetails.ErrorInfo{Domain: paladin.ErrorDomain}
	if reason != commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED {
		info.Reason = reason.String()
	}
	if code != "" {
		info.Metadata = map[string]string{paladin.ErrorMetadataLimesCode: string(code)}
	}
	return info
}
