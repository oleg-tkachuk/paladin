package auth

import (
	"context"
	"strings"

	"connectrpc.com/connect"
)

// PermissiveInterceptor verifies a bearer token *if* present and skips auth
// silently when the Authorization header is absent. Designed for the IAM
// plane, which serves both unauthenticated RPCs (Login, RefreshToken) and
// authenticated ones (WhoAmI, UserService.*) through the same mux.
//
// Per-RPC role/audience enforcement is the job of downstream interceptors
// (RequireAudience / RequireRole) — those check the principal-from-context
// and naturally reject unauthenticated calls to gated RPCs.
//
// AllowMissingFor is the per-procedure allowlist: requests targeting these
// procedures are permitted to pass through with no Principal in context.
// Any other procedure WITHOUT an Authorization header still produces
// CodeUnauthenticated.
type PermissiveInterceptor struct {
	Verifier        TokenVerifier
	AllowMissingFor []string
}

func NewPermissiveInterceptor(v TokenVerifier, allowMissingFor ...string) *PermissiveInterceptor {
	return &PermissiveInterceptor{Verifier: v, AllowMissingFor: allowMissingFor}
}

func (p *PermissiveInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		ctx, err := p.attach(ctx, req.Header().Get("Authorization"), req.Spec().Procedure)
		if err != nil {
			return nil, err
		}
		return next(ctx, req)
	}
}

func (p *PermissiveInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (p *PermissiveInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		ctx, err := p.attach(ctx, conn.RequestHeader().Get("Authorization"), conn.Spec().Procedure)
		if err != nil {
			return err
		}
		return next(ctx, conn)
	}
}

func (p *PermissiveInterceptor) attach(ctx context.Context, authz, procedure string) (context.Context, error) {
	if authz == "" {
		if p.allowsMissing(procedure) {
			return ctx, nil
		}
		return ctx, connect.NewError(connect.CodeUnauthenticated,
			missingAuthErr())
	}
	const bearer = "Bearer "
	if !strings.HasPrefix(authz, bearer) {
		return ctx, connect.NewError(connect.CodeUnauthenticated, expectedBearerErr())
	}
	token := strings.TrimSpace(authz[len(bearer):])
	if token == "" {
		return ctx, connect.NewError(connect.CodeUnauthenticated, emptyTokenErr())
	}
	principal, err := p.Verifier.Verify(ctx, token)
	if err != nil {
		return ctx, connect.NewError(connect.CodeUnauthenticated, err)
	}
	return WithPrincipal(ctx, principal), nil
}

func (p *PermissiveInterceptor) allowsMissing(procedure string) bool {
	for _, allowed := range p.AllowMissingFor {
		if procedure == allowed {
			return true
		}
		// Also accept a method-suffix match: "Login" matches
		// "/paladin.iam.v1.AuthService/Login".
		if i := strings.LastIndexByte(procedure, '/'); i >= 0 && procedure[i+1:] == allowed {
			return true
		}
	}
	return false
}

// Sentinel errors kept unexported so callers depend only on the connect.Code.
type errMsg string

func (e errMsg) Error() string { return string(e) }

func missingAuthErr() error    { return errMsg("missing Authorization header") }
func expectedBearerErr() error { return errMsg("expected Bearer token") }
func emptyTokenErr() error     { return errMsg("empty token") }
