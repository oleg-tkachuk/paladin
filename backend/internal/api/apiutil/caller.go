// Package apiutil holds cross-handler glue that would otherwise be duplicated
// across every handler package under internal/api/<plane>/v1/ — caller-context extraction,
// shared error mapping, and similar boundary helpers.
//
// Handlers depend on apiutil; apiutil never depends on handler packages.
package apiutil

import (
	"context"

	"connectrpc.com/connect/v2"
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
		return uuid.Nil, nil, connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return uuid.Nil, nil, connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	return tenantID, p, nil
}

// ActingContext is CallerContext for a handler that acts on the tenant its
// request names: the tenant the request acts on (auth.EffectiveTenant) — the
// one a platform admin named, set by the data plane's name parsing — or the
// caller's own. Database queries, storage keys and the Cedar resource all take
// this one value, and the RLS pool binds the same one, so they cannot
// disagree. Admin-plane handlers keep CallerContext, which answers who is
// calling rather than whose data is touched.
func ActingContext(ctx context.Context) (uuid.UUID, *auth.Principal, error) {
	tenantID, err := auth.EffectiveTenant(ctx)
	if err != nil {
		return uuid.Nil, nil, connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return uuid.Nil, nil, connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	return tenantID, p, nil
}
