package auth

import (
	"context"
	"strings"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/internal/capability"
)

// Header names for capability tokens. Two are accepted:
//
//   - "Authorization: Capability <token>" — normalised RFC 7235-style
//     scheme; tools that already speak Authorization for JWT can switch
//     by changing the scheme keyword.
//
//   - "X-PALADIN-Capability: <token>" — convenience for clients that already
//     use Authorization for an OIDC bearer and need a separate slot.
//
// Either is accepted; if both are present, X-PALADIN-Capability wins because
// the explicit per-product header is the unambiguous signal.
const (
	HeaderCapability       = "X-PALADIN-Capability"
	AuthorizationCapScheme = "capability"
)

// capabilityKey is the context value the interceptor stashes the
// verified *capability.Capability under. Read via CapabilityFromContext.
type capabilityKey struct{}

// CapabilityFromContext returns the verified capability for the request,
// or nil when none was supplied / verified. Handlers gate on caveats
// (op set, resource prefix, budget) by calling this and switching on
// the result.
func CapabilityFromContext(ctx context.Context) (*capability.Capability, bool) {
	c, ok := ctx.Value(capabilityKey{}).(*capability.Capability)
	return c, ok && c != nil
}

// WithCapability stamps a verified capability onto a context. Public so
// tests can prepare contexts without going through the interceptor.
func WithCapability(ctx context.Context, c *capability.Capability) context.Context {
	if c == nil {
		return ctx
	}
	return context.WithValue(ctx, capabilityKey{}, c)
}

// CapabilityInterceptor is an *additive* Connect interceptor — it
// looks for a capability token on the inbound request and, when present
// and valid, stamps the verified *capability.Capability onto the
// request context.
//
// Crucially: a missing or invalid token is a NO-OP. The interceptor
// does not reject; downstream JWT verification still runs and the
// existing auth path is unaffected. Handlers that *require* a
// capability check the context value and return CodePermissionDenied
// when it's absent.
//
// Audience is the plane label this interceptor is mounted on
// ("data" / "admin" / "iam") — the verifier rejects tokens whose
// `aud` claim doesn't include the plane.
//
// When verifier is nil (capability subsystem disabled) the returned
// interceptor is a pass-through that never touches the context.
func CapabilityInterceptor(verifier *capability.StandardVerifier, audience string) connect.Interceptor {
	if verifier == nil {
		return passthroughInterceptor{}
	}
	return &capabilityInterceptor{verifier: verifier, audience: audience}
}

type capabilityInterceptor struct {
	verifier *capability.StandardVerifier
	audience string
}

func (i *capabilityInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		token := extractCapabilityToken(req.Header().Get(HeaderCapability), req.Header().Get("Authorization"))
		if token == "" {
			return next(ctx, req)
		}
		cap, err := i.verifier.Verify(ctx, token, i.audience)
		if err != nil {
			// Token was supplied AND failed verification. We surface
			// this as PermissionDenied — the caller chose to present
			// a capability and it didn't pass; falling through to JWT
			// silently would mask the misconfiguration.
			return nil, connect.NewError(connect.CodePermissionDenied, err)
		}
		return next(WithCapability(ctx, cap), req)
	}
}

func (i *capabilityInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *capabilityInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		token := extractCapabilityToken(conn.RequestHeader().Get(HeaderCapability), conn.RequestHeader().Get("Authorization"))
		if token == "" {
			return next(ctx, conn)
		}
		cap, err := i.verifier.Verify(ctx, token, i.audience)
		if err != nil {
			return connect.NewError(connect.CodePermissionDenied, err)
		}
		return next(WithCapability(ctx, cap), conn)
	}
}

// extractCapabilityToken reads the token from either of the supported
// headers. X-PALADIN-Capability wins over Authorization scheme=capability
// when both are present (explicit > overloaded).
func extractCapabilityToken(xocp, authz string) string {
	if t := strings.TrimSpace(xocp); t != "" {
		return t
	}
	if authz == "" {
		return ""
	}
	parts := strings.SplitN(strings.TrimSpace(authz), " ", 2)
	if len(parts) != 2 {
		return ""
	}
	if !strings.EqualFold(parts[0], AuthorizationCapScheme) {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// passthroughInterceptor is the no-op variant returned when the
// capability subsystem is disabled. Implements connect.Interceptor by
// forwarding every callback unchanged.
type passthroughInterceptor struct{}

func (passthroughInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return next
}
func (passthroughInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}
func (passthroughInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}
