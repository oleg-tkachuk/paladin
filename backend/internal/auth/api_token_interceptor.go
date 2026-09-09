package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/internal/auth/api_token"
	"github.com/oleg-tkachuk/paladin/internal/auth/api_token/ratelimit"
)

// HeaderAPIToken is the additional accepted header for API tokens.
// Both `Authorization: Bearer paladin_pat_…` and `X-Paladin-API-Token: paladin_pat_…`
// route to the same verifier; X-Paladin wins on duplicate (explicit > overloaded).
const HeaderAPIToken = "X-Paladin-API-Token" // #nosec G101 -- an HTTP header name, not a credential

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
//     The literal `paladin_pat_` prefix is what disambiguates a Paladin API
//     token from a federated OIDC bearer when both Authorization
//     headers compete; non-`paladin_pat_` Bearer values are left alone for
//     the JWT verifier downstream.
//
//   - Also accepts `X-Paladin-API-Token` for clients that already use
//     Authorization for an OIDC bearer.
//
//   - Verification = one indexed lookup by HMAC digest. The Verifier
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

// APITokenAuthInterceptor is APITokenInterceptorWithLimiter that ALSO establishes an
// auth.Principal from a valid token — so an `paladin_pat_…` API key can stand ALONE as the
// request's identity, not merely as an additive attribute on top of a JWT. It is used on
// the data plane (paired with auth.InterceptorSkipAPITokens, which lets a PAT bearer past
// the JWT verifier) so a service (e.g. consumer) authenticates document uploads with a
// long-lived service API key. The derived principal is tenant-scoped, Kind=ApiKey, carries
// the token's roles (empty unless a platform admin granted them at creation — migration
// 063 — so role-gated ops stay denied for an ordinary service token), and — opt-in — its own
// resource scopes: an unscoped token gets the default per-tenant baseline (Presign/Put its
// own objects), while a scoped token is further confined to matching resources by the
// scope-enforcement built-in policy. Its Audience is the plane label so a downstream
// RequireAudience passes.
// Additive: it only sets the principal when the context does not already carry one (a JWT
// that already authenticated wins).
func APITokenAuthInterceptor(verifier *api_token.Verifier, limiter ratelimit.Limiter, audience string) connect.Interceptor {
	if verifier == nil {
		return passthroughInterceptor{}
	}
	return &apiTokenInterceptor{verifier: verifier, limiter: limiter, audience: audience, establishPrincipal: true}
}

// APITokenRoleAuthInterceptor establishes the principal ONLY for a token that
// carries roles.
//
// The admin plane is role-gated, so an ordinary service token authenticating
// there would gain nothing and widen the surface for no benefit: every RPC that
// matters would still deny it, and the ones gated on tenant alone would newly
// admit it. A token with roles is different — only a platform admin can mint one
// (granting roles is gated in APITokenService.Create), and it exists precisely
// so a machine can satisfy a role-gated policy without a human session.
//
// A roleless token therefore falls through exactly as before, and the JWT path
// stays the only way to authenticate one on this plane.
func APITokenRoleAuthInterceptor(verifier *api_token.Verifier, limiter ratelimit.Limiter, audience string) connect.Interceptor {
	if verifier == nil {
		return passthroughInterceptor{}
	}

	return &apiTokenInterceptor{
		verifier: verifier, limiter: limiter, audience: audience,
		establishPrincipal: true, requireRolesToEstablish: true,
	}
}

type apiTokenInterceptor struct {
	verifier *api_token.Verifier
	limiter  ratelimit.Limiter
	audience string
	// establishPrincipal makes a verified token also set an auth.Principal on the
	// context (see APITokenAuthInterceptor). Off for the plain additive interceptor.
	establishPrincipal bool
	// requireRolesToEstablish narrows establishment to tokens that carry roles.
	// See APITokenRoleAuthInterceptor.
	requireRolesToEstablish bool
}

// principalFromAPIToken derives the request Principal from a verified API token. Tenant
// binding + Kind=ApiKey + the plane audience, and — opt-in — the token's resource scopes.
//
// Scopes are the OPT-IN half of the model: a token minted WITHOUT scopes keeps the
// tenant-member baseline the default Cedar policy grants (read/write own objects, nothing
// role-gated), exactly as before. A token minted WITH scopes (tenant:/backend:/bucket:/
// collection:/*) is confined by the scope-enforcement built-in policy to matching resources.
//
// Fail-closed on a malformed scope: a scoped token whose scope won't parse must NOT silently
// drop the scope and fall back to unrestricted tenant-wide access. ParseScopes returns an
// error the caller maps to CodeUnauthenticated, so the request is rejected outright.
func principalFromAPIToken(t *api_token.Token, audienceLabel string) (*Principal, error) {
	scopes, err := ParseScopes(t.Scopes)
	if err != nil {
		return nil, fmt.Errorf("api_token: scope: %w", err)
	}
	return &Principal{
		TenantID: t.TenantID,
		Subject:  "apikey:" + t.ID.String(),
		Audience: principalAudienceFor(audienceLabel),
		Kind:     PrincipalKindApiKey,
		Scopes:   scopes,
		// Roles were absent here, so a machine caller could not satisfy any
		// role-gated policy and a consumer needing one had to log in as a
		// human user and manage a session. Tokens carry none unless a
		// platform admin granted them explicitly at creation (the RLS baseline (002_roles_and_rls.sql)).
		Roles: t.Roles,
	}, nil
}

