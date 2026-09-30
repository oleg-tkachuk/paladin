// OAuth 2.1 Resource-Server surface for the streamable-HTTP MCP server
// (ADR-0008). This is the RS half only: it makes the MCP server
// *discoverable* by standard MCP clients (Claude Desktop / Cursor) and
// *enforces* a bearer token at the edge. The Authorization Server itself
// (/authorize, /token, dynamic client registration) is a separate, deferred
// phase — here we only advertise where it lives and validate the tokens it
// (or a federated IdP) mints.
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

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	apitoken "github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

// WellKnownProtectedResource is the RFC 9728 path the RS serves its
// protected-resource metadata at, and the URL it points clients to from the
// WWW-Authenticate challenge.
const WellKnownProtectedResource = "/.well-known/oauth-protected-resource"

// WellKnownAuthorizationServer is the RFC 8414 path served only when this
// process is itself the Authorization Server (cfg.MCP.OAuth.AuthorizationServer.Issuer set).
const WellKnownAuthorizationServer = "/.well-known/oauth-authorization-server"

// BearerToken extracts the caller's access token from a request, preferring
// the standard `Authorization: Bearer <token>` header (what OAuth-aware MCP
// clients send) and falling back to the legacy `X-Paladin-Token` header so
// existing bridge deployments keep working. Returns "" when neither is set.
func BearerToken(r *http.Request) string {
	if authz := r.Header.Get("Authorization"); authz != "" {
		const prefix = "Bearer "
		if len(authz) > len(prefix) && strings.EqualFold(authz[:len(prefix)], prefix) {
			return strings.TrimSpace(authz[len(prefix):])
		}
	}
	return r.Header.Get("X-Paladin-Token")
}

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

// authorizationServerMetadata is the subset of RFC 8414 fields Paladin advertises
// when it is itself the AS. Endpoint fields are omitted when unset so we never
// publish a URL that 404s.
type authorizationServerMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint,omitempty"`
	TokenEndpoint                     string   `json:"token_endpoint,omitempty"`
	RegistrationEndpoint              string   `json:"registration_endpoint,omitempty"`
	JWKSURI                           string   `json:"jwks_uri,omitempty"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
}

// AuthorizationServerMetadataHandler serves RFC 8414 metadata describing the
// Paladin-as-AS endpoints. Only mounted when cfg.AuthorizationServer.Issuer is
// set (the same-origin AS case); when delegating to an external IdP the
// client fetches that IdP's own metadata instead. PKCE S256 is mandatory and
// auth-code + refresh are the advertised grants — the contract the deferred
// AS phase implements.
func AuthorizationServerMetadataHandler(as config.MCPOAuthAS) http.Handler {
	doc := authorizationServerMetadata{
		Issuer:                            as.Issuer,
		AuthorizationEndpoint:             as.AuthorizationEndpoint,
		TokenEndpoint:                     as.TokenEndpoint,
		RegistrationEndpoint:              as.RegistrationEndpoint,
		JWKSURI:                           as.JWKSURI,
		ResponseTypesSupported:            []string{"code"},
		GrantTypesSupported:               []string{"authorization_code", "refresh_token"},
		CodeChallengeMethodsSupported:     []string{"S256"},
		TokenEndpointAuthMethodsSupported: []string{"none", "client_secret_basic"},
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
func RequireBearer(next http.Handler, verifier auth.TokenVerifier, resourceMetadataURL string) http.Handler {
	bound := bindSession(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := BearerToken(r)
		if tok == "" {
			writeChallenge(w, resourceMetadataURL, "", "")
			return
		}
		info := &mcpauth.TokenInfo{UserID: credentialIdentity(tok)}
		if !strings.HasPrefix(tok, apitoken.TokenPrefix) {
			p, err := verifier.Verify(r.Context(), tok)
			if err != nil {
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
	w.WriteHeader(http.StatusUnauthorized)
	payload := map[string]string{"error": "unauthorized"}
	if errCode != "" {
		payload["error"] = errCode
		payload["error_description"] = errDesc
	}
	_ = json.NewEncoder(w).Encode(payload)
}
