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
	"encoding/json"
	"net/http"
	"strings"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
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

// RequireBearer wraps the MCP handler with an OAuth 2.1 Resource-Server
// challenge: a request without a valid bearer token gets 401 +
// WWW-Authenticate pointing at the protected-resource metadata, so a
// compliant client can begin discovery. A token that is present but fails
// signature/issuer/expiry checks is rejected the same way (error="invalid_token").
//
// resourceMetadataURL is the absolute (or root-relative) URL of the RFC 9728
// document. verifier validates signature + issuer + expiry but NOT audience —
// an agent token targets whichever plane it calls (admin/data/iam), so pinning
// one audience here would wrongly reject valid tokens; the planes still do
// their own per-audience checks downstream.
func RequireBearer(next http.Handler, verifier auth.TokenVerifier, resourceMetadataURL string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := BearerToken(r)
		if tok == "" {
			writeChallenge(w, resourceMetadataURL, "", "")
			return
		}
		if _, err := verifier.Verify(r.Context(), tok); err != nil {
			writeChallenge(w, resourceMetadataURL, "invalid_token", "the access token is missing, expired, or invalid")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireToken is the challenge for deployments that have not enabled the
// OAuth Resource-Server posture. It only checks that *some* credential is
// present — verification stays downstream, where the planes check audience and
// scope — but it makes the refusal honest: without it, a request with no token
// reaches the SDK's session factory, which returns nil and surfaces as
// "400 Bad Request". A missing credential is 401, not a malformed request, and
// clients (and humans reading logs) act on that difference.
//
// No resource_metadata parameter is emitted: with OAuth disabled there is no
// metadata document to point a client at. Use RequireBearer when there is.
func RequireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if BearerToken(r) == "" {
			writeChallenge(w, "", "", "")
			return
		}
		next.ServeHTTP(w, r)
	})
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
