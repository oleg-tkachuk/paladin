package config

import (
	"testing"
)

// With the MCP edge's OAuth on, the resource and its authorization servers
// are https URLs, as RFC 9728 and limes's exchange require of the metadata
// the edge publishes: one that is not stops the boot and names the key.
func TestValidateMCPOAuthIdentifiers(t *testing.T) {
	const (
		resource = "https://mcp.example.com/mcp"
		issuer   = "https://api.example.com"
	)
	for name, tc := range map[string]struct {
		oauth MCPOAuth
		key   string
	}{
		"off, nothing named":           {MCPOAuth{}, ""},
		"https resource and issuer":    {MCPOAuth{Enabled: true, ResourceURL: resource, AuthorizationServers: []string{issuer}}, ""},
		"no resource":                  {MCPOAuth{Enabled: true, AuthorizationServers: []string{issuer}}, mcpOAuthResourceKey},
		"an http resource":             {MCPOAuth{Enabled: true, ResourceURL: "http://localhost:8095/mcp", AuthorizationServers: []string{issuer}}, mcpOAuthResourceKey},
		"no authorization server":      {MCPOAuth{Enabled: true, ResourceURL: resource}, mcpOAuthServersKey},
		"an http authorization server": {MCPOAuth{Enabled: true, ResourceURL: resource, AuthorizationServers: []string{"http://localhost:8085"}}, mcpOAuthServersKey},
	} {
		t.Run(name, func(t *testing.T) {
			c := minimalValidConfig()
			c.MCP.OAuth = tc.oauth
			if tc.key == "" {
				if err := c.Validate(); err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			mustReject(t, c, tc.key)
		})
	}
}
