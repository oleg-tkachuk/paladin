package config

import (
	"fmt"
	"strings"
)

// The MCP bridge's configuration lives in the main config loader, reached
// through cfg.MCP.Upstreams (see cmd/server/serve_mcp.go). An MCPDefaults /
// LoadMCP pair used to sit here, documented as the path "the MCP binaries"
// took "when no YAML config is supplied". Nothing called either one: every
// caller had moved to the CUE-validated loader, so the pair described a
// configuration route that did not exist, tested itself into looking alive,
// and defaulted the iam upstream to http://localhost:8085 — which made it the
// standing suspect for the api pod's plain-HTTP-to-a-TLS-port bursts. It was
// never running, so it was never the client. Removed rather than corrected:
// the env-var names it read (PALADIN_IAM_URL and friends) belong to the
// console BFF, which does read them, and two consumers of one name where only
// one is live is how the confusion started.

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
			// section. `serve mcp` fails on its own if they are missing,
			// which is where the requirement belongs.
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
