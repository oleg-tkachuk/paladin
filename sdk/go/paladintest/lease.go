package paladintest

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"uuid"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/genproto/googleapis/type/money"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// Lease, GetLease and SettleLease, as the fake serves them: who may lease
// for a capability is the server's rule, checked here; the lease itself is
// limes's, kept by the in-memory store the fake counts usage in (usage), as
// the server's is by pgstore. A request under a lease is spent from it. The
// fake charges no budget, so a lease's spent amount stays zero.

// The server's refusals of a presented capability's lease call, word for word.
const (
	msgLeaseByCopy       = "capability: a Biscuit copy cannot lease; present the capability it was attenuated from"
	msgLeaseWithoutShare = "capability: leasing for a capability requires OpShare"
	msgLeaseNotBelow     = "capability: a capability leases only for itself or a capability delegated from it"
)

// Lease opens a lease on the capability named; on one it holds already, it
// settles that and opens a new one with what was left plus the request.
func (s capabilityService) Lease(ctx context.Context, req *adminv1.CapabilityServiceLeaseRequest) (*adminv1.CapabilityServiceLeaseResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := s.leaseTargetLocked(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	subject, err := s.usage.subject(rec.msg.GetId())
	if err != nil {
		return nil, limesRefusal(limesStatus(err, leaseExceptionCodes), err)
	}
	if a := req.GetAmount(); a != nil && a.GetCurrencyCode() != subject.UnitCode() {
		return nil, withReason(connect.Errorf(connect.CodeInvalidArgument,
			"amount: unit %q, the capability's is %q", a.GetCurrencyCode(), subject.UnitCode()))
	}
	l, err := s.usage.store.Lease(ctx, meter.LeaseRequest{
		Subject:  subject,
		Amount:   limes.Nanos(nanosOf(req.GetAmount())),
		Requests: req.GetRequests(),
		TTL:      req.GetTtl().AsDuration(),
	})
	if err != nil {
		err = fmt.Errorf("lease %s: %w", req.GetId(), err)
		return nil, limesRefusal(limesStatus(err, leaseExceptionCodes), err)
	}
	return &adminv1.CapabilityServiceLeaseResponse{Lease: leaseProto(l)}, nil
}

// GetLease returns the lease a capability holds.
func (s capabilityService) GetLease(ctx context.Context, req *adminv1.CapabilityServiceGetLeaseRequest) (*adminv1.CapabilityServiceGetLeaseResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.leaseTargetLocked(ctx, req.GetId()); err != nil {
		return nil, err
	}
	subject, err := s.usage.subject(req.GetId())
	if err != nil {
		return nil, limesRefusal(limesStatus(err, leaseExceptionCodes), err)
	}
	l, err := s.usage.store.GetLease(ctx, subject.CapabilityID())
	if err != nil {
		return nil, limesRefusal(limesStatus(err, leaseExceptionCodes), err)
	}
	return &adminv1.CapabilityServiceGetLeaseResponse{Lease: leaseProto(l)}, nil
}

// SettleLease ends a capability's lease and returns it as it ended.
func (s capabilityService) SettleLease(ctx context.Context, req *adminv1.CapabilityServiceSettleLeaseRequest) (*adminv1.CapabilityServiceSettleLeaseResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.leaseTargetLocked(ctx, req.GetId()); err != nil {
		return nil, err
	}
	subject, err := s.usage.subject(req.GetId())
	if err != nil {
		return nil, limesRefusal(limesStatus(err, leaseExceptionCodes), err)
	}
	l, err := s.usage.store.SettleLease(ctx, meter.SettleLeaseRequest{Subject: subject})
	if err != nil {
		err = fmt.Errorf("settle lease %s: %w", req.GetId(), err)
		return nil, limesRefusal(limesStatus(err, leaseExceptionCodes), err)
	}
	return &adminv1.CapabilityServiceSettleLeaseResponse{Lease: leaseProto(l)}, nil
}

// leaseTargetLocked is the capability a lease call names, once the caller
// may reach it: any caller with no capability presented, as the server's
// operator path admits one Cedar allows, or a presented capability holding
// share for itself or one delegated from it. The caller holds mu.
func (s capabilityService) leaseTargetLocked(ctx context.Context, id string) (*capabilityRecord, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, withReason(connect.Errorf(connect.CodeInvalidArgument, "id: %v", err))
	}
	header := requestHeader(ctx)
	presented := capabilityToken(header.Get(paladin.HeaderCapability), header.Get(paladin.HeaderAuthorization))
	if presented == "" {
		rec, ok := s.capabilities[id]
		if !ok {
			return nil, limesRefusal(connect.CodeNotFound, fmt.Errorf("%w: capability %s", limes.ErrNotFound, id))
		}
		return rec, nil
	}
	holder, err := s.presentedHolderLocked(presented)
	if err != nil {
		return nil, err
	}
	rec, ok := s.capabilities[id]
	if !ok {
		return nil, limesRefusal(connect.CodeNotFound, fmt.Errorf("%w: capability %s", limes.ErrNotFound, id))
	}
	if id == holder.msg.GetId() {
		return rec, nil
	}
	if s.stoppedBy(rec) != nil {
		return nil, capabilityRefusal(connect.CodePermissionDenied, fmt.Errorf("lease %s: %w", id, limes.ErrRevoked))
	}
	var ancestors []string
	for a := s.capabilities[rec.msg.GetParentId()]; a != nil; a = s.capabilities[a.msg.GetParentId()] {
		ancestors = append(ancestors, a.msg.GetId())
	}
	if !slices.Contains(ancestors, holder.msg.GetId()) {
		return nil, withReason(connect.NewError(connect.CodePermissionDenied, msgLeaseNotBelow))
	}
	return rec, nil
}

