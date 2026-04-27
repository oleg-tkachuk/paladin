package auth

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
)

// TokenVerifier validates a bearer token and returns the derived Principal.
// Implementations may use JWT, PASETO, OAuth introspection, etc.
type TokenVerifier interface {
	Verify(ctx context.Context, token string) (*Principal, error)
}

// Interceptor is a Connect interceptor that extracts the Authorization
// header, verifies the bearer token via the provided Verifier, and attaches
// the resulting Principal to the context for both unary and streaming RPCs.
//
// Handlers rely exclusively on context for identity — the token never leaks
// beyond this interceptor.
func Interceptor(v TokenVerifier) connect.Interceptor {
	return &authInterceptor{verifier: v}
}

type authInterceptor struct {
	verifier TokenVerifier
}

func (a *authInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		p, err := principalFromHeaders(ctx, a.verifier, req.Header().Get("Authorization"))
		if err != nil {
			return nil, err
		}
		return next(WithPrincipal(ctx, p), req)
	}
}

func (a *authInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	// Server-side PALADIN: no outbound calls. Pass through unchanged.
	return next
}

func (a *authInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		p, err := principalFromHeaders(ctx, a.verifier, conn.RequestHeader().Get("Authorization"))
		if err != nil {
			return err
		}
		return next(WithPrincipal(ctx, p), conn)
	}
}

func principalFromHeaders(ctx context.Context, v TokenVerifier, authz string) (*Principal, error) {
	if authz == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("missing Authorization header"))
	}
	const bearer = "Bearer "
	if !strings.HasPrefix(authz, bearer) {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("expected Bearer token"))
	}
	token := strings.TrimSpace(authz[len(bearer):])
	if token == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("empty token"))
	}
	p, err := v.Verify(ctx, token)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	return p, nil
}
