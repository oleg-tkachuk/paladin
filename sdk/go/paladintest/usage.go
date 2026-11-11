package paladintest

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"connectrpc.com/connect/v2"
	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/memstore"
	"github.com/oleg-tkachuk/limes/meter"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/types/known/timestamppb"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// usage counts the fake's capabilities' requests and keeps their budget
// leases, through limes's in-memory store. The server keeps the same in
// pgstore, and memstore passes the conformance suite pgstore does
// (storetest). So the fake counts and leases as the server does: an
// ancestor's ceiling binding its descendants, a request under a lease spent
// from it, a nested lease refused.
type usage struct {
	// records holds each capability the fake issued, under the fake's own
	// id, for the ceilings of a chain.
	records *memstore.Store
	store   *memstore.UsageStore
}

const (
	// usageIssuer is who the fake's capability records name as their issuer.
	usageIssuer = "paladintest-usage"
	// defaultBudgetUnit is the unit a budget named in none is in: the
	// server's capability.currency default.
	defaultBudgetUnit = limes.AbstractUnitCode
)

func newUsage() (*usage, error) {
	records, err := memstore.NewStore(memstore.StoreConfig{})
	if err != nil {
		return nil, err
	}
	store, err := memstore.NewUsageStore(memstore.UsageStoreConfig{Records: records})
	if err != nil {
		return nil, err
	}
	return &usage{records: records, store: store}, nil
}

// record puts c, which the fake just minted, on record, so its ceilings bind
// it and every capability delegated from it.
func (u *usage) record(c *adminv1.Capability) error {
	lc, err := usageCapability(c)
	if err != nil {
		return err
	}
	// A budget named in no unit is in its parent's, else in the server's
	// default currency, as the server reads one.
	if lc.Caveats.Budget.UnitCode == "" {
		lc.Caveats.Budget.UnitCode = defaultBudgetUnit
		if lc.ParentID != uuid.Nil() {
			parent, err := u.records.GetRecord(context.Background(), lc.ParentID)
			if err != nil {
				return err
			}
			lc.Caveats.Budget.UnitCode = parent.Capability.Caveats.Budget.UnitCode
		}
	}
	return u.records.Insert(context.Background(), limes.InsertRequest{
		Capability: lc,
		IssuedBy:   limes.Principal{Type: limes.PrincipalTypeService, TenantID: lc.Subject.TenantID, Subject: usageIssuer},
	})
}

// usageCapability is c as limes records it: what metering reads of a
// capability — its tenant, lineage, ceilings and lifetime.
func usageCapability(c *adminv1.Capability) (limes.Capability, error) {
	id, err := uuid.Parse(c.GetId())
	if err != nil {
		return limes.Capability{}, fmt.Errorf("capability id: %w", err)
	}
	tenant, err := uuid.Parse(c.GetSubject().GetTenantId())
	if err != nil {
		return limes.Capability{}, fmt.Errorf("subject tenant: %w", err)
	}
	var parent uuid.UUID
	if p := c.GetParentId(); p != "" {
		if parent, err = uuid.Parse(p); err != nil {
			return limes.Capability{}, fmt.Errorf("parent id: %w", err)
		}
	}
	caveats := limesCaveats(c.GetCaveats())
	caveats.Requests = requestCeiling(c.GetCaveats())
	caveats.Budget = budgetCeiling(c.GetCaveats())
	out := limes.Capability{
		ID:              id,
		ParentID:        parent,
		Issuer:          c.GetIssuer(),
		Subject:         limes.Principal{Type: limes.PrincipalTypeService, TenantID: tenant, Subject: c.GetSubject().GetSubject()},
		Audience:        c.GetAudience(),
		Caveats:         caveats,
		ConfirmationJKT: c.GetConfirmationJkt(),
		IssuedAt:        c.GetIssuedAt().AsTime(),
		ExpiresAt:       c.GetExpiresAt().AsTime(),
	}
	if c.GetNotBefore().IsValid() {
		out.NotBefore = c.GetNotBefore().AsTime()
	}
	return out, nil
}

// budgetCeiling is the budget ceiling caveats set, in its unit.
func budgetCeiling(caveats *adminv1.CapabilityCaveats) limes.BudgetCeiling {
	if b := caveats.GetMaxBudget(); b != nil {
		return limes.BudgetCeiling{Max: limes.Nanos(nanosOf(b)), UnitCode: b.GetCurrencyCode()}
	}
	return limes.BudgetCeiling{Unlimited: true, UnitCode: caveats.GetUnlimitedBudget().GetCurrencyCode()}
}