// presentedHolderLocked is the capability a caller presented to a lease call:
// one the fake issued, not revoked, holding share, and not a Biscuit — its
// own or a copy narrowed from it. The caller holds mu.
func (s capabilityService) presentedHolderLocked(presented string) (*capabilityRecord, error) {
	cred, ok := s.credentials[presented]
	if !ok {
		// A copy the fake's sealer opens is a Biscuit all the same.
		if _, _, err := s.seal.open(presented, paladin.PlaneAdmin); err == nil {
			return nil, withReason(connect.NewError(connect.CodeFailedPrecondition, msgLeaseByCopy))
		}
		return nil, connect.NewError(connect.CodeUnauthenticated, errCapabilityInvalid.Error()).WithCause(errCapabilityInvalid)
	}
	holder := s.capabilities[cred.capability]
	switch {
	case holder == nil:
		return nil, connect.NewError(connect.CodeUnauthenticated, errCapabilityInvalid.Error()).WithCause(errCapabilityInvalid)
	case cred.revoked:
		return nil, connect.NewError(connect.CodeUnauthenticated, errCapabilityRevoked.Error()).WithCause(errCapabilityRevoked)
	case cred.biscuit:
		return nil, withReason(connect.NewError(connect.CodeFailedPrecondition, msgLeaseByCopy))
	case !slices.Contains(holder.msg.GetCaveats().GetOps(), string(paladin.OpShare)):
		return nil, withReason(connect.NewError(connect.CodePermissionDenied, msgLeaseWithoutShare))
	}
	return holder, nil
}

// limesRefusal answers a limes error as the server's capability errors do:
// the capability reason when limes names one, and the limes code always.
func limesRefusal(code connect.Code, err error) *connect.Error {
	e := connect.NewError(code, refusalMessage(err)).WithCause(err)
	info := &errdetails.ErrorInfo{Domain: paladin.ErrorDomain}
	if r, ok := limes.ReasonOf(err); ok {
		if v, ok := commonv1.ErrorReason_value[capabilityReasonPrefix+strings.ToUpper(string(r))]; ok {
			info.Reason = commonv1.ErrorReason(v).String()
		}
	}
	if c, ok := limes.CodeOf(err); ok {
		info.Metadata = map[string]string{paladin.ErrorMetadataLimesCode: string(c)}
	}
	detail, derr := connectproto.NewErrorDetail(info)
	if derr != nil {
		panic(fmt.Sprintf("paladintest: %v", derr)) // an ErrorInfo always marshals
	}
	return e.WithDetail(detail)
}

func nanosOf(m *money.Money) int64 {
	return m.GetUnits()*int64(limes.NanosPerUnit) + int64(m.GetNanos())
}
