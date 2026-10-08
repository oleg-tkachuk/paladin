package auth

import (
	"context"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
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

// Intercept authenticates every call, unary or streaming, from its
// Authorization header.
func (p *PermissiveInterceptor) Intercept(next connect.ServerFunc) connect.ServerFunc {
	return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
		ctx, err := p.attach(ctx, unary.Info(ctx).RequestHeader().Get("Authorization"), spec.Procedure)
		if err != nil {
			return err
		}
		return next(ctx, spec, stream)
	}
}

func (p *PermissiveInterceptor) attach(ctx context.Context, authz, procedure string) (context.Context, error) {
	if authz == "" {
		if p.allowsMissing(procedure) {
			return ctx, nil
		}
		return ctx, rpcerr.New(connect.CodeUnauthenticated, missingAuthErr())
	}
	const bearer = "Bearer "
	if !strings.HasPrefix(authz, bearer) {
		return ctx, rpcerr.New(connect.CodeUnauthenticated, expectedBearerErr())
	}
	token := strings.TrimSpace(authz[len(bearer):])
	if token == "" {
		return ctx, rpcerr.New(connect.CodeUnauthenticated, emptyTokenErr())
	}
	principal, err := p.Verifier.Verify(ctx, token)
	if err != nil {
		return ctx, connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
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
