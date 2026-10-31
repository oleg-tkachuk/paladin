package capabilityh

import (
	"context"
	"fmt"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// RunRevoker revokes every capability of an agent run in a tenant and
// releases their holds; auth.BulkRevocation implements it.
type RunRevoker interface {
	RevokeRun(ctx context.Context, tenant, run uuid.UUID, note, actor string) (int64, error)
}

// WithRunRevocation wires RevokeRun. Without it RevokeRun answers
// Unavailable.
func (h *Handler) WithRunRevocation(runs RunRevoker) *Handler {
	h.runs = runs
	return h
}

// runsSegment is the path segment that names an agent run under its tenant,
// in the audit log: tenants/{tenant}/runs/{run}.
const runsSegment = "/runs/"

// errRevokeRunAcrossTenants refuses a caller confined to its own tenant
// another tenant's run.
const errRevokeRunAcrossTenants = "revoking another tenant's run requires platform.admin or platform.capability-issuer"

// RevokeRun revokes every capability of one agent run in a tenant, and
// releases their holds: the caller's own tenant's run, or for a caller
// spanning tenants any tenant's. A repeat revokes nothing and reports none.
func (h *Handler) RevokeRun(ctx context.Context, req *adminv1.CapabilityServiceRevokeRunRequest) (*adminv1.CapabilityServiceRevokeRunResponse, error) {
	caller, err := h.authorize(ctx, cedar.ActionRevokeCapability)
	if err != nil {
		return nil, err
	}
	if h.runs == nil {
		return nil, connect.Errorf(connect.CodeUnavailable, "run revocation not wired")
	}
	tenant, err := uuid.Parse(req.GetTenantId())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("tenant_id: %w", err))
	}
	run, err := uuid.Parse(req.GetRunId())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("run_id: %w", err))
	}
	// Cedar authorised the action against the caller's own tenant, not the
	// one named, as Issue and List check it.
	if tenant != caller.TenantID && !spansTenants(caller) {
		return nil, connect.NewError(connect.CodePermissionDenied, errRevokeRunAcrossTenants)
	}
	revoked, err := h.runs.RevokeRun(auth.WithActingTenant(ctx, tenant), tenant, run, req.GetReason(), caller.Subject)
	if err != nil {
		return nil, auth.AnswerCapabilityError(fmt.Errorf("revoke run %s: %w", run, err), nil)
	}
	// The audit row names the run under its tenant, so it shows in that
	// tenant's trail.
	apiutil.StashResource(ctx, apiutil.TenantNamePrefix+tenant.String()+runsSegment+run.String())
	return &adminv1.CapabilityServiceRevokeRunResponse{Revoked: revoked}, nil
}
