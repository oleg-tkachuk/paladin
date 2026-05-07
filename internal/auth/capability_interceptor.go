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

// AssertCapabilityOp is the handler-side gate. Call early in any
// handler that wants to honour capability caveats:
//
//	if err := auth.AssertCapabilityOp(ctx, capability.OpGet, key); err != nil {
//	    return nil, err
//	}
//
// Behaviour:
//
//   - No capability on context (caller used JWT auth) → returns nil.
//     Handler proceeds as before; existing role / Cedar gates still run.
//
//   - Capability present and op + resource allowed → returns nil.
//     Handler proceeds; downstream code may also call CapabilityFromContext
//     to read budget / source-IP / other caveats.
//
//   - Capability present but op not in Caveats.Ops, or resource not
//     under any of Caveats.ResourcePrefixes / ResourceURIs → returns a
//     CodePermissionDenied connect.Error so the caller sees an
//     unambiguous "your capability didn't allow this" instead of
//     falling through to a more generic 403.
//
// resourceURI is matched as a string against ResourcePrefixes (HasPrefix)
// and ResourceURIs (exact). Empty resourceURI skips the resource check —
// useful for ops that don't target a specific URI (e.g. capability self-
// introspect).
func AssertCapabilityOp(ctx context.Context, op capability.Op, resourceURI string) error {
	cap, ok := CapabilityFromContext(ctx)
	if !ok {
		return nil // no capability presented; not our gate
	}
	if !containsOp(cap.Caveats.Ops, op) {
		return connect.NewError(connect.CodePermissionDenied,
			capabilityOpNotAllowed{op: op, allowed: cap.Caveats.Ops})
	}
	if resourceURI != "" && !resourceAllowed(cap.Caveats, resourceURI) {
		return connect.NewError(connect.CodePermissionDenied,
			capabilityResourceNotAllowed{uri: resourceURI})
	}
	return nil
}

// containsOp checks Op set membership without dragging slices.Contains
// into every call site.
func containsOp(set []capability.Op, want capability.Op) bool {
	for _, op := range set {
		if op == want {
			return true
		}
	}
	return false
}

// resourceAllowed reports whether the supplied URI is reachable under
// the capability's resource caveats. Empty caveats = unrestricted within
// tenant scope (the verifier already enforced that).
func resourceAllowed(c capability.Caveats, uri string) bool {
	if len(c.ResourcePrefixes) == 0 && len(c.ResourceURIs) == 0 {
		return true
	}
	for _, exact := range c.ResourceURIs {
		if exact == uri {
			return true
		}
	}
	for _, prefix := range c.ResourcePrefixes {
		if len(prefix) > 0 && len(uri) >= len(prefix) && uri[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// capabilityOpNotAllowed and capabilityResourceNotAllowed are typed
// errors so callers / tests can branch on the rejection reason without
// string-matching connect error messages.
type capabilityOpNotAllowed struct {
	op      capability.Op
	allowed []capability.Op
}

func (e capabilityOpNotAllowed) Error() string {
	return "capability op " + string(e.op) + " not in allowed set"
}

type capabilityResourceNotAllowed struct {
	uri string
}

func (e capabilityResourceNotAllowed) Error() string {
	return "capability does not authorise resource " + e.uri
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
