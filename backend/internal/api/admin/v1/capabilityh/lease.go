package capabilityh

import (
	"context"
	"fmt"
	"slices"
	"uuid"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// WithLeases wires Lease, GetLease and SettleLease. Without it they answer
// Unavailable.
func (h *Handler) WithLeases(leases meter.Leaser) *Handler {
	h.leases = leases
	return h
}

// errLeasesNotWired answers a lease RPC on a deployment whose meter leases
// nothing.
const errLeasesNotWired = "capability budget leases not wired"

// leaseAmountField is the request field Lease reads its amount from.
const leaseAmountField = "amount"

// leaseExceptions answer a lease operation: a capability or a lease not
// there is NotFound, whether no such id or another tenant's.
var leaseExceptions = auth.Exceptions{
	limes.CodeNotFound:      {Code: connect.CodeNotFound},
	meter.CodeLeaseNotFound: {Code: connect.CodeNotFound},
}

// Lease holds budget and requests of a capability's chain for it, or adds to
// the lease it holds. It hands out the chain's budget as issuing does, so it
// is gated as Issue is, or by a presented capability (leaseTarget).
func (h *Handler) Lease(ctx context.Context, req *adminv1.CapabilityServiceLeaseRequest) (*adminv1.CapabilityServiceLeaseResponse, error) {
	ctx, id, err := h.leaseTarget(ctx, cedar.ActionIssueCapability, req.GetId())
	if err != nil {
		return nil, err
	}
	subject, err := limes.StoredMeterSubject(ctx, h.store, id)
	if err != nil {
		return nil, auth.AnswerCapabilityError(err, leaseExceptions)
	}
	amount, err := leaseAmount(subject, req)
	if err != nil {
		return nil, err
	}
	lease, err := h.leases.Lease(ctx, meter.LeaseRequest{
		Subject:  subject,
		Amount:   amount,
		Requests: req.GetRequests(),
		TTL:      req.GetTtl().AsDuration(),
	})
	if err != nil {
		return nil, auth.AnswerCapabilityError(fmt.Errorf("lease %s: %w", id, err), leaseExceptions)
	}
	stashCapabilityInScope(ctx, id)
	return &adminv1.CapabilityServiceLeaseResponse{Lease: leaseToProto(lease)}, nil
}

// GetLease returns the lease a capability holds.
func (h *Handler) GetLease(ctx context.Context, req *adminv1.CapabilityServiceGetLeaseRequest) (*adminv1.CapabilityServiceGetLeaseResponse, error) {
	ctx, id, err := h.leaseTarget(ctx, cedar.ActionReadCapability, req.GetId())
	if err != nil {
		return nil, err
	}
	lease, err := h.leases.GetLease(ctx, id)
	if err != nil {
		return nil, auth.AnswerCapabilityError(err, leaseExceptions)
	}
	return &adminv1.CapabilityServiceGetLeaseResponse{Lease: leaseToProto(lease)}, nil
}

// SettleLease ends a capability's lease, charging the chain what it spent.
func (h *Handler) SettleLease(ctx context.Context, req *adminv1.CapabilityServiceSettleLeaseRequest) (*adminv1.CapabilityServiceSettleLeaseResponse, error) {
	ctx, id, err := h.leaseTarget(ctx, cedar.ActionIssueCapability, req.GetId())
	if err != nil {
		return nil, err
	}
	// The record names the capability's tenant, which settling needs.
	subject, err := limes.StoredMeterSubject(ctx, h.store, id)
	if err != nil {
		return nil, auth.AnswerCapabilityError(err, leaseExceptions)
	}
	lease, err := h.leases.SettleLease(ctx, meter.SettleLeaseRequest{Subject: subject})
	if err != nil {
		return nil, auth.AnswerCapabilityError(fmt.Errorf("settle lease %s: %w", id, err), leaseExceptions)
	}
	stashCapabilityInScope(ctx, id)
	return &adminv1.CapabilityServiceSettleLeaseResponse{Lease: leaseToProto(lease)}, nil
}

// leaseTarget parses the capability a lease RPC names, gates the call, and
// scopes ctx to the capability's tenant, once leasing is wired. A caller
// presenting a capability is gated by it alone, as Delegate is
// (presentedLeaseTarget); any other caller by Cedar's action.
func (h *Handler) leaseTarget(ctx context.Context, action cedar.Action, raw string) (context.Context, uuid.UUID, error) {
	verified, presented := auth.VerifiedCapabilityFromContext(ctx)
	var caller *auth.Principal
	if !presented {
		var err error
		if caller, err = h.authorize(ctx, action); err != nil {
			return nil, uuid.Nil(), err
		}
	}
	if h.leases == nil {
		return nil, uuid.Nil(), connect.NewError(connect.CodeUnavailable, errLeasesNotWired)
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, uuid.Nil(), rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("id: %w", err))
	}
	if presented {
		return h.presentedLeaseTarget(ctx, verified, id)
	}
	ctx, err = h.actOnCapabilitysTenant(ctx, caller, id)
	if err != nil {
		return nil, uuid.Nil(), err
	}
	return ctx, id, nil
}

