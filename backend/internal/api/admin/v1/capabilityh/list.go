package capabilityh

import (
	"context"
	"fmt"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// WithRecords wires List and Get: capabilities with their dispositions.
// Without it both RPCs answer Unavailable.
func (h *Handler) WithRecords(records limes.RecordReporter) *Handler {
	h.records = records
	return h
}

// List pages through a tenant's capabilities, each with its disposition.
func (h *Handler) List(ctx context.Context, req *adminv1.CapabilityServiceListRequest) (*adminv1.CapabilityServiceListResponse, error) {
	caller, err := h.authorize(ctx, cedar.ActionReadCapability)
	if err != nil {
		return nil, err
	}
	if h.records == nil {
		return nil, connect.Errorf(connect.CodeUnavailable, "capability record listing not wired")
	}

	tenantID, err := uuid.Parse(req.GetTenantId())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("tenant_id: %w", err))
	}
	// Same gate as Issue: ReadCapability in a tenant's own policy must not become a
	// read of every other tenant's capabilities. Cedar above authorised the
	// ACTION against the caller's own tenant, not against this one.
	if tenantID != caller.TenantID && !spansTenants(caller) {
		return nil, connect.NewError(connect.CodePermissionDenied, "listing another tenant's capabilities requires platform.admin or platform.capability-issuer")
	}
	// Capability records are RLS-isolated, so reading another tenant's rows
	// needs the connection scoped to that tenant. Without this the admin
	// console renders an empty list instead of an error — RLS filters.
	ctx = auth.WithActingTenant(ctx, tenantID)
	page, err := h.records.ListRecords(ctx, limes.ListRecordsRequest{
		TenantID:      tenantID,
		PrincipalType: protoToPrincipalKind(req.GetPrincipalKind()),
		Subject:       req.GetSubject(),
		Dispositions:  listDispositions(req),
		Order:         listOrder(req.GetSortOrder()),
		Cursor:        req.GetPageToken(),
		Limit:         int(req.GetPageSize()),
	})
	if err != nil {
		return nil, auth.AnswerCapabilityError(err, listExceptions)
	}

	out := make([]*adminv1.Capability, 0, len(page.Items))
	for i := range page.Items {
		c := capabilityToProto(&page.Items[i].Record.Capability)
		c.Disposition = dispositionToProto(page.Items[i].Disposition)
		out = append(out, c)
	}
	return &adminv1.CapabilityServiceListResponse{
		Capabilities:  out,
		NextPageToken: page.Next,
	}, nil
}

// listDispositions is what req lists: its dispositions, or, when it names
// none, the active capabilities and those its deprecated include flags add.
func listDispositions(req *adminv1.CapabilityServiceListRequest) []limes.RecordDisposition {
	if named := req.GetDispositions(); len(named) > 0 {
		out := make([]limes.RecordDisposition, 0, len(named))
		for _, d := range named {
			out = append(out, protoToDisposition(d))
		}
		return out
	}
	out := []limes.RecordDisposition{limes.RecordDispositionActive}
	if req.GetIncludeExpired() { //nolint:staticcheck // SA1019: the deprecated flags still widen a request that names no dispositions
		out = append(out, limes.RecordDispositionExpired)
	}
	if req.GetIncludeRevoked() { //nolint:staticcheck // SA1019: as above
		out = append(out, limes.RecordDispositionRevoked)
	}
	return out
}

// listOrder is the id order of a sort order: descending for DESC, else
// ascending, the order List paged in before it took one.
func listOrder(o commonv1.SortOrder) limes.ListOrder {
	if o == commonv1.SortOrder_SORT_ORDER_DESC {
		return limes.ListOrderDescending
	}
	return limes.ListOrderAscending
}

func protoToDisposition(d adminv1.CapabilityDisposition) limes.RecordDisposition {
	switch d {
	case adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_ACTIVE:
		return limes.RecordDispositionActive
	case adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_EXPIRED:
		return limes.RecordDispositionExpired
	case adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_REVOKED:
		return limes.RecordDispositionRevoked
	default:
		return limes.RecordDispositionUnspecified
	}
}

func dispositionToProto(d limes.RecordDisposition) adminv1.CapabilityDisposition {
	switch d {
	case limes.RecordDispositionActive:
		return adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_ACTIVE
	case limes.RecordDispositionExpired:
		return adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_EXPIRED
	case limes.RecordDispositionRevoked:
		return adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_REVOKED
	default:
		return adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_UNSPECIFIED
	}
}
