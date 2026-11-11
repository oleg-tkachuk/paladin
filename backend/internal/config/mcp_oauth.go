package config

import (
	"fmt"

	"github.com/oleg-tkachuk/limes/exchange"
)

// The keys a refusal of the MCP edge's OAuth identifiers names.
const (
	mcpOAuthResourceKey = "mcp.oauth.resource_url"
	mcpOAuthServersKey  = "mcp.oauth.authorization_servers"
)

// validateMCPOAuth refuses, with the MCP edge's OAuth on, a resource or an
// authorization server that is not an https URL, and no authorization
// server at all: the protected-resource metadata the edge publishes holds
// both (RFC 9728 §2), and limes's exchange, which serves it, refuses either.
// The check is limes's own, so the boot stops here rather than in the
// handler. Each is an identifier, not an address the edge listens on: the
// listener may be plain http behind a proxy that holds the certificate.
func (o MCPOAuth) validate() error {
	if !o.Enabled {
		return nil
	}
	if _, err := exchange.ResourceMetadataURL(o.ResourceURL); err != nil {
		return fmt.Errorf("%s %q: %w", mcpOAuthResourceKey, o.ResourceURL, err)
	}
	if len(o.AuthorizationServers) == 0 {
		return fmt.Errorf("%s: at least one is required while mcp.oauth.enabled is true", mcpOAuthServersKey)
	}
	for _, issuer := range o.AuthorizationServers {
		if _, err := exchange.AuthorizationServerMetadataURL(issuer); err != nil {
			return fmt.Errorf("%s %q: %w", mcpOAuthServersKey, issuer, err)
		}
	}
	return nil
}
