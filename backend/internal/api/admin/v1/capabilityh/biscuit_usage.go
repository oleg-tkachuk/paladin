package capabilityh

import (
	"context"
	"fmt"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"

	"connectrpc.com/connect/v2"

	"google.golang.org/genproto/googleapis/type/money"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// WithCopyUsage wires GetBiscuitUsage, beside WithBiscuitCopies. Without it
// the RPC answers Unavailable.
func (h *Handler) WithCopyUsage(reader meter.CopyUsageReader) *Handler {
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
		return nil, h.answer("get biscuit usage", err, recordExceptions)
	}

	copies := c.Copies()
	byID, err := h.copyUsageByID(ctx, c.CapabilityID(), copies)
	if err != nil {
		return nil, err
	}
	out := copyUsageRows(copies, byID, record.Capability.Caveats.Budget.UnitCode)
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

// copyUsageByID reads what each copy has used, keyed by its revocation id.
func (h *Handler) copyUsageByID(ctx context.Context, capabilityID uuid.UUID, copies []limes.CopyCeiling) (map[string]meter.CopyUsage, error) {
	byID := map[string]meter.CopyUsage{}
	if len(copies) == 0 {
		return byID, nil
	}
	ids := make([][]byte, 0, len(copies))
	for _, l := range copies {
		ids = append(ids, l.RevocationID)
	}
	counted, err := h.copyUsage.CopyUsage(ctx, meter.CopyUsageRequest{CapabilityID: capabilityID, RevocationIDs: ids})
	if err != nil {
		return nil, h.answer("get biscuit usage", err, nil)
	}
	for _, u := range counted.Copies {
		byID[string(u.RevocationID)] = u
	}
	return byID, nil
}

// copyUsageRows is each copy with its limits and what it has used, in unit;
// a copy never used counts zero.
func copyUsageRows(copies []limes.CopyCeiling, byID map[string]meter.CopyUsage, unit string) []*adminv1.CapabilityBiscuitCopyUsage {
	out := make([]*adminv1.CapabilityBiscuitCopyUsage, 0, len(copies))
	for _, l := range copies {
		u := byID[string(l.RevocationID)]
		out = append(out, &adminv1.CapabilityBiscuitCopyUsage{
			RevocationId: l.RevocationID,
			MaxRequests:  copyRequestLimit(l),
			MaxBudget:    copyBudgetLimit(l, unit),
			RequestCount: u.RequestCount,
			Spent:        apiutil.MoneyOf(unit, u.SpentAmount),
			Reserved:     apiutil.MoneyOf(unit, u.ReservedAmount),
		})
	}
	return out
}
