package auth

import (
	"context"
	"errors"
	"net/http"
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

// InterceptorSkipAPITokens is Interceptor that PASSES THROUGH (setting no principal) when
// the bearer is a Paladin API token (`paladin_pat_…`), deferring authentication to a downstream
// APITokenAuthInterceptor. Every other case is handled exactly like Interceptor — a JWT is
// verified, and a missing/invalid non-PAT bearer is rejected — so auth stays mandatory.
// Used on the data plane so a caller may authenticate with EITHER an OIDC JWT or a PAT.
func InterceptorSkipAPITokens(v TokenVerifier) connect.Interceptor {
	return &authInterceptor{verifier: v, skipAPITokens: true}
}

// InterceptorSkipTokensAndCapabilities also passes through when the request
// carries a capability, deferring to a downstream establishing capability
// interceptor.
//
// Needed because a capability may be the WHOLE credential (ADR-0010) and rides
// in its own header: a capability-only request has no Authorization at all, so
// this gate rejected it as "missing Authorization header" before the capability
// could authenticate it. Auth stays mandatory — a request with neither a
// capability nor a bearer is still refused here, and a capability that fails
// verification is refused downstream rather than falling back to anonymous.
func InterceptorSkipTokensAndCapabilities(v TokenVerifier) connect.Interceptor {
	return &authInterceptor{verifier: v, skipAPITokens: true, skipCapabilities: true}
}

type authInterceptor struct {
	verifier TokenVerifier
	// skipAPITokens lets `paladin_pat_…` bearers past this JWT gate untouched (no principal),
	// so a downstream APITokenAuthInterceptor authenticates them instead.
	skipAPITokens bool
	// skipCapabilities lets a request carrying a capability past untouched, so a
	// downstream establishing capability interceptor authenticates it. A
	// capability-only request has no Authorization header at all.
	skipCapabilities bool
}

// isAPIToken reports whether the Authorization header carries a Paladin API token, which this
// interceptor should defer to the PAT interceptor rather than verify as a JWT.
func (a *authInterceptor) isAPIToken(authz string) bool {
	return a.skipAPITokens && extractAPIToken("", authz) != ""
}

// hasCapability reports whether the request presents a capability in either
// accepted form, so this gate can step aside for it.
func (a *authInterceptor) hasCapability(h http.Header) bool {
	return a.skipCapabilities &&
		extractCapabilityToken(h.Get(HeaderCapability), h.Get("Authorization")) != ""
}

func (a *authInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if a.isAPIToken(req.Header().Get("Authorization")) {
			return next(ctx, req) // a PAT — leave it for the API-token interceptor
		}
		if a.hasCapability(req.Header()) {
			return next(ctx, req) // a capability — leave it for the capability interceptor
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
// bounded-cardinality (one per tenant); collection is deliberately NOT set
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
	// Server-side Paladin: no outbound calls. Pass through unchanged.
	return next
}

func (a *authInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if a.isAPIToken(conn.RequestHeader().Get("Authorization")) {
			return next(ctx, conn) // a PAT — leave it for the API-token interceptor
		}
		if a.hasCapability(conn.RequestHeader()) {
			return next(ctx, conn) // a capability — leave it for the capability interceptor
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
