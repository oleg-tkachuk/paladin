package config

import (
	"strings"
	"testing"
)

// TestMCPUpstreamsValidate guards the misconfiguration that silently broke the
// deployed bridge: plaintext URLs pointed at planes that serve TLS. Nothing
// fails at startup in that state — it surfaces only as "internal: 400 Bad
// Request" on every tools/call, because a Go TLS listener answers a plaintext
// request with exactly that. The inverse (https:// with no CA) is just as
// quiet: the planes' certificates chain to the internal mTLS CA, which the
// system roots do not contain, so verification fails on first use.
func TestMCPUpstreamsValidate(t *testing.T) {
	t.Parallel()

	const ca = "/etc/paladin-mtls/ca.crt"

	tests := []struct {
		name    string
		in      MCPUpstreams
		wantErr string
	}{
		{
			name: "all plaintext needs no trust material",
			in: MCPUpstreams{
				AdminURL: "http://admin:8090", DataURL: "http://api:8080", IAMURL: "http://api:8085",
			},
		},
		{
			name: "all TLS with a CA",
			in: MCPUpstreams{
				AdminURL: "https://admin:8090", DataURL: "https://api:8080", IAMURL: "https://api:8085",
				TLS: MCPUpstreamTLS{CaPath: ca},
			},
		},
		{
			name: "mixed schemes are allowed — the scheme is per-upstream",
			in: MCPUpstreams{
				AdminURL: "https://admin:8090", DataURL: "http://api:8080", IAMURL: "http://api:8085",
				TLS: MCPUpstreamTLS{CaPath: ca},
			},
		},
		{
			name: "client keypair alongside the CA",
			in: MCPUpstreams{
				AdminURL: "https://admin:8090", DataURL: "https://api:8080", IAMURL: "https://api:8085",
				TLS: MCPUpstreamTLS{CaPath: ca, CertPath: "/c.crt", KeyPath: "/c.key"},
			},
		},
		{
			name: "insecure_skip_verify substitutes for a CA",
			in: MCPUpstreams{
				AdminURL: "https://admin:8090", DataURL: "https://api:8080", IAMURL: "https://api:8085",
				TLS: MCPUpstreamTLS{InsecureSkipVerify: true},
			},
		},
		{
			name: "empty URLs are skipped — roles that build no bridge carry no mcp section",
			in:   MCPUpstreams{},
		},
		{
			name: "unrendered helm template is scheme-checked, not URL-parsed",
			in: MCPUpstreams{
				AdminURL: `https://{{ include "chart.fullname" . }}-admin:8090`,
				DataURL:  `https://{{ include "chart.fullname" . }}-api:8080`,
				IAMURL:   `https://{{ include "chart.fullname" . }}-api:8085`,
				TLS:      MCPUpstreamTLS{CaPath: ca},
			},
		},
		{
			name: "https without trust material",
			in: MCPUpstreams{
				AdminURL: "https://admin:8090", DataURL: "https://api:8080", IAMURL: "https://api:8085",
			},
			wantErr: "requires tls.ca_path",
		},
		{
			name: "a single https upstream is enough to require a CA",
			in: MCPUpstreams{
				AdminURL: "https://admin:8090", DataURL: "http://api:8080", IAMURL: "http://api:8085",
			},
			wantErr: "requires tls.ca_path",
		},
		{
			name: "missing scheme",
			in: MCPUpstreams{
				AdminURL: "admin:8090", DataURL: "http://api:8080", IAMURL: "http://api:8085",
			},
			wantErr: "missing scheme",
		},
		{
			name: "unsupported scheme",
			in: MCPUpstreams{
				AdminURL: "grpc://admin:8090", DataURL: "http://api:8080", IAMURL: "http://api:8085",
			},
			wantErr: "scheme must be http or https",
		},
		{
			name: "cert without key",
			in: MCPUpstreams{
				AdminURL: "https://admin:8090", DataURL: "https://api:8080", IAMURL: "https://api:8085",
				TLS: MCPUpstreamTLS{CaPath: ca, CertPath: "/c.crt"},
			},
			wantErr: "must be set together",
		},
		{
			name: "key without cert",
			in: MCPUpstreams{
				AdminURL: "https://admin:8090", DataURL: "https://api:8080", IAMURL: "https://api:8085",
				TLS: MCPUpstreamTLS{CaPath: ca, KeyPath: "/c.key"},
			},
			wantErr: "must be set together",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.in.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// TestMCPUpstreamsValidateNamesTheOffendingField keeps the diagnostics useful:
// three upstreams look alike in a config file, so an error that does not name
// one leaves the operator checking all three.
func TestMCPUpstreamsValidateNamesTheOffendingField(t *testing.T) {
	t.Parallel()

	err := MCPUpstreams{
		AdminURL: "http://admin:8090",
		DataURL:  "ftp://api:8080",
		IAMURL:   "http://api:8085",
	}.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want an error")
	}
	if !strings.Contains(err.Error(), "data_url") {
		t.Errorf("error %q does not name data_url", err)
	}
}
