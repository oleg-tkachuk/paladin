// Package auth is the identity layer.
//
// The invariant this package enforces: request handlers MUST obtain the
// caller's tenant from context, never from request fields. Violating this
// is a class-of-bug source (wrong-tenant writes) that cannot be caught by
// code review alone.
//
// Wiring:
//
//	verifier := &auth.JWTVerifier{Key: pubkey, ExpectedIssuer: ..., ExpectedAudience: ...}
//	mux.Handle(path, connect.WithInterceptors(auth.Interceptor(verifier)))
//
// Inside a handler:
//
//	tenantID, err := auth.TenantFromContext(ctx)
//	if err != nil { return connect.NewError(connect.CodeUnauthenticated, err) }
//
// For cross-tenant admin RPCs (TenantService.*), read the full principal:
//
//	p, _ := auth.PrincipalFromContext(ctx)
//	if !p.HasRole("paladin:admin") {
//	    return connect.NewError(connect.CodePermissionDenied, ...)
//	}
package auth
