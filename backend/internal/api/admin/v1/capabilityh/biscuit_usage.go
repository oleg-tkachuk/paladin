package capabilityh

import (
	"context"
	"errors"
	"fmt"

	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"

	"connectrpc.com/connect/v2"

	"google.golang.org/genproto/googleapis/type/money"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// WithCopyUsage wires GetBiscuitUsage, beside WithBiscuitCopies. Without it
// the RPC answers Unavailable.
func (h *Handler) WithCopyUsage(reader limes.CopyUsageReader) *Handler {
	h.copyUsage = reader
	return h
}

// GetBiscuitUsage reports the limits in force on one copy of a capability's
// Biscuit and what each has counted. Same permission and scoping as
// GetUsage: a caller confined to its own tenant learns nothing about another
// tenant's capability, not even that it exists.
func (h *Handler) GetBiscuitUsage(ctx context.Context, req *adminv1.CapabilityServiceGetBiscuitUsageRequest) (*adminv1.CapabilityServiceGetBiscuitUsageResponse, error) {
	caller, err := h.authorize(ctx, cedar.ActionReadCapability)
	if err != nil {
		return nil, err
	}
	if h.copier == nil || h.copyUsage == nil {
		return nil, connect.NewError(connect.CodeUnavailable, "biscuit copy usage not wired")
	}
	c, err := h.copier.BiscuitCopy(ctx, req.GetToken())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("token: %w", err))
	}
	ctx, err = h.actOnCapabilitysTenant(ctx, caller, c.CapabilityID())
	if err != nil {
		return nil, err
	}
	// The record, read under the caller's scoping, is both the visibility
	// check and the unit the copies' budgets are in.
	record, err := h.store.GetRecord(ctx, c.CapabilityID())
	if err != nil {
		if errors.Is(err, limes.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err.Error()).WithCause(err)
		}
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}

	copies := c.Copies()
	byID := map[string]limes.CopyUsage{}
	if len(copies) > 0 {
		ids := make([][]byte, 0, len(copies))
		for _, l := range copies {
			ids = append(ids, l.RevocationID)
		}
		counted, err := h.copyUsage.CopyUsage(ctx, limes.CopyUsageRequest{RevocationIDs: ids})
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		for _, u := range counted.Copies {
			byID[string(u.RevocationID)] = u
		}
	}

	// A copy's amounts are in its capability's unit.
	unit := record.Capability.Caveats.UnitCode
	out := make([]*adminv1.CapabilityBiscuitCopyUsage, 0, len(copies))
	for _, l := range copies {
		u := byID[string(l.RevocationID)] // a copy never used counts zero
		out = append(out, &adminv1.CapabilityBiscuitCopyUsage{
			RevocationId: l.RevocationID,
			MaxRequests:  copyRequestLimit(l),
			MaxBudget:    copyBudgetLimit(l, unit),
			RequestCount: u.RequestCount,
			Spent:        apiutil.MoneyOf(unit, u.SpentAmount),
			Reserved:     apiutil.MoneyOf(unit, u.ReservedAmount),
		})
	}
	return &adminv1.CapabilityServiceGetBiscuitUsageResponse{
		CapabilityId: c.CapabilityID().String(),
		Copies:       out,
	}, nil
}

// copyRequestLimit is a copy's own request limit, nil when the copy set
// none. A copy only narrows, so its limit is never unlimited; one that were
// would limit nothing, and reads as none.
func copyRequestLimit(c limes.CopyCeiling) *int64 {
	if c.Requests == nil || c.Requests.Unlimited {
		return nil
	}
	n := c.Requests.Max
	return &n
}

// copyBudgetLimit is a copy's own budget in unit, nil when the copy set none.
func copyBudgetLimit(c limes.CopyCeiling, unit string) *money.Money {
	if c.Budget == nil || c.Budget.Unlimited {
		return nil
	}
	return apiutil.MoneyOf(unit, c.Budget.Max)
}
