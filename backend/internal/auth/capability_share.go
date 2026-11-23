package auth

import (
	"context"
	"fmt"
	"slices"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
)

// errNoCapabilityToShare is what a caller asking to share without presenting
// a capability is told.
const errNoCapabilityToShare = "no capability presented to share from"

// CapabilityPrincipal is the principal a verified capability acts as: its own
// tenant, its id under SubjectPrefixCapability, PrincipalKindCapability, and
// no roles. audience is the canonical plane audience it is presented on.
func CapabilityPrincipal(c *limes.Capability, audience string) *Principal {
	return &Principal{
		TenantID: c.Subject.TenantID,
		Subject:  SubjectPrefixCapability + c.ID.String(),
		Audience: audience,
		Kind:     PrincipalKindCapability,
	}
}

// AssertCapabilityShare checks that the presented capability's caveats allow
// limes.OpShare, and records share as the operation the request admitted, so
// the policy engine sees context.capability_op "share". It checks the
// operation alone, where AssertCapabilityOp checks every caveat: a delegation
// names no resource, and Issuer.Delegate refuses any child wider than its
// parent.
func AssertCapabilityShare(ctx context.Context) error {
	c, ok := CapabilityFromContext(ctx)
	if !ok {
		return connect.NewError(connect.CodeFailedPrecondition, errNoCapabilityToShare)
	}
	if !slices.Contains(c.Caveats.Ops, limes.OpShare) {
		return capabilityError(connect.CodePermissionDenied,
			fmt.Errorf("%w: %q not in %v", limes.ErrOpNotAllowed, limes.OpShare, c.Caveats.Ops))
	}
	stampLastOp(ctx, limes.OpShare)
	return nil
}
