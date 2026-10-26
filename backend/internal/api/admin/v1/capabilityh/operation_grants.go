package capabilityh

import (
	"context"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"

	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// ListOperationGrants serves cedar.CapabilityOperationGrants: what each
// capability operation unlocks with no tenant policy. It reads no tenant, so
// a caller who may read capabilities at all sees the same table.
func (h *Handler) ListOperationGrants(ctx context.Context, _ *adminv1.CapabilityServiceListOperationGrantsRequest) (*adminv1.CapabilityServiceListOperationGrantsResponse, error) {
	if _, err := h.authorize(ctx, cedar.ActionReadCapability); err != nil {
		return nil, err
	}
	return &adminv1.CapabilityServiceListOperationGrantsResponse{Grants: operationGrantsToProto(cedar.CapabilityOperationGrants)}, nil
}

func operationGrantsToProto(rows []cedar.CapabilityOperationGrant) []*adminv1.CapabilityOperationGrant {
	out := make([]*adminv1.CapabilityOperationGrant, 0, len(rows))
	for _, row := range rows {
		actions := make([]string, 0, len(row.Actions))
		for _, a := range row.Actions {
			actions = append(actions, a.String())
		}
		out = append(out, &adminv1.CapabilityOperationGrant{
			Op:         row.Op,
			Actions:    actions,
			Basis:      grantBasisToProto[row.Basis],
			PairedOps:  row.PairedOps,
			Procedures: row.Procedures,
		})
	}
	return out
}

// grantBasisToProto maps the engine's basis onto the wire enum; a basis
// missing here reaches the wire as UNSPECIFIED, which a test refuses.
var grantBasisToProto = map[cedar.GrantBasis]adminv1.CapabilityGrantBasis{
	cedar.GrantBuiltin:      adminv1.CapabilityGrantBasis_CAPABILITY_GRANT_BASIS_BUILTIN,
	cedar.GrantTenantPolicy: adminv1.CapabilityGrantBasis_CAPABILITY_GRANT_BASIS_TENANT_POLICY,
	cedar.GrantPaired:       adminv1.CapabilityGrantBasis_CAPABILITY_GRANT_BASIS_PAIRED,
	cedar.GrantCaveatsOnly:  adminv1.CapabilityGrantBasis_CAPABILITY_GRANT_BASIS_CAVEATS_ONLY,
}
