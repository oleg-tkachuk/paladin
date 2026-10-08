package auth

import (
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/oleg-tkachuk/paladin/capability"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// capabilityReasonPrefix spells a capability.Reason as an ErrorReason value
// name: "budget_exceeded" is ERROR_REASON_CAPABILITY_BUDGET_EXCEEDED. One rule
// rather than a table, so a reason the module adds cannot be mapped wrong —
// only left unmapped, which TestEveryCapabilityReasonHasAnErrorReason refuses.
const capabilityReasonPrefix = "ERROR_REASON_CAPABILITY_"

// capabilityErrorReason is r's ErrorReason; ERROR_REASON_UNSPECIFIED when the
// proto has no value for it.
func capabilityErrorReason(r capability.Reason) commonv1.ErrorReason {
	return commonv1.ErrorReason(commonv1.ErrorReason_value[capabilityReasonPrefix+strings.ToUpper(string(r))])
}

// capabilityError is connect.NewError carrying, when err is a refusal of the
// capability, a google.rpc.ErrorInfo naming its reason — so a client tells a
// spent or revoked capability, which it should stop using, from a request it
// can fix, without reading the message.
func capabilityError(code connect.Code, err error) *connect.Error {
	e := connect.NewError(code, err.Error()).WithCause(err)
	r, ok := capability.ReasonOf(err)
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
