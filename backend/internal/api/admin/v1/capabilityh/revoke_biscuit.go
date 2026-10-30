package capabilityh

import (
	"context"
	"fmt"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// BiscuitCopier names which copy of which capability a Biscuit is, after
// checking that it is genuine. limes.Verifier implements it.
type BiscuitCopier interface {
	BiscuitCopy(ctx context.Context, token string) (limes.BiscuitCopy, error)
}

// WithBiscuitCopies wires RevokeBiscuit. Without it the RPC answers
// Unavailable, as GetUsage does without a usage store.
func (h *Handler) WithBiscuitCopies(copier BiscuitCopier, copies limes.BiscuitRevocationStore) *Handler {
	h.copier, h.copies = copier, copies
	return h
}

// RevokeBiscuit revokes one copy of a capability's Biscuit. It is the same
// permission as Revoke, scoped the same way: a caller confined to its own
// tenant cannot revoke a copy of another tenant's capability, and learns
// nothing about it either.
func (h *Handler) RevokeBiscuit(ctx context.Context, req *adminv1.CapabilityServiceRevokeBiscuitRequest) (*adminv1.CapabilityServiceRevokeBiscuitResponse, error) {
	caller, err := h.authorize(ctx, cedar.ActionRevokeCapability)
	if err != nil {
		return nil, err
	}
	if h.copier == nil || h.copies == nil {
		return nil, connect.NewError(connect.CodeUnavailable,
			"biscuit copy revocation not wired")
	}
	c, err := h.copier.BiscuitCopy(ctx, req.GetToken())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("token: %w", err))
	}
	ctx, err = h.actOnCapabilitysTenant(ctx, caller, c.CapabilityID())
	if err != nil {
		return nil, err
	}
	res, err := h.copies.RevokeBiscuit(ctx, limes.RevokeBiscuitRequest{
		Copy:  c,
		Note:  req.GetReason(),
		Actor: caller.Subject,
	})
	if err != nil {
		// As in Revoke: no such capability and another tenant's are one
		// answer, so the endpoint is no oracle.
		return nil, auth.AnswerCapabilityError(err, recordExceptions)
	}
	stashCapabilityInScope(ctx, c.CapabilityID())
	return &adminv1.CapabilityServiceRevokeBiscuitResponse{
		CapabilityId:   c.CapabilityID().String(),
		AlreadyRevoked: res.AlreadyRevoked,
		Revocation:     biscuitRevocationToProto(res.Revocation),
	}, nil
}

// biscuitRevocationToProto is a copy's revocation entry as the API carries
// it: a copy is revoked alone, never by a cascade.
func biscuitRevocationToProto(r limes.BiscuitRevocation) *adminv1.CapabilityRevocation {
	return &adminv1.CapabilityRevocation{RevokedAt: timestamppb.New(r.RevokedAt), Reason: r.Note, Actor: r.Actor}
}
