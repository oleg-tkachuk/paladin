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

// proofUnchecked is what a caller is told when it presents a key-bound
// capability to a plane that checks no proof: the deployment's fault, which
// no retry mends.
const proofUnchecked = "this plane checks no proof of possession; a key-bound capability cannot be used here"

// verifyError maps a failed verification to its transport status. A refusal
// of the capability is PermissionDenied with its reason. Keys the verifier
// could not fetch, and a failure that refuses nothing — the revocation store
// unreachable — are the server's, Unavailable: the caller retries rather than
// drops a capability that may be sound. A key-bound capability on a plane
// with no proof check is FailedPrecondition: the deployment's fault, which
// no retry mends. All are logged with their cause,
// which the outcome log, reading only the message, does not see.
func verifyError(ctx context.Context, err error) error {
	r, refused := limes.ReasonOf(err)
	switch {
	case errors.Is(err, limes.ErrProofUnsupported):
		logger.FromContext(ctx).Error("capability: a key-bound capability reached a verifier with no DPoP check", zap.Error(err))
		return connect.NewError(connect.CodeFailedPrecondition, proofUnchecked).WithCause(err)
	case !refused:
		logger.FromContext(ctx).Error("capability: verification failed on the server's side", zap.Error(err))
		return connect.NewError(connect.CodeUnavailable, capabilityUnchecked).WithCause(err)
	case r == limes.ReasonKeysUnavailable:
		logger.FromContext(ctx).Error("capability: verification keys unavailable", zap.Error(err))
		return capabilityError(connect.CodeUnavailable, err)
	}
	return capabilityError(connect.CodePermissionDenied, err)
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
