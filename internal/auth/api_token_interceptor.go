package auth

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/internal/auth/api_token"
	"github.com/oleg-tkachuk/paladin/internal/auth/api_token/ratelimit"
)

// HeaderAPIToken is the additional accepted header for API tokens.
// Both `Authorization: Bearer paladin_pat_…` and `X-PALADIN-API-Token: paladin_pat_…`
// route to the same verifier; X-PALADIN wins on duplicate (explicit > overloaded).
const HeaderAPIToken = "X-PALADIN-API-Token"

// apiTokenKey is the context value the interceptor stashes the verified
// *api_token.Token under. Read via APITokenFromContext from handler code.
type apiTokenKey struct{}

// APITokenFromContext returns the verified API token for the request,
// or (nil, false) when none was supplied. Handlers gate on scopes /
// audience by reading this and switching on the result.
func APITokenFromContext(ctx context.Context) (*api_token.Token, bool) {
	t, ok := ctx.Value(apiTokenKey{}).(*api_token.Token)
	return t, ok && t != nil
}

// WithAPIToken stamps a verified API token onto a context. Public so
// tests can prepare contexts without going through the interceptor.
func WithAPIToken(ctx context.Context, t *api_token.Token) context.Context {
	if t == nil {
		return ctx
	}
	return context.WithValue(ctx, apiTokenKey{}, t)
}

// APITokenInterceptor is the additive Connect interceptor for API
// tokens. Mirrors CapabilityInterceptor in spirit: missing token =
// no-op, invalid = CodeUnauthenticated. Stamps *api_token.Token on
// the request context for handler-side gating.
//
// Distinct from CapabilityInterceptor:
//
//   - Reads `Authorization: Bearer paladin_pat_…` (NOT `Capability` scheme).
//     The literal `paladin_pat_` prefix is what disambiguates an PALADIN API
//     token from a federated OIDC bearer when both Authorization
//     headers compete; non-`paladin_pat_` Bearer values are left alone for
//     the JWT verifier downstream.
//
//   - Also accepts `X-PALADIN-API-Token` for clients that already use
//     Authorization for an OIDC bearer.
//
//   - Verification = DB lookup + argon2id compare (slow). The Verifier
//     handles its own caching strategy if any; the interceptor doesn't.
//
// audience is the plane label this interceptor is mounted on
// (data / admin / iam / mcp). Verifier rejects tokens whose
// `audience` array doesn't include it.
//
// When verifier is nil the returned interceptor is a passthrough, same
// shape as CapabilityInterceptor — disabled deploys pay zero cost.
//
// limiter is optional: when nil OR the verified token's RateLimitRPM
// is 0, the rate-limit gate is skipped entirely. When both are set,
// the interceptor calls Allow on every successful verify; on
// Decision.Allowed=false it returns CodeResourceExhausted with a
// Retry-After header so well-behaved clients back off.
func APITokenInterceptor(verifier *api_token.Verifier, audience string) connect.Interceptor {
	return APITokenInterceptorWithLimiter(verifier, nil, audience)
}

// APITokenInterceptorWithLimiter is the rate-limit-aware variant.
// Production wires both verifier and limiter from
// internal/app.APITokenBundle; tests / dev deploys can pass nil
// limiter and get verify-only semantics.
func APITokenInterceptorWithLimiter(verifier *api_token.Verifier, limiter ratelimit.Limiter, audience string) connect.Interceptor {
	if verifier == nil {
		return passthroughInterceptor{}
	}
	return &apiTokenInterceptor{verifier: verifier, limiter: limiter, audience: audience}
}

type apiTokenInterceptor struct {
	verifier *api_token.Verifier
	limiter  ratelimit.Limiter
	audience string
}

// rateLimitGate runs the limiter for a verified token. Returns nil to
// continue, or a connect.Error to short-circuit. Skipped entirely
// when the limiter is unconfigured or the token has no per-token cap.
//
// On deny the Retry-After header is attached to the connect.Error's
// Meta — connect-go propagates that to the response so well-behaved
// clients back off the right amount. Failing the limiter call itself
// fails open: a rate-limit infra outage shouldn't deny otherwise-
// valid requests; the token already passed signature / time / audience.
func (i *apiTokenInterceptor) rateLimitGate(ctx context.Context, tok *api_token.Token) error {
	if i.limiter == nil || tok.RateLimitRPM <= 0 {
		return nil
	}
	d, err := i.limiter.Allow(ctx, tok.ID, tok.RateLimitRPM)
	if err != nil || d.Allowed {
		return nil
	}
	ce := connect.NewError(connect.CodeResourceExhausted,
		fmt.Errorf("api_token: rate limit %d rpm exceeded (weighted=%.1f)", tok.RateLimitRPM, d.WeightedCount))
	if d.RetryAfter > 0 {
		ce.Meta().Set("Retry-After", strconv.Itoa(int(d.RetryAfter.Seconds())))
	}
	return ce
}

func (i *apiTokenInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		token := extractAPIToken(req.Header().Get(HeaderAPIToken), req.Header().Get("Authorization"))
		if token == "" {
			return next(ctx, req)
		}
		t, err := i.verifier.Verify(ctx, token, i.audience)
		if err != nil {
			return nil, mapAPITokenErr(err)
		}
		if err := i.rateLimitGate(ctx, t); err != nil {
			return nil, err
		}
		return next(WithAPIToken(ctx, t), req)
	}
}

func (i *apiTokenInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *apiTokenInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		token := extractAPIToken(conn.RequestHeader().Get(HeaderAPIToken), conn.RequestHeader().Get("Authorization"))
		if token == "" {
			return next(ctx, conn)
		}
		t, err := i.verifier.Verify(ctx, token, i.audience)
		if err != nil {
			return mapAPITokenErr(err)
		}
		if err := i.rateLimitGate(ctx, t); err != nil {
			return err
		}
		return next(WithAPIToken(ctx, t), conn)
	}
}

// extractAPIToken pulls the token from either supported header. Returns
// "" when no recognisable API token is present — Bearer values that
// don't start with `paladin_pat_` are skipped so the OIDC JWT verifier can
// process them downstream.
func extractAPIToken(xocp, authz string) string {
	if t := strings.TrimSpace(xocp); strings.HasPrefix(t, api_token.TokenPrefix) {
		return t
	}
	if authz == "" {
		return ""
	}
	parts := strings.SplitN(strings.TrimSpace(authz), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	candidate := strings.TrimSpace(parts[1])
	if !strings.HasPrefix(candidate, api_token.TokenPrefix) {
		return ""
	}
	return candidate
}

// mapAPITokenErr translates verifier sentinel errors to connect.Error.
// All denial paths use CodeUnauthenticated rather than PermissionDenied:
// API tokens are an *authentication* primitive (proves who you are);
// authorisation gates (scopes / Cedar) live downstream.
func mapAPITokenErr(err error) error {
	switch {
	case errors.Is(err, api_token.ErrTokenMalformed):
		return connect.NewError(connect.CodeUnauthenticated, err)
	case errors.Is(err, api_token.ErrTokenNotFound):
		return connect.NewError(connect.CodeUnauthenticated, err)
	case errors.Is(err, api_token.ErrTokenExpired):
		return connect.NewError(connect.CodeUnauthenticated, err)
	case errors.Is(err, api_token.ErrTokenRevoked):
		return connect.NewError(connect.CodeUnauthenticated, err)
	case errors.Is(err, api_token.ErrAudienceMismatch):
		return connect.NewError(connect.CodePermissionDenied, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}
