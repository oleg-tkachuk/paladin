// OAuth 2.1 Resource-Server surface for the streamable-HTTP MCP server
// (ADR-0008). This is the RS half only: it makes the MCP server
// *discoverable* by standard MCP clients (desktop agents, IDE plugins) and
// *enforces* a bearer token at the edge. The Authorization Server itself
// (/authorize, /token, dynamic client registration) lives in
// internal/auth/oauth (ADR-0009) — here we only advertise where it lives and
// validate the tokens it (or a federated IdP) mints.
//
// Flow a compliant client runs against this:
//
//  1. POST /mcp without a token        → 401 + WWW-Authenticate: Bearer
//     resource_metadata="…/.well-known/oauth-protected-resource"
//  2. GET that metadata (RFC 9728)     → learns the authorization_servers
//  3. GET <AS>/.well-known/oauth-authorization-server (RFC 8414) → endpoints
//  4. … OAuth auth-code + PKCE at the AS … → access_token
//  5. POST /mcp  Authorization: Bearer <token> → validated here, session opens
package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"go.uber.org/zap"

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	apitoken "github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

// WellKnownProtectedResource is the RFC 9728 path the RS serves its
// protected-resource metadata at, and the URL it points clients to from the
// WWW-Authenticate challenge.
const WellKnownProtectedResource = "/.well-known/oauth-protected-resource"

// BearerToken extracts the caller's access token from a request, preferring
// the standard `Authorization: Bearer <token>` header (what OAuth-aware MCP
// clients send) and falling back to the legacy `X-Paladin-Token` header so
// existing bridge deployments keep working. Returns "" when neither is set.
func BearerToken(r *http.Request) string { return headerToken(r.Header) }

func headerToken(h http.Header) string {
	if authz := h.Get("Authorization"); authz != "" {
		const prefix = "Bearer "
		if len(authz) > len(prefix) && strings.EqualFold(authz[:len(prefix)], prefix) {
			return strings.TrimSpace(authz[len(prefix):])
		}
	}
	return h.Get(legacyTokenHeader)
}

// legacyTokenHeader is where a bridge deployment that predates OAuth sends
// its token.
const legacyTokenHeader = "X-Paladin-Token"

