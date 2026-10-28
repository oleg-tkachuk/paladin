package main

import (
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/oauth"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/mcp"
)

func TestExchangeTokenURL(t *testing.T) {
	const iam = "https://paladin-core-api:8085"
	for _, tc := range []struct {
		name   string
		ex     config.MCPOAuthExchange
		iamURL string
		want   string
	}{
		{"the IAM upstream's", config.MCPOAuthExchange{}, iam, iam + oauth.TokenPath},
		{"a trailing slash is not doubled", config.MCPOAuthExchange{}, iam + "/", iam + oauth.TokenPath},
		{"a configured URL wins", config.MCPOAuthExchange{TokenURL: "https://as.example/token"}, iam, "https://as.example/token"},
	} {
		if got := exchangeTokenURL(tc.ex, tc.iamURL); got != tc.want {
			t.Errorf("%s: exchangeTokenURL = %q, want %q", tc.name, got, tc.want)
		}
	}
	// In process the configured URL is dropped: only the inline IAM is there.
	cfg := config.MCP{OAuth: config.MCPOAuth{Exchange: config.MCPOAuthExchange{TokenURL: "https://as.example/token"}}}
	if got := exchangeTokenURL(inlineExchange(cfg).OAuth.Exchange, mcp.InlineIAMURL); got != mcp.InlineIAMURL+oauth.TokenPath {
		t.Errorf("embedded: exchangeTokenURL = %q, want the inline IAM's", got)
	}
}

// The edge exchanges only when mcp.oauth.exchange names a client.
func TestDelegationClient(t *testing.T) {
	if got := delegationClient(config.MCP{}, nil, mcp.InlineIAMURL); got != nil {
		t.Error("exchange off: a delegation client was built")
	}
	on := config.MCP{OAuth: config.MCPOAuth{Enabled: true, Exchange: config.MCPOAuthExchange{ClientID: "mcp", ClientSecret: "s"}}}
	if got := delegationClient(on, nil, mcp.InlineIAMURL); got == nil {
		t.Error("exchange on: no delegation client")
	}
}
