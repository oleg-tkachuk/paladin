package auth

import (
	"context"

	"connectrpc.com/connect/v2"

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

// The short plane labels an API token's audience lists, as
// APITokenService.Create takes them; principalAudienceFor maps each to the
// audience above.
const (
	TokenPlaneData  = "data"
	TokenPlaneAdmin = "admin"
	TokenPlaneIAM   = "iam"
)

// RequireAudience is a Connect interceptor that asserts the principal in the
// context was authenticated with the given audience. Use AFTER the auth
// Interceptor in the chain — it reads from context, not headers.
//
// This is the primary mechanism that prevents an `paladin-data` JWT (handed to a
// browser) from calling admin RPCs even if the routing layer mis-forwards.
func RequireAudience(want string) connect.ServerInterceptor {
	return (&audienceInterceptor{want: want}).intercept
}

type audienceInterceptor struct {
	want string
}

func (a *audienceInterceptor) intercept(next connect.ServerFunc) connect.ServerFunc {
	return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
		if err := a.check(ctx); err != nil {
			return err
		}
		return next(ctx, spec, stream)
	}
}

func (a *audienceInterceptor) check(ctx context.Context) error {
	p, err := PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	if p.Audience != a.want {
		return connect.Errorf(connect.CodePermissionDenied,
			"token audience %q is not allowed on %q plane", p.Audience, a.want)
	}
	return nil
}
