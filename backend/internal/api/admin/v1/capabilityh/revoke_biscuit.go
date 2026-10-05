package capabilityh

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/capability"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// BiscuitCopier names which copy of which capability a Biscuit is, after
// checking that it is genuine. capability.StandardVerifier implements it.
type BiscuitCopier interface {
	BiscuitCopy(ctx context.Context, token string) (capability.BiscuitCopy, error)
}

// WithBiscuitCopies wires RevokeBiscuit. Without it the RPC answers
// Unavailable, as GetUsage does without a usage store.
func (h *Handler) WithBiscuitCopies(copier BiscuitCopier, copies capability.BiscuitRevocationStore) *Handler {
	h.copier, h.copies = copier, copies
	return h
}

// RevokeBiscuit revokes one copy of a capability's Biscuit. It is the same
// permission as Revoke, scoped the same way: a caller confined to its own
// tenant cannot revoke a copy of another tenant's capability, and learns
// nothing about it either.
func (h *Handler) RevokeBiscuit(ctx context.Context, req *connect.Request[adminv1.CapabilityServiceRevokeBiscuitRequest]) (*connect.Response[adminv1.CapabilityServiceRevokeBiscuitResponse], error) {
	caller, err := h.authorize(ctx, "revoke")
	if err != nil {
		return nil, err
	}
	if h.copier == nil || h.copies == nil {
		return nil, connect.NewError(connect.CodeUnavailable,
			errors.New("biscuit copy revocation not wired"))
	}
	c, err := h.copier.BiscuitCopy(ctx, req.Msg.GetToken())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("token: %w", err))
	}
	ctx, err = h.actOnCapabilitysTenant(ctx, caller, c.CapabilityID)
	if err != nil {
		return nil, err
	}
	if err := h.copies.RevokeBiscuit(ctx, capability.RevokeBiscuitArgs{
		CapabilityID: c.CapabilityID,
		RevocationID: c.RevocationID,
		Reason:       req.Msg.GetReason(),
		Actor:        caller.Subject,
	}); err != nil {
		// As in Revoke: no such capability and another tenant's are one
		// answer, so the endpoint is no oracle.
		if errors.Is(err, capability.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	stashCapabilityInScope(ctx, c.CapabilityID)
	return connect.NewResponse(&adminv1.CapabilityServiceRevokeBiscuitResponse{
		CapabilityId: c.CapabilityID.String(),
	}), nil
}