// Refusals of a presented capability's lease call.
const (
	errLeaseByCopy       = "capability: a Biscuit copy cannot lease; present the capability it was attenuated from"
	errLeaseWithoutShare = "capability: leasing for a capability requires OpShare"
	errLeaseNotBelow     = "capability: a capability leases only for itself or a capability delegated from it"
	errLeaseNoLineage    = "capability: the store names no ancestors, so no lease can be checked against them"
)

// presentedLeaseTarget gates a lease call by the capability presented: one
// that may share leases for itself, or for a capability delegated from it —
// the orchestrator handing its sub-agents budget it holds. A Biscuit copy
// cannot: none of its charges go under a lease.
func (h *Handler) presentedLeaseTarget(ctx context.Context, verified limes.VerifiedCapability, id uuid.UUID) (context.Context, uuid.UUID, error) {
	if verified.FromBiscuit() {
		return nil, uuid.Nil(), connect.NewError(connect.CodeFailedPrecondition, errLeaseByCopy)
	}
	holder := verified.Capability()
	if !hasOp(holder.Caveats.Ops, limes.OpShare) {
		return nil, uuid.Nil(), connect.NewError(connect.CodePermissionDenied, errLeaseWithoutShare)
	}
	ctx = auth.WithActingTenant(ctx, holder.Subject.TenantID)
	if id == holder.ID {
		return ctx, id, nil
	}
	lineage, ok := h.store.(limes.RevocationLineageLookup)
	if !ok {
		return nil, uuid.Nil(), connect.NewError(connect.CodeUnavailable, errLeaseNoLineage)
	}
	answer, err := lineage.RevocationLineage(ctx, id)
	if err != nil {
		return nil, uuid.Nil(), auth.AnswerCapabilityError(err, leaseExceptions)
	}
	if answer.Revoked {
		return nil, uuid.Nil(), auth.AnswerCapabilityError(fmt.Errorf("lease %s: %w", id, limes.ErrRevoked), nil)
	}
	if !slices.Contains(answer.Ancestors, holder.ID) {
		return nil, uuid.Nil(), connect.NewError(connect.CodePermissionDenied, errLeaseNotBelow)
	}
	return ctx, id, nil
}

// leaseAmount is the amount req leases, in subject's unit: none when it names
// none. An amount in another unit is refused rather than read as the
// subject's.
func leaseAmount(subject limes.MeterSubject, req *adminv1.CapabilityServiceLeaseRequest) (limes.Nanos, error) {
	if req.GetAmount() == nil {
		return 0, nil
	}
	n, unit, err := apiutil.NanosOf(leaseAmountField, req.GetAmount())
	if err != nil {
		return 0, rpcerr.New(connect.CodeInvalidArgument, err)
	}
	if unit != subject.UnitCode() {
		return 0, connect.NewError(connect.CodeInvalidArgument,
			fmt.Sprintf("%s: unit %q, the capability's is %q", leaseAmountField, unit, subject.UnitCode()))
	}
	return n, nil
}

func leaseToProto(l meter.Lease) *adminv1.CapabilityLease {
	return &adminv1.CapabilityLease{
		Id:            l.ID.String(),
		CapabilityId:  l.CapabilityID.String(),
		TenantId:      l.TenantID.String(),
		Amount:        apiutil.MoneyOf(l.UnitCode, l.Amount),
		Spent:         apiutil.MoneyOf(l.UnitCode, l.Spent),
		Requests:      l.Requests,
		RequestsSpent: l.RequestsSpent,
		ExpiresAt:     timestamppb.New(l.ExpiresAt),
	}
}
