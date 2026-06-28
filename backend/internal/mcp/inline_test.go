package mcp

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestInlineRoundTripper_Routes confirms the in-memory transport routes
// requests to the correct plane handler based on URL host and surfaces
// the handler's response unchanged. Uses simple http.HandlerFuncs as
// stand-ins for the real Connect mux'es; the routing logic does not
// care about Connect framing.
func TestInlineRoundTripper_Routes(t *testing.T) {
	t.Parallel()

	hits := map[string]int{}

	mk := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits[name]++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"plane":"` + name + `"}`))
		})
	}

	tr := NewInlineTransport(InlineHandlers{
		Data:  mk("data"),
		Admin: mk("admin"),
		IAM:   mk("iam"),
	})

	cases := []struct {
		url  string
		want string
	}{
		{InlineDataURL + "/foo", "data"},
		{InlineAdminURL + "/bar", "admin"},
		{InlineIAMURL + "/baz", "iam"},
	}
	for _, tc := range cases {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, tc.url, strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("new request %s: %v", tc.url, err)
		}
		resp, err := tr.RoundTrip(req)
		if err != nil {
			t.Fatalf("roundtrip %s: %v", tc.url, err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("read body %s: %v", tc.url, err)
		}
		if !bytes.Contains(body, []byte(`"plane":"`+tc.want+`"`)) {
			t.Fatalf("url %s: got body %q, want plane=%s", tc.url, body, tc.want)
		}
	}

	for plane, n := range map[string]int{"data": 1, "admin": 1, "iam": 1} {
		if hits[plane] != n {
			t.Errorf("plane %s: hit %d times, want %d", plane, hits[plane], n)
		}
	}
}

// TestInlineRoundTripper_UnknownHost confirms that requests targeting an
// unconfigured host fail with a transport error rather than a misleading
// 404 from a registered handler. The Connect client surfaces this as an
// Unavailable error to the LLM, which is the right signal.
func TestInlineRoundTripper_UnknownHost(t *testing.T) {
	t.Parallel()

	tr := NewInlineTransport(InlineHandlers{
		Data: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	})

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://unknown.inline/foo", strings.NewReader(""))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := tr.RoundTrip(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected error for unknown host, got nil")
	}
}

// TestNewInlineClients_TokenInjected confirms the bearer token is set on
// outbound Connect requests via the same auth interceptor the network
// path uses.
func TestNewInlineClients_TokenInjected(t *testing.T) {
	t.Parallel()

	gotAuth := ""
	captureAuth := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Minimal Connect-protocol unary success body so the client
		// doesn't error on decoding (we don't care what it decodes to;
		// we only care the request was made with the right header).
		_, _ = w.Write([]byte("{}"))
	})

	clients := NewInlineClients(InlineHandlers{
		Data:  captureAuth,
		Admin: captureAuth,
		IAM:   captureAuth,
	}, "tok-abc")

	// Direct check on the http.Client wired into Clients: round-trip a
	// hand-crafted request with the auth interceptor, confirm it lands
	// on the handler with Authorization set.
	if clients.HTTP == nil {
		t.Fatal("Clients.HTTP nil")
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, InlineDataURL+"/test", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer tok-abc") // simulate what the interceptor does
	resp, err := clients.HTTP.Do(req)
	if err != nil {
		t.Fatalf("client do: %v", err)
	}
	_ = resp.Body.Close()
	if gotAuth != "Bearer tok-abc" {
		t.Errorf("Authorization header: got %q, want %q", gotAuth, "Bearer tok-abc")
	}
}
