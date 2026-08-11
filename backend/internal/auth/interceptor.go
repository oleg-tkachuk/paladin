package auth

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
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

// InterceptorSkipAPITokens is Interceptor that PASSES THROUGH (setting no principal) when
// the bearer is an PALADIN API token (`paladin_pat_…`), deferring authentication to a downstream
// APITokenAuthInterceptor. Every other case is handled exactly like Interceptor — a JWT is
// verified, and a missing/invalid non-PAT bearer is rejected — so auth stays mandatory.
// Used on the data plane so a caller may authenticate with EITHER an OIDC JWT or a PAT.
func InterceptorSkipAPITokens(v TokenVerifier) connect.Interceptor {
	return &authInterceptor{verifier: v, skipAPITokens: true}
}

type authInterceptor struct {
	verifier TokenVerifier
	// skipAPITokens lets `paladin_pat_…` bearers past this JWT gate untouched (no principal),
	// so a downstream APITokenAuthInterceptor authenticates them instead.
	skipAPITokens bool
}

// isAPIToken reports whether the Authorization header carries an PALADIN API token, which this
// interceptor should defer to the PAT interceptor rather than verify as a JWT.
func (a *authInterceptor) isAPIToken(authz string) bool {
	return a.skipAPITokens && extractAPIToken("", authz) != ""
}

func (a *authInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if a.isAPIToken(req.Header().Get("Authorization")) {
			return next(ctx, req) // a PAT — leave it for the API-token interceptor
		}
		p, err := principalFromHeaders(ctx, a.verifier, req.Header().Get("Authorization"))
		if err != nil {
			return nil, err
		}
		annotateSpan(ctx, p)
		return next(WithPrincipal(ctx, p), req)
	}
}

// annotateSpan stamps the caller's tenant onto the active RPC span (the
// otelconnect server span, created upstream of this interceptor). ADR-0001
// follow-up: gives traces a per-tenant dimension to filter on. tenant_id is
// bounded-cardinality (one per tenant); object_key is deliberately NOT set
// here — it is per-request and unbounded, so it stays a handler concern.
// No-op when OTel is disabled (the span is non-recording).
func annotateSpan(ctx context.Context, p *Principal) {
	if p == nil || p.TenantID == uuid.Nil {
		return
	}
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	attrs := []attribute.KeyValue{attribute.String("paladin.tenant_id", p.TenantID.String())}
	if p.TenantSlug != "" {
		attrs = append(attrs, attribute.String("paladin.tenant_slug", p.TenantSlug))
	}
	span.SetAttributes(attrs...)
}

func (a *authInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	// Server-side PALADIN: no outbound calls. Pass through unchanged.
	return next
}

func (a *authInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if a.isAPIToken(conn.RequestHeader().Get("Authorization")) {
			return next(ctx, conn) // a PAT — leave it for the API-token interceptor
		}
		p, err := principalFromHeaders(ctx, a.verifier, conn.RequestHeader().Get("Authorization"))
		if err != nil {
			return err
		}
		annotateSpan(ctx, p)
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
