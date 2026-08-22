package main

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oleg-tkachuk/paladin/internal/config"
)

// writeCAFile serialises the test server's own certificate as a PEM bundle —
// the stand-in for the internal mTLS CA the planes' certificates chain to.
func writeCAFile(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	cert := srv.Certificate()
	path := filepath.Join(t.TempDir(), "ca.crt")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}
	return path
}

// TestUpstreamHTTPClientTrustsConfiguredCA is the regression for the deployed
// bridge's broken state: it dialled TLS planes over http:// with no CA, so every
// tools/call came back as "internal: 400 Bad Request" (a Go TLS listener's reply
// to a plaintext request). Both halves are asserted — the CA makes the dial
// work, and its absence makes it fail — because a test that only proves the
// success path would still pass if the trust material were quietly ignored.
func TestUpstreamHTTPClientTrustsConfiguredCA(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	caPath := writeCAFile(t, srv)

	t.Run("with the CA the dial verifies", func(t *testing.T) {
		c, err := upstreamHTTPClient(config.MCPUpstreams{
			AdminURL: srv.URL, DataURL: srv.URL, IAMURL: srv.URL,
			TLS: config.MCPUpstreamTLS{CaPath: caPath},
		})
		if err != nil {
			t.Fatalf("upstreamHTTPClient: %v", err)
		}
		resp, err := c.Do(mustGet(t, srv.URL))
		if err != nil {
			t.Fatalf("GET over TLS with the CA installed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusNoContent {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
		}
	})

	t.Run("without the CA verification fails", func(t *testing.T) {
		c, err := upstreamHTTPClient(config.MCPUpstreams{
			AdminURL: srv.URL, DataURL: srv.URL, IAMURL: srv.URL,
		})
		if err != nil {
			t.Fatalf("upstreamHTTPClient: %v", err)
		}
		resp, err := c.Do(mustGet(t, srv.URL))
		if err == nil {
			_ = resp.Body.Close()
			t.Fatal("GET succeeded without the CA — the system roots should not " +
				"contain this certificate, so config validation requiring ca_path " +
				"would be pointless")
		}
		var unknown x509.UnknownAuthorityError
		if !errors.As(err, &unknown) {
			t.Errorf("error = %v, want x509.UnknownAuthorityError", err)
		}
	})

	t.Run("insecure_skip_verify dials without any CA", func(t *testing.T) {
		c, err := upstreamHTTPClient(config.MCPUpstreams{
			AdminURL: srv.URL, DataURL: srv.URL, IAMURL: srv.URL,
			TLS: config.MCPUpstreamTLS{InsecureSkipVerify: true},
		})
		if err != nil {
			t.Fatalf("upstreamHTTPClient: %v", err)
		}
		resp, err := c.Do(mustGet(t, srv.URL))
		if err != nil {
			t.Fatalf("GET with insecure_skip_verify: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
	})
}

// TestUpstreamHTTPClientPlaintextIsUntouched pins that a plaintext deployment
// pays nothing for the TLS support: no trust material configured means the
// default transport, unchanged.
func TestUpstreamHTTPClientPlaintextIsUntouched(t *testing.T) {
	t.Parallel()

	c, err := upstreamHTTPClient(config.MCPUpstreams{
		AdminURL: "http://admin:8090", DataURL: "http://api:8080", IAMURL: "http://api:8085",
	})
	if err != nil {
		t.Fatalf("upstreamHTTPClient: %v", err)
	}
	if c.Transport != nil {
		t.Errorf("Transport = %T, want nil (http.DefaultTransport)", c.Transport)
	}
	if c.Timeout == 0 {
		t.Error("client has no timeout — a hung plane would hang the bridge")
	}
}

// TestUpstreamHTTPClientRejectsUnreadableCA asserts a typo'd or unmounted CA
// path is a startup error, not a client that silently falls back to the system
// roots and fails later on every call.
func TestUpstreamHTTPClientRejectsUnreadableCA(t *testing.T) {
	t.Parallel()

	_, err := upstreamHTTPClient(config.MCPUpstreams{
		AdminURL: "https://admin:8090", DataURL: "https://api:8080", IAMURL: "https://api:8085",
		TLS: config.MCPUpstreamTLS{CaPath: filepath.Join(t.TempDir(), "absent.crt")},
	})
	if err == nil {
		t.Fatal("upstreamHTTPClient accepted a CA path that does not exist")
	}
	if !strings.Contains(err.Error(), "tls") {
		t.Errorf("error %q does not identify the TLS config as the cause", err)
	}
}

// mustGet builds a context-carrying GET so the client calls stay lint-clean.
func mustGet(t *testing.T, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	return req
}
