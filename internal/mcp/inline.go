// Inline transport for the MCP bridge.
//
// Default mode (cmd/server `serve mcp`) speaks to the PALADIN planes over
// HTTP, so the MCP pod can run independently of the api/admin pods.
// Embedded mode (`serve mcp --embedded`) co-hosts everything in one
// process and routes Connect calls in-memory: no kernel TCP, no port
// binding, no h2c roundtrip.
//
// Implementation: a custom http.RoundTripper inspects req.URL.Host and
// hands the request to the matching plane's *http.ServeMux via
// ServeHTTP. Connect's unary protocol is request-response over
// HTTP/1.1+JSON, which works through httptest.ResponseRecorder without
// special handling. Streaming RPCs (none today) would need a more
// involved transport — see BACKLOG.
//
// Synthetic URL hosts: "data.inline", "admin.inline", "iam.inline". The
// scheme is irrelevant (the transport doesn't dial); convention is "http".
package mcp

import (
	"fmt"
	"net/http"
	"net/http/httptest"
)

// Synthetic hostnames. These match what NewInlineClients passes as the
// admin/data/iam URL strings, and what the inline RoundTripper switches
// on. Externalised so callers can compose without magic strings.
const (
	InlineHostData  = "data.inline"
	InlineHostAdmin = "admin.inline"
	InlineHostIAM   = "iam.inline"

	InlineDataURL  = "http://" + InlineHostData
	InlineAdminURL = "http://" + InlineHostAdmin
	InlineIAMURL   = "http://" + InlineHostIAM
)

// InlineHandlers maps a synthetic host to the *http.Handler that should
// serve requests for that host. Construct via NewInlineHandlers; the
// RoundTripper closes over the map without copying.
type InlineHandlers struct {
	Data  http.Handler
	Admin http.Handler
	IAM   http.Handler
}

// NewInlineTransport returns an http.RoundTripper that dispatches each
// request to the handler keyed by req.URL.Host. Unknown hosts return an
// error; the caller will surface it as a transport failure to the
// Connect client (which becomes a connect.CodeUnavailable for the LLM).
func NewInlineTransport(h InlineHandlers) http.RoundTripper {
	planes := map[string]http.Handler{}
	if h.Data != nil {
		planes[InlineHostData] = h.Data
	}
	if h.Admin != nil {
		planes[InlineHostAdmin] = h.Admin
	}
	if h.IAM != nil {
		planes[InlineHostIAM] = h.IAM
	}
	return &inlineRoundTripper{planes: planes}
}

type inlineRoundTripper struct {
	planes map[string]http.Handler
}

// RoundTrip dispatches the request to the matching handler in-process.
// The Body's Close on the returned response must be called by the
// connect-go client; we close the request body explicitly when the
// handler returns to mirror real-network semantics.
func (t *inlineRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	h, ok := t.planes[req.URL.Host]
	if !ok {
		return nil, fmt.Errorf("inline transport: no handler for host %q", req.URL.Host)
	}

	rec := httptest.NewRecorder()
	// ServeHTTP writes status + headers + body into the recorder. Connect
	// unary uses Content-Type "application/proto" or "application/json"
	// with a single response body — no trailers, no half-close — which
	// the recorder supports without modification.
	h.ServeHTTP(rec, req)

	if req.Body != nil {
		_ = req.Body.Close()
	}
	resp := rec.Result()
	resp.Request = req
	return resp, nil
}

// NewInlineClients is a convenience constructor for an inline-mode Clients
// bundle. Equivalent to NewClients with the synthetic URLs and an
// http.Client whose Transport is the inline RoundTripper.
//
// Bearer is the access token MCP injects on every Connect call; for the
// embedded codepath the verifiers expect the same audience-pinned tokens
// the network mode does, so the caller must produce a real auth string
// (typically a service-account JWT minted at boot).
func NewInlineClients(h InlineHandlers, bearer string) *Clients {
	httpc := &http.Client{Transport: NewInlineTransport(h)}
	return NewClients(httpc, InlineAdminURL, InlineDataURL, InlineIAMURL, bearer)
}
