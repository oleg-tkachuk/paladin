// Package apiutil holds cross-handler glue that would otherwise be duplicated
// across every handler package under internal/api/<plane>/v1/ — caller-context extraction,
// shared error mapping, and similar boundary helpers.
//
// Handlers depend on apiutil; apiutil never depends on handler packages.
package apiutil

import (
	"context"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// CallerContext extracts (tenant_id, principal) from the request context and
// maps the auth-package errors to the canonical Connect Unauthenticated code.
// It replaces the per-package callerContext / authzContext helpers that were
// drifting out of sync across the v1 handlers.
func CallerContext(ctx context.Context) (uuid.UUID, *auth.Principal, error) {
	tenantID, err := auth.TenantFromContext(ctx)
	if err != nil {
		return uuid.Nil, nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return uuid.Nil, nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	return tenantID, p, nil
}
