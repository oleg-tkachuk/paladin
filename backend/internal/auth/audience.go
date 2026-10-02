package auth

import (
	"context"
	"fmt"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// Standard audiences. Each Connect mux is wrapped with RequireAudience to
// reject tokens issued for a different plane. Defined in the Go SDK, whose
// sessions ask for them, so a client and the server cannot spell one
// differently.
const (
	AudienceData  = paladin.AudienceData
	AudienceAdmin = paladin.AudienceAdmin
	AudienceIAM   = paladin.AudienceIAM
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
