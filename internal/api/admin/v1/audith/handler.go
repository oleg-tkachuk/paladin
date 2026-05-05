// Package audith implements the admin AuditLogService — read access to the
// append-only audit log. Writes are produced by middleware on each
// admin/iam mutation.
package audith

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
)

type Handler struct {
	repo admindomain.AuditRepository
}

func NewHandler(r admindomain.AuditRepository) *Handler { return &Handler{repo: r} }

func (h *Handler) ListAuditLog(ctx context.Context, args admindomain.ListAuditArgs) ([]admindomain.AuditEntry, string, error) {
	caller, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, "", err
	}
	// Non-platform admins can see only their own tenant's entries.
	if !apiutil.HasRole(ctx, apiutil.RolePlatformAdmin) {
		if args.ActorTenantID != uuid.Nil && args.ActorTenantID != caller {
			return nil, "", connect.NewError(connect.CodePermissionDenied,
				errors.New("cross-tenant audit denied"))
		}
		args.ActorTenantID = caller
	}
	return h.repo.List(ctx, args)
}

func (h *Handler) GetAuditLogEntry(ctx context.Context, id uuid.UUID) (*admindomain.AuditEntry, error) {
	caller, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	e, err := h.repo.Get(ctx, id)
	if err != nil {
		if errors.Is(err, admindomain.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if !apiutil.HasRole(ctx, apiutil.RolePlatformAdmin) && e.ActorTenantID != caller {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("audit entry not found"))
	}
	return &e, nil
}

// ExportAuditLog is a stub — real implementation lives in slice 4 (worker +
// PresignedUrl issuance). Returns Unimplemented for now so the proto contract
// stays honest.
func (h *Handler) ExportAuditLog(ctx context.Context, _ string, _ string) error {
	return connect.NewError(connect.CodeUnimplemented, errors.New("ExportAuditLog: TODO slice 4"))
}
