package auth

import (
	"context"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary"
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
func InterceptorSkipAPITokens(v TokenVerifier) connect.ServerInterceptor {
	return (&authInterceptor{verifier: v, skipAPITokens: true}).intercept
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
func InterceptorSkipTokensAndCapabilities(v TokenVerifier) connect.ServerInterceptor {
	return (&authInterceptor{verifier: v, skipAPITokens: true, skipCapabilities: true}).intercept
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

// isAPIToken reports whether the request carries a Paladin API token — in
// X-Paladin-API-Token or as a bearer — which this interceptor should defer to
// the PAT interceptor rather than verify as a JWT.
func (a *authInterceptor) isAPIToken(h *connect.Header) bool {
	return a.skipAPITokens && extractAPIToken(h.Get(HeaderAPIToken), h.Get("Authorization")) != ""
}

// hasCapability reports whether the request presents a capability in either
// accepted form, so this gate can step aside for it.
func (a *authInterceptor) hasCapability(h *connect.Header) bool {
	return a.skipCapabilities &&
		extractCapabilityToken(h.Get(HeaderCapability), h.Get("Authorization")) != ""
}

// intercept authenticates every call, unary or streaming, from its headers.
func (a *authInterceptor) intercept(next connect.ServerFunc) connect.ServerFunc {
	return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
		h := unary.Info(ctx).RequestHeader()
		if a.isAPIToken(h) {
			return next(ctx, spec, stream) // a PAT — leave it for the API-token interceptor
		}
		if a.hasCapability(h) {
			return next(ctx, spec, stream) // a capability — leave it for the capability interceptor
		}
		p, err := principalFromHeaders(ctx, a.verifier, h.Get("Authorization"))
		if err != nil {
			return err
		}
		annotateSpan(ctx, p)
		return next(WithPrincipal(ctx, p), spec, stream)
	}
}

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

func principalFromHeaders(ctx context.Context, v TokenVerifier, authz string) (*Principal, error) {
	if authz == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, "missing Authorization header")
	}
	const bearer = "Bearer "
	if !strings.HasPrefix(authz, bearer) {
		return nil, connect.NewError(connect.CodeUnauthenticated, "expected Bearer token")
	}
	token := strings.TrimSpace(authz[len(bearer):])
	if token == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, "empty token")
	}
	p, err := v.Verify(ctx, token)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	return p, nil
}
