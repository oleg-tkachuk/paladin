package oauth

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

// WellKnownAuthorizationServer is the RFC 8414 path of this Authorization
// Server's metadata.
const WellKnownAuthorizationServer = "/.well-known/oauth-authorization-server"

// metadataMaxAge is how long a client may cache the metadata document.
const metadataMaxAge = "public, max-age=300"

// responseTypeCode is the one response type: the authorization code flow.
const responseTypeCode = "code"

// The RFC 8414 values this server supports: the code flow only, PKCE S256
// only, a public client or a confidential one over HTTP Basic or the form.
var (
	responseTypesSupported            = []string{responseTypeCode}
	codeChallengeMethodsSupported     = []string{PKCEMethodS256}
	tokenEndpointAuthMethodsSupported = []string{"none", "client_secret_basic", "client_secret_post"}
)

// authorizationServerMetadata is the RFC 8414 document. Endpoint fields are
// omitted when unset, so no URL that 404s is published.
type authorizationServerMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint,omitempty"`
	TokenEndpoint                     string   `json:"token_endpoint,omitempty"`
	RegistrationEndpoint              string   `json:"registration_endpoint,omitempty"`
	RevocationEndpoint                string   `json:"revocation_endpoint,omitempty"`
	JWKSURI                           string   `json:"jwks_uri,omitempty"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	// The revocation endpoint authenticates a client as the token endpoint does.
	RevocationEndpointAuthMethodsSupported []string `json:"revocation_endpoint_auth_methods_supported,omitempty"`
}

// revocationEndpoint is where /oauth/revoke is reached: beside the token
// endpoint, which Mount serves on the same mux. A token endpoint configured
// at another path names no revocation endpoint rather than a guessed one.
func revocationEndpoint(tokenEndpoint string) string {
	base, ok := strings.CutSuffix(tokenEndpoint, TokenPath)
	if !ok {
		return ""
	}
	return base + RevokePath
}

// MetadataHandler serves the RFC 8414 metadata of this Authorization Server,
// at its own origin and, for a same-origin deployment, at the MCP server's.
// tokenExchange adds the RFC 8693 grant, which only a seed client marked
// token_exchange can use.
func MetadataHandler(as config.MCPOAuthAS, tokenExchange bool) http.Handler {
	grants := []string{GrantTypeAuthorizationCode, GrantTypeRefreshToken}
	if tokenExchange {
		grants = append(grants, GrantTypeTokenExchange)
	}
	revoke := revocationEndpoint(as.TokenEndpoint)
	var revokeAuth []string
	if revoke != "" {
		revokeAuth = tokenEndpointAuthMethodsSupported
	}
	body, _ := json.MarshalIndent(authorizationServerMetadata{
		Issuer:                                 as.Issuer,
		AuthorizationEndpoint:                  as.AuthorizationEndpoint,
		TokenEndpoint:                          as.TokenEndpoint,
		RegistrationEndpoint:                   as.RegistrationEndpoint,
		RevocationEndpoint:                     revoke,
		JWKSURI:                                as.JWKSURI,
		ResponseTypesSupported:                 responseTypesSupported,
		GrantTypesSupported:                    grants,
		CodeChallengeMethodsSupported:          codeChallengeMethodsSupported,
		TokenEndpointAuthMethodsSupported:      tokenEndpointAuthMethodsSupported,
		RevocationEndpointAuthMethodsSupported: revokeAuth,
	}, "", "  ")
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", metadataMaxAge)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
}