// subject is what the capability id is metered as, read from its record as
// the server reads it (limes.StoredMeterSubject).
func (u *usage) subject(id string) (limes.MeterSubject, error) {
	capID, err := uuid.Parse(id)
	if err != nil {
		return limes.MeterSubject{}, fmt.Errorf("%w: capability id %q", limes.ErrInvalidRequest, id)
	}
	return limes.StoredMeterSubject(context.Background(), u.records, capID)
}

// bump counts one request against the capability id and its ancestors, or
// refuses it when one of them has no room for it.
func (u *usage) bump(id string) error {
	subject, err := u.subject(id)
	if err != nil {
		return err
	}
	_, err = u.store.Bump(context.Background(), meter.BumpRequest{Subject: subject})
	return err
}

// settle ends the lease the capability id holds, if it holds one: the server
// settles a revoked capability's lease with its revoke.
func (u *usage) settle(id string) error {
	subject, err := u.subject(id)
	if err != nil {
		return err
	}
	_, err = u.store.SettleLease(context.Background(), meter.SettleLeaseRequest{Subject: subject})
	if errors.Is(err, meter.ErrLeaseNotFound) {
		return nil
	}
	return err
}

// leaseProto is l as the server answers it.
func leaseProto(l meter.Lease) *adminv1.CapabilityLease {
	return &adminv1.CapabilityLease{
		Id:            l.ID.String(),
		CapabilityId:  l.CapabilityID.String(),
		TenantId:      l.TenantID.String(),
		Amount:        moneyOf(l.UnitCode, l.Amount),
		Spent:         moneyOf(l.UnitCode, l.Spent),
		Requests:      l.Requests,
		RequestsSpent: l.RequestsSpent,
		ExpiresAt:     timestamppb.New(l.ExpiresAt),
	}
}

// moneyOf is n nanos of unit.
func moneyOf(unit string, n limes.Nanos) *money.Money {
	return &money.Money{CurrencyCode: unit, Units: int64(n) / int64(limes.NanosPerUnit), Nanos: int32(int64(n) % int64(limes.NanosPerUnit))}
}

// limesCategoryCodes is the transport code the server answers a limes
// error's category with (internal/auth's capabilityCategoryCodes).
var limesCategoryCodes = map[limes.Category]connect.Code{
	limes.CategoryUnauthenticated: connect.CodeUnauthenticated,
	limes.CategoryDenied:          connect.CodePermissionDenied,
	limes.CategoryExhausted:       connect.CodeResourceExhausted,
	limes.CategoryInvalid:         connect.CodeInvalidArgument,
	limes.CategoryNotFound:        connect.CodeFailedPrecondition,
	limes.CategoryConflict:        connect.CodeAborted,
	limes.CategoryAlreadyExists:   connect.CodeAlreadyExists,
	limes.CategoryPrecondition:    connect.CodeFailedPrecondition,
	limes.CategoryUnavailable:     connect.CodeUnavailable,
	limes.CategoryInternal:        connect.CodeInternal,
}

// leaseExceptionCodes are the limes codes the server answers a lease call
// otherwise than by their category: a capability or a lease not there is
// NotFound (capabilityh's leaseExceptions).
var leaseExceptionCodes = map[limes.Code]connect.Code{
	limes.CodeNotFound:      connect.CodeNotFound,
	meter.CodeLeaseNotFound: connect.CodeNotFound,
}

// delegationInsertCodes are the limes codes the server answers a capability
// limes's store refuses to record otherwise than by their category
// (capabilityh's delegationExceptions): a child in another budget unit than
// its parent is denied, one past the delegation depth a precondition.
var delegationInsertCodes = map[limes.Code]connect.Code{
	limes.CodeUnitCodeMismatch:  connect.CodePermissionDenied,
	limes.CodeDelegationTooDeep: connect.CodeFailedPrecondition,
	limes.CodeTenantErased:      connect.CodeFailedPrecondition,
}

// limesStatus is the code the server answers err with: its limes code's
// row in except, else its category's, else Internal, the server's own fault.
func limesStatus(err error, except map[limes.Code]connect.Code) connect.Code {
	code, ok := limes.CodeOf(err)
	if !ok {
		return connect.CodeInternal
	}
	if c, ok := except[code]; ok {
		return c
	}
	if category, ok := limes.CategoryOfCode(code); ok {
		if c, ok := limesCategoryCodes[category]; ok {
			return c
		}
	}
	return connect.CodeInternal
}
