package capabilityh

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/capability"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// WithCopyUsage wires GetBiscuitUsage, beside WithBiscuitCopies. Without it
// the RPC answers Unavailable.
func (h *Handler) WithCopyUsage(reader capability.CopyUsageReader) *Handler {
	h.copyUsage = reader
	return h
}

// GetBiscuitUsage reports the limits in force on one copy of a capability's
// Biscuit and what each has counted. Same permission and scoping as
// GetUsage: a caller confined to its own tenant learns nothing about another
// tenant's capability, not even that it exists.
func (h *Handler) GetBiscuitUsage(ctx context.Context, req *connect.Request[adminv1.CapabilityServiceGetBiscuitUsageRequest]) (*connect.Response[adminv1.CapabilityServiceGetBiscuitUsageResponse], error) {
	caller, err := h.authorize(ctx, cedar.ActionReadCapability)
	if err != nil {
		return nil, err
	}
	if h.copier == nil || h.copyUsage == nil {
		return nil, connect.NewError(connect.CodeUnavailable,
			errors.New("biscuit copy usage not wired"))
	}
	c, err := h.copier.BiscuitCopy(ctx, req.Msg.GetToken())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("token: %w", err))
	}
	ctx, err = h.actOnCapabilitysTenant(ctx, caller, c.CapabilityID)
	if err != nil {
		return nil, err
	}
	// The record, read under the caller's scoping, is both the visibility
	// check and the unit the copies' budgets are in.
	record, err := h.store.Get(ctx, c.CapabilityID)
	if err != nil {
		if errors.Is(err, capability.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	ids := make([][]byte, 0, len(c.Limits))
	for _, l := range c.Limits {
		ids = append(ids, l.RevocationID)
	}
	counted, err := h.copyUsage.CopyUsage(ctx, ids)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	byID := make(map[string]capability.CopyUsage, len(counted))
	for _, u := range counted {
		byID[string(u.RevocationID)] = u
	}

	out := make([]*adminv1.CapabilityBiscuitCopyUsage, 0, len(c.Limits))
	for _, l := range c.Limits {
		u := byID[string(l.RevocationID)] // a copy never used counts zero
		out = append(out, &adminv1.CapabilityBiscuitCopyUsage{
			RevocationId:    l.RevocationID,
			MaxRequests:     l.MaxRequests,
			MaxBudgetMicros: l.MaxBudgetMicros,
			RequestCount:    u.RequestCount,
			SpentMicros:     apiutil.Micros(u.SpentAmount),
			ReservedMicros:  apiutil.Micros(u.ReservedAmount),
		})
	}
	unit := record.Caveats.UnitCode
	if unit == "" {
		unit = capability.DefaultUnitCode
	}
	return connect.NewResponse(&adminv1.CapabilityServiceGetBiscuitUsageResponse{
		CapabilityId: c.CapabilityID.String(),
		UnitCode:     unit,
		Copies:       out,
	}), nil
}