// protectedResourceMetadata is the RFC 9728 document.
type protectedResourceMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers,omitempty"`
	ScopesSupported        []string `json:"scopes_supported,omitempty"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
}

// ProtectedResourceMetadataHandler serves the RFC 9728 metadata that tells a
// client which Authorization Server(s) issue tokens for this MCP resource.
func ProtectedResourceMetadataHandler(cfg config.MCPOAuth) http.Handler {
	doc := protectedResourceMetadata{
		Resource:               cfg.ResourceURL,
		AuthorizationServers:   cfg.AuthorizationServers,
		ScopesSupported:        cfg.ScopesSupported,
		BearerMethodsSupported: []string{"header"},
	}
	body, _ := json.MarshalIndent(doc, "", "  ")
	return jsonDocHandler(body)
}

func jsonDocHandler(body []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
}

// RequireBearer authenticates every request at the MCP edge and hands the
// SDK the caller's identity, so a session is bound to the principal that
// opened it. A request without a credential, or with one that fails
// signature, issuer or expiry, gets 401 + WWW-Authenticate (error=
// "invalid_token" for the second); resourceMetadataURL, when set, points an
// OAuth client at the RFC 9728 document, and is empty when OAuth is off.
//
// verifier checks signature + issuer + expiry but NOT audience — an agent
// token targets whichever plane a tool calls, and the planes check their own
// audience downstream. A Paladin API token (paladin_pat_…) is not a JWT and
// is verified by the planes against their store, which this edge does not
// hold; it passes here, identified by its hash.
//
// With OnlyMCPAudience the edge is the one ADR-0036 describes: verifier
// accepts paladin-mcp alone, and an API token — a plane credential — is
// refused rather than forwarded.
func RequireBearer(next http.Handler, verifier auth.TokenVerifier, resourceMetadataURL string, opts ...EdgeOption) http.Handler {
	var o edgeOptions
	for _, opt := range opts {
		opt(&o)
	}
	if o.delegation != nil {
		// Inside the session binding, which reads the token from
		// Authorization before it is replaced.
		next = delegateCalls(next, o.delegation, resourceMetadataURL)
	}
	bound := bindSession(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := BearerToken(r)
		if tok == "" {
			writeChallenge(w, resourceMetadataURL, "", "")
			return
		}
		info := &mcpauth.TokenInfo{UserID: credentialIdentity(tok)}
		isAPIToken := strings.HasPrefix(tok, apitoken.TokenPrefix)
		if isAPIToken && o.onlyMCPAudience {
			writeChallenge(w, resourceMetadataURL, "invalid_token", "an API token is a plane's credential; authorize for "+auth.AudienceMCP)
			return
		}
		if !isAPIToken {
			p, err := verifier.Verify(r.Context(), tok)
			if err != nil {
				// The agent is told only that the token was refused; why —
				// expired, another audience, a key set that would not load —
				// is here. The token itself is never logged.
				logger.FromContext(r.Context()).Info(tokenRefusedMessage, zap.Error(err))
				writeChallenge(w, resourceMetadataURL, "invalid_token", "the access token is missing, expired, or invalid")
				return
			}
			info = &mcpauth.TokenInfo{
				UserID:     principalIdentity(p),
				Expiration: p.ExpiresAt,
				Extra:      map[string]any{TokenInfoAudiences: p.Audiences},
			}
		}
		bound.ServeHTTP(w, withTokenInfo(r, tok, info))
	})
}

// tokenRefusedMessage is the log line of a bearer token the edge refused.
const tokenRefusedMessage = "mcp access token refused"

// EdgeOption tunes RequireBearer.
type EdgeOption func(*edgeOptions)

type edgeOptions struct {
	onlyMCPAudience bool
	delegation      capabilitySource
}

// WithDelegation exchanges each caller's token for the capability that acts
// for its user (ADR-0041), and sends the bridge's calls with that capability
// alone.
func WithDelegation(d *DelegationClient) EdgeOption {
	return func(o *edgeOptions) { o.delegation = d }
}

// OnlyMCPAudience refuses every credential but a paladin-mcp token; the
// verifier given RequireBearer must check that audience (ADR-0036).
func OnlyMCPAudience() EdgeOption {
	return func(o *edgeOptions) { o.onlyMCPAudience = true }
}

// RequireToken is the edge for a deployment with no way to verify a token —
// no signing key and no JWKS. It can only check that a credential is present;
// the planes verify it. It still binds each session to that credential, so a
// session id cannot be driven with another token.
//
// No resource_metadata parameter is emitted: with OAuth disabled there is no
// metadata document to point a client at.
func RequireToken(next http.Handler) http.Handler {
	bound := bindSession(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := BearerToken(r)
		if tok == "" {
			writeChallenge(w, "", "", "")
			return
		}
		bound.ServeHTTP(w, withTokenInfo(r, tok, &mcpauth.TokenInfo{UserID: credentialIdentity(tok)}))
	})
}

// TokenInfoAudiences is the TokenInfo.Extra key holding the token's `aud`
// values, for handlers that explain which planes a session can reach.
const TokenInfoAudiences = "audiences"

// principalIdentity names a verified principal for session binding: the
// tenant and the subject, since a subject is unique only within its tenant.
func principalIdentity(p *auth.Principal) string {
	return "jwt:" + p.TenantID.String() + "/" + p.Subject
}

// credentialIdentity names a credential the edge cannot open. Only a digest
// is kept: the identity sits in the SDK's session table.
func credentialIdentity(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return "token:" + hex.EncodeToString(sum[:])
}

type tokenInfoKey struct{}

// withTokenInfo stashes the identity for bindSession and puts the credential
// in the Authorization header, which is the only place the SDK reads it from
// — a legacy X-Paladin-Token request would otherwise be refused there.
func withTokenInfo(r *http.Request, tok string, info *mcpauth.TokenInfo) *http.Request {
	r = r.Clone(context.WithValue(r.Context(), tokenInfoKey{}, info))
	r.Header.Set("Authorization", "Bearer "+tok)
	return r
}

// bindSession runs the SDK's bearer middleware with the identity this edge
// already established. The SDK does the binding itself: a session remembers
// the TokenInfo.UserID that created it and refuses requests carrying another.
func bindSession(next http.Handler) http.Handler {
	return mcpauth.RequireBearerToken(
		func(ctx context.Context, _ string, _ *http.Request) (*mcpauth.TokenInfo, error) {
			info, _ := ctx.Value(tokenInfoKey{}).(*mcpauth.TokenInfo)
			if info == nil {
				return nil, mcpauth.ErrInvalidToken
			}
			return info, nil
		},
		// Expiry was checked by the verifier with its leeway; an API token and
		// an unverifiable credential carry none to check.
		&mcpauth.RequireBearerTokenOptions{AllowMissingExpiration: true},
	)(next)
}

// writeChallenge emits the 401 + WWW-Authenticate per RFC 9728 §5.1 (the
// resource_metadata parameter) and RFC 6750 (error / error_description).
// An empty resourceMetadataURL omits that parameter entirely — an empty
// resource_metadata="" would be a promise of a document that isn't served.
func writeChallenge(w http.ResponseWriter, resourceMetadataURL, errCode, errDesc string) {
	writeChallengeStatus(w, http.StatusUnauthorized, resourceMetadataURL, errCode, errDesc)
}

// writeChallengeStatus is writeChallenge with another status: 403 for
// insufficient_scope (RFC 6750 §3.1).
func writeChallengeStatus(w http.ResponseWriter, status int, resourceMetadataURL, errCode, errDesc string) {
	var b strings.Builder
	b.WriteString("Bearer")
	if resourceMetadataURL != "" {
		b.WriteString(` resource_metadata="`)
		b.WriteString(resourceMetadataURL)
		b.WriteString(`"`)
	}
	if errCode != "" {
		if resourceMetadataURL != "" {
			b.WriteString(",")
		}
		b.WriteString(` error="`)
		b.WriteString(errCode)
		b.WriteString(`", error_description="`)
		b.WriteString(errDesc)
		b.WriteString(`"`)
	}
	w.Header().Set("WWW-Authenticate", b.String())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	payload := map[string]string{"error": "unauthorized"}
	if errCode != "" {
		payload["error"] = errCode
		payload["error_description"] = errDesc
	}
	_ = json.NewEncoder(w).Encode(payload)
}
