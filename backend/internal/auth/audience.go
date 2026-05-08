package auth

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
)

// Standard audiences. Each Connect mux is wrapped with RequireAudience to
// reject tokens issued for a different plane.
const (
	AudienceData  = "paladin-data"
	AudienceAdmin = "paladin-admin"
	AudienceIAM   = "paladin-iam"
)

// RequireAudience is a Connect interceptor that asserts the principal in the
// context was authenticated with the given audience. Use AFTER the auth
// Interceptor in the chain — it reads from context, not headers.
//
// This is the primary mechanism that prevents an `paladin-data` JWT (handed to a
// browser) from calling admin RPCs even if the routing layer mis-forwards.
func RequireAudience(want string) connect.Interceptor {
	return &audienceInterceptor{want: want}
}

type audienceInterceptor struct {
	want string
}

func (a *audienceInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if err := a.check(ctx); err != nil {
			return nil, err
		}
		return next(ctx, req)
	}
}

func (a *audienceInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (a *audienceInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if err := a.check(ctx); err != nil {
			return err
		}
		return next(ctx, conn)
	}
}

func (a *audienceInterceptor) check(ctx context.Context) error {
	p, err := PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	if p.Audience != a.want {
		return connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("token audience %q is not allowed on %q plane", p.Audience, a.want))
	}
	return nil
}

// RequireRole returns an interceptor that admits only principals that hold
// at least one of the listed roles. Useful at admin-plane mux level to
// short-circuit before Cedar evaluation.
func RequireRole(roles ...string) connect.Interceptor {
	if len(roles) == 0 {
		panic("auth: RequireRole called without roles")
	}
	return &roleInterceptor{want: roles}
}

type roleInterceptor struct {
	want []string
}

func (r *roleInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if err := r.check(ctx); err != nil {
			return nil, err
		}
		return next(ctx, req)
	}
}

func (r *roleInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (r *roleInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if err := r.check(ctx); err != nil {
			return err
		}
		return next(ctx, conn)
	}
}

func (r *roleInterceptor) check(ctx context.Context) error {
	p, err := PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	for _, want := range r.want {
		if p.HasRole(want) {
			return nil
		}
	}
	return connect.NewError(connect.CodePermissionDenied,
		errors.New("missing required role"))
}
