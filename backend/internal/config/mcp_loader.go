package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	goyaml "gopkg.in/yaml.v3"
)

// MCPDefaults returns an MCP config populated with the same defaults the CUE
// schema applies to the main service. Used by the MCP binaries when no YAML
// config is supplied — in that mode env vars are the only knobs.
func MCPDefaults() MCP {
	return MCP{
		Upstreams: MCPUpstreams{
			AdminURL: "http://localhost:8090",
			DataURL:  "http://localhost:8080",
			IAMURL:   "http://localhost:8085",
		},
		Stdio: MCPStdio{Enabled: true, Profile: "read_only"},
		HTTP:  MCPHTTP{Enabled: true, Addr: ":8095", Profile: "read_only", SessionTimeout: 10 * time.Minute},
	}
}

// LoadMCP parses just the `mcp:` section of a YAML config file, applies
// defaults for unset fields, and overlays environment-variable overrides.
//
// Path may be empty: the binaries are designed to run on a laptop with no
// YAML at all, just env vars (PALADIN_ADMIN_URL, PALADIN_MCP_TOKEN, etc.). When
// path is set but the file is unreadable, an error is returned so the
// operator catches deploy-time misconfiguration instead of silently
// falling back to defaults that hide the problem.
//
// Env overrides (always win over YAML so a Claude Desktop config can pin
// behaviour without rewriting the cluster YAML):
//
//	PALADIN_ADMIN_URL, PALADIN_DATA_URL, PALADIN_IAM_URL — upstream plane URLs
//	PALADIN_MCP_STDIO_ENABLED                    — true|false (default true)
//	PALADIN_MCP_STDIO_PROFILE                    — read_only | agent_safe | admin
//	PALADIN_MCP_HTTP_ENABLED                     — true|false (default true)
//	PALADIN_MCP_HTTP_ADDR                        — listen address, default :8095
//	PALADIN_MCP_HTTP_PROFILE                     — read_only | agent_safe | admin
//	PALADIN_MCP_HTTP_SESSION_TIMEOUT             — Go duration, default 10m
func LoadMCP(path string) (MCP, error) {
	cfg := MCPDefaults()

	if path != "" {
		raw, err := os.ReadFile(path) // #nosec G304 — operator-supplied path
		if err != nil {
			return MCP{}, fmt.Errorf("read mcp config %q: %w", path, err)
		}
		var wrap struct {
			MCP MCP `yaml:"mcp"`
		}
		// Pre-fill wrap.MCP with defaults so partial YAML overrides only
		// the fields the operator actually set.
		wrap.MCP = cfg
		if err := goyaml.Unmarshal(raw, &wrap); err != nil {
			return MCP{}, fmt.Errorf("parse mcp config %q: %w", path, err)
		}
		cfg = wrap.MCP
	}

	if v := os.Getenv("PALADIN_ADMIN_URL"); v != "" {
		cfg.Upstreams.AdminURL = v
	}
	if v := os.Getenv("PALADIN_DATA_URL"); v != "" {
		cfg.Upstreams.DataURL = v
	}
	if v := os.Getenv("PALADIN_IAM_URL"); v != "" {
		cfg.Upstreams.IAMURL = v
	}
	if v, ok := envBool("PALADIN_MCP_STDIO_ENABLED"); ok {
		cfg.Stdio.Enabled = v
	}
	if v := os.Getenv("PALADIN_MCP_STDIO_PROFILE"); v != "" {
		cfg.Stdio.Profile = v
	}
	if v, ok := envBool("PALADIN_MCP_HTTP_ENABLED"); ok {
		cfg.HTTP.Enabled = v
	}
	if v := os.Getenv("PALADIN_MCP_HTTP_ADDR"); v != "" {
		cfg.HTTP.Addr = v
	}
	if v := os.Getenv("PALADIN_MCP_HTTP_PROFILE"); v != "" {
		cfg.HTTP.Profile = v
	}
	if v := os.Getenv("PALADIN_MCP_HTTP_SESSIONS_URL"); v != "" {
		cfg.HTTP.SessionsURL = v
	}
	if v := os.Getenv("PALADIN_MCP_HTTP_SESSION_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return MCP{}, fmt.Errorf("PALADIN_MCP_HTTP_SESSION_TIMEOUT %q: %w", v, err)
		}
		cfg.HTTP.SessionTimeout = d
	}
	if v := os.Getenv("PALADIN_MCP_UPSTREAM_CA"); v != "" {
		cfg.Upstreams.TLS.CaPath = v
	}
	if v := os.Getenv("PALADIN_MCP_UPSTREAM_CERT"); v != "" {
		cfg.Upstreams.TLS.CertPath = v
	}
	if v := os.Getenv("PALADIN_MCP_UPSTREAM_KEY"); v != "" {
		cfg.Upstreams.TLS.KeyPath = v
	}

	for _, up := range []struct{ name, url string }{
		{"admin_url", cfg.Upstreams.AdminURL},
		{"data_url", cfg.Upstreams.DataURL},
		{"iam_url", cfg.Upstreams.IAMURL},
	} {
		if up.url == "" {
			return MCP{}, fmt.Errorf("mcp.upstreams.%s is required", up.name)
		}
	}
	if err := cfg.Upstreams.Validate(); err != nil {
		return MCP{}, err
	}
	return cfg, nil
}

// Validate rejects upstream settings that would leave the bridge unable to
// reach the planes. The failure this guards against is silent at startup and
// only shows up as an opaque error on every tools/call, so it is worth
// catching at load time.
func (u MCPUpstreams) Validate() error {
	anyTLS := false
	for _, up := range []struct{ name, url string }{
		{"admin_url", u.AdminURL},
		{"data_url", u.DataURL},
		{"iam_url", u.IAMURL},
	} {
		if up.url == "" {
			// Not an error here: Config.Validate runs for every role, and
			// roles that never build a bridge legitimately carry no mcp
			// section. LoadMCP — the bridge's own path — requires them.
			continue
		}
		// Scheme only, by prefix rather than url.Parse: these values reach
		// Validate straight out of Helm values with the host still an
		// unrendered {{ template }}, which is not a parseable URL but is a
		// perfectly good thing to scheme-check.
		scheme, _, ok := strings.Cut(up.url, "://")
		if !ok {
			return fmt.Errorf("mcp.upstreams.%s %q: missing scheme", up.name, up.url)
		}
		switch scheme {
		case "http":
		case "https":
			anyTLS = true
		default:
			return fmt.Errorf("mcp.upstreams.%s %q: scheme must be http or https", up.name, up.url)
		}
	}
	if anyTLS && u.TLS.CaPath == "" && !u.TLS.InsecureSkipVerify {
		return fmt.Errorf(
			"mcp.upstreams: an https:// upstream requires tls.ca_path (the internal " +
				"mTLS CA bundle the planes serve from; the system roots do not " +
				"contain it) unless tls.insecure_skip_verify is set")
	}
	if (u.TLS.CertPath == "") != (u.TLS.KeyPath == "") {
		return fmt.Errorf("mcp.upstreams.tls: cert_path and key_path must be set together")
	}
	return nil
}

func envBool(key string) (bool, bool) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return false, false
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, false
	}
	return b, true
}
