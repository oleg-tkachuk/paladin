package oauth

import (
	"encoding/json"
	"net/http"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

// WellKnownAuthorizationServer is the RFC 8414 path of this Authorization
// Server's metadata.
const WellKnownAuthorizationServer = "/.well-known/oauth-authorization-server"

// metadataMaxAge is how long a client may cache the metadata document.
const metadataMaxAge = "public, max-age=300"

// The RFC 8414 values this server supports: the code flow only, PKCE S256
// only, a public client or a confidential one over HTTP Basic or the form.
var (
	responseTypesSupported            = []string{"code"}
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
	JWKSURI                           string   `json:"jwks_uri,omitempty"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
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
	body, _ := json.MarshalIndent(authorizationServerMetadata{
		Issuer:                            as.Issuer,
		AuthorizationEndpoint:             as.AuthorizationEndpoint,
		TokenEndpoint:                     as.TokenEndpoint,
		RegistrationEndpoint:              as.RegistrationEndpoint,
		JWKSURI:                           as.JWKSURI,
		ResponseTypesSupported:            responseTypesSupported,
		GrantTypesSupported:               grants,
		CodeChallengeMethodsSupported:     codeChallengeMethodsSupported,
		TokenEndpointAuthMethodsSupported: tokenEndpointAuthMethodsSupported,
	}, "", "  ")
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", metadataMaxAge)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
}
