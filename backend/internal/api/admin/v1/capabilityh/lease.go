package capabilityh

import (
	"context"
	"fmt"
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
// is gated as Issue is.
func (h *Handler) Lease(ctx context.Context, req *adminv1.CapabilityServiceLeaseRequest) (*adminv1.CapabilityServiceLeaseResponse, error) {
	caller, err := h.authorize(ctx, cedar.ActionIssueCapability)
	if err != nil {
		return nil, err
	}
	ctx, id, err := h.leaseTarget(ctx, caller, req.GetId())
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
	caller, err := h.authorize(ctx, cedar.ActionReadCapability)
	if err != nil {
		return nil, err
	}
	ctx, id, err := h.leaseTarget(ctx, caller, req.GetId())
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
	caller, err := h.authorize(ctx, cedar.ActionIssueCapability)
	if err != nil {
		return nil, err
	}
	ctx, id, err := h.leaseTarget(ctx, caller, req.GetId())
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

// leaseTarget parses the capability a lease RPC names and scopes ctx to its
// tenant, once leasing is wired.
func (h *Handler) leaseTarget(ctx context.Context, caller *auth.Principal, raw string) (context.Context, uuid.UUID, error) {
	if h.leases == nil {
		return nil, uuid.Nil(), connect.NewError(connect.CodeUnavailable, errLeasesNotWired)
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, uuid.Nil(), rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("id: %w", err))
	}
	ctx, err = h.actOnCapabilitysTenant(ctx, caller, id)
	if err != nil {
		return nil, uuid.Nil(), err
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
