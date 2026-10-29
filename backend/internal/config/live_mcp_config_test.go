package config

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

// The script that runs an agent_safe MCP bridge beside a deployed release,
// and the flag that makes it print the bridge's config and stop.
const (
	liveMCPScript       = "../../../scripts/live-mcp-delegation.sh"
	liveMCPRenderConfig = "--render-config"
	liveMCPProfile      = "agent_safe"
)

// TestLiveMCPDelegationConfig loads the config scripts/live-mcp-delegation.sh
// renders for its bridge against the live CUE schema, through the script's
// own rendering: a key the strict loader no longer takes fails here, not on
// the cluster where the live check would only say the bridge did not start.
func TestLiveMCPDelegationConfig(t *testing.T) {
	inputs := map[string]string{
		"APP_NAME":            "paladin-core",
		"AUTH_ISSUER":         "paladin-core",
		"MCP_ADDR":            "127.0.0.1:28396",
		"MCP_PROFILE":         liveMCPProfile,
		"UNUSED_DSN":          "postgres://unused@127.0.0.1/unused",
		"OAUTH_ISSUER":        "https://api.paladin.local",
		"OAUTH_AUTHORIZE_URL": "https://api.paladin.local/oauth/authorize",
		"OAUTH_TOKEN_URL":     "https://api.paladin.local/oauth/token",
		"OAUTH_REGISTER_URL":  "https://api.paladin.local/oauth/register",
		"MCP_RESOURCE_URL":    "https://mcp.paladin.local/mcp",
		"EXCHANGE_CLIENT_ID":  "paladin-mcp-server",
		"ADMIN_URL":           "http://127.0.0.1:28390",
		"DATA_URL":            "http://127.0.0.1:28380",
		"IAM_URL":             "http://127.0.0.1:28385",
	}
	cmd := exec.CommandContext(t.Context(), "bash", liveMCPScript, liveMCPRenderConfig)
	cmd.Env = os.Environ()
	for k, v := range inputs {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			t.Fatalf("render: %v\n%s", err, exitErr.Stderr)
		}
		t.Fatalf("render: %v", err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	// The script hands the bridge its two secrets in the environment.
	t.Setenv(EnvPrefix+"AUTH_SIGNING_KEY", "live-mcp-test-signing-key-6c1f0a9e2b7d4c58a3e1")
	t.Setenv(EnvPrefix+"MCP_OAUTH_EXCHANGE_CLIENT_SECRET", "live-mcp-test-exchange-secret-91d2e4f7")
	cfg, err := Load([]string{path}, zap.NewNop())
	if err != nil {
		t.Fatalf("Load the bridge config the script renders: %v", err)
	}

	m := cfg.MCP
	if !m.HTTP.Enabled || m.HTTP.Profile != liveMCPProfile || m.HTTP.Addr != inputs["MCP_ADDR"] {
		t.Errorf("http %+v, want enabled on %s with %s", m.HTTP, inputs["MCP_ADDR"], liveMCPProfile)
	}
	if !m.OAuth.ExchangeEnabled() || m.OAuth.Exchange.ClientID != inputs["EXCHANGE_CLIENT_ID"] {
		t.Errorf("exchange %+v, want it on for %s", m.OAuth.Exchange, inputs["EXCHANGE_CLIENT_ID"])
	}
	if m.OAuth.ResourceURL != inputs["MCP_RESOURCE_URL"] || cfg.Auth.Issuer != inputs["AUTH_ISSUER"] {
		t.Errorf("resource %q, issuer %q; want the release's", m.OAuth.ResourceURL, cfg.Auth.Issuer)
	}
	u := m.Upstreams
	if u.AdminURL != inputs["ADMIN_URL"] || u.DataURL != inputs["DATA_URL"] || u.IAMURL != inputs["IAM_URL"] {
		t.Errorf("upstreams %+v, want the forwarded planes", u)
	}
}

// A value the release's config did not yield stops the render rather than
// leaving an empty key the bridge would start on.
func TestLiveMCPDelegationConfigRefusesAMissingValue(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "bash", liveMCPScript, liveMCPRenderConfig)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	if out, err := cmd.Output(); err == nil {
		t.Errorf("rendered with no inputs:\n%s", out)
	}
}