// principalAudienceFor maps a short plane label carried by API tokens ("data"/"admin"/
// "iam"/"mcp") to the canonical RequireAudience value ("paladin-data", …). Unknown labels pass
// through unchanged (RequireAudience then rejects, which is the safe default).
func principalAudienceFor(label string) string {
	switch label {
	case "data":
		return AudienceData
	case "admin":
		return AudienceAdmin
	case "iam":
		return AudienceIAM
	default:
		return label
	}
}

// withTokenIdentity stamps the verified token on the context, and — when this interceptor
// establishes identity — also the derived Principal (unless one is already present).
// Returns an error only when deriving the principal fails (a malformed token scope); the
// caller maps that to CodeUnauthenticated. The additive (non-establishing) path never
// derives a principal, so it never errors here.
func (i *apiTokenInterceptor) withTokenIdentity(ctx context.Context, t *api_token.Token) (context.Context, error) {
	ctx = WithAPIToken(ctx, t)
	if i.establishPrincipal && (!i.requireRolesToEstablish || len(t.Roles) > 0) {
		if _, err := PrincipalFromContext(ctx); err != nil {
			p, perr := principalFromAPIToken(t, i.audience)
			if perr != nil {
				return ctx, perr
			}
			ctx = WithPrincipal(ctx, p)
		}
	}
	return ctx, nil
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
	if err != nil {
		// Fail open: a limiter outage should not deny a request whose token
		// already passed signature, expiry and audience.
		//
		// But record it. Failing open silently means rate limiting can stop
		// existing and nothing says so — the decisions counter just goes
		// quiet, which is indistinguishable from no traffic. This counter is
		// what tells the two apart, and a non-zero rate on it means every
		// token is effectively uncapped.
		recordRateLimitFailOpen(ctx, tok.TenantID.String())
		return nil
	}
	recordRateLimitDecision(ctx, tok.TenantID.String(), d.Allowed, d.WeightedCount, tok.RateLimitRPM)
	if d.Allowed {
		return nil
	}
	ce := connect.NewError(connect.CodeResourceExhausted,
		fmt.Errorf("api_token: rate limit %d rpm exceeded (weighted=%.1f)", tok.RateLimitRPM, d.WeightedCount))
	if d.RetryAfter > 0 {
		ce.Meta().Set("Retry-After", strconv.Itoa(int(d.RetryAfter.Seconds())))
	}
	return ce
}

// authenticate runs the whole API-token gate for one request: extract the
// token, verify it, meter the verify, apply the rate limit, and derive the
// identity. It returns the context the handler should run under, or a
// connect.Error to short-circuit with.
//
// Both wrappers share this instead of each carrying a copy. They carried
// copies, and the copies drifted: only the unary one recorded the verify
// histogram, so `paladin.api_token.verify.duration_ms` quietly meant "unary
// calls only" while its own description claimed end-to-end verify latency —
// invisible in exactly the direction that matters, since a streaming verify
// could be pathological and never appear. Nothing in this sequence is
// call-kind-specific: the verify happens once, at stream open, and is the
// same digest lookup either way. What differs between the two wrappers is
// where the headers come from and how they return, and that is now all that
// differs.
//
// No token is not a failure. The context comes back unchanged with a nil
// error so the request defers to whatever authenticates it downstream.
func (i *apiTokenInterceptor) authenticate(ctx context.Context, hdr http.Header) (context.Context, error) {
	token := extractAPIToken(hdr.Get(HeaderAPIToken), hdr.Get("Authorization"))
	if token == "" {
		return ctx, nil
	}
	start := time.Now()
	t, verr := i.verifier.Verify(ctx, token, i.audience)
	// Tenant attribution: only known on success. On failure the
	// metric is recorded with empty tenant_id; cardinality stays
	// bounded.
	var tenantID string
	if t != nil {
		tenantID = t.TenantID.String()
	}
	recordVerifyDuration(ctx, float64(time.Since(start).Microseconds())/1000.0, tenantID, verr == nil)
	if verr != nil {
		return ctx, mapAPITokenErr(verr)
	}
	if err := i.rateLimitGate(ctx, t); err != nil {
		return ctx, err
	}
	idCtx, err := i.withTokenIdentity(ctx, t)
	if err != nil {
		return ctx, connect.NewError(connect.CodeUnauthenticated, err)
	}
	return idCtx, nil
}

func (i *apiTokenInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		idCtx, err := i.authenticate(ctx, req.Header())
		if err != nil {
			return nil, err
		}
		return next(idCtx, req)
	}
}

func (i *apiTokenInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *apiTokenInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		idCtx, err := i.authenticate(ctx, conn.RequestHeader())
		if err != nil {
			return err
		}
		return next(idCtx, conn)
	}
}

// extractAPIToken pulls the token from either supported header. Returns
// "" when no recognisable API token is present — Bearer values that
// don't start with `paladin_pat_` are skipped so the OIDC JWT verifier can
// process them downstream.
func extractAPIToken(xlegate, authz string) string {
	if t := strings.TrimSpace(xlegate); strings.HasPrefix(t, api_token.TokenPrefix) {
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
// The token's own gates — malformed, unknown, expired, revoked — answer
// "who are you" and map to CodeUnauthenticated; API tokens are an
// *authentication* primitive and the authorisation gates (scopes /
// Cedar) live downstream. Audience is the exception and maps to
// CodePermissionDenied: the caller is identified, the token is simply
// not minted for this plane.
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
