package oauth

import (
	"net/url"
	"testing"
)

// Every authorize redirect is the client's registered URI with its parameters
// appended as a query. Built that way, nothing the caller supplies — state
// above all — can change where the browser is sent.
func TestRedirectTo(t *testing.T) {
	for _, tc := range []struct {
		name, base, state, wantHost, wantPrefix string
	}{
		{"no query on the registered uri", "https://app.example/cb", "s1", "app.example", "https://app.example/cb?"},
		{"registered uri with a query", "https://app.example/cb?tenant=acme", "s1", "app.example", "https://app.example/cb?tenant=acme&"},
		{"custom scheme", "claude-desktop://cb", "s1", "cb", "claude-desktop://cb?"},
		{"a state built to move the host", "https://app.example/cb", "x@evil.test/#", "app.example", "https://app.example/cb?"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := redirectTo(tc.base, url.Values{"code": {"c1"}, "state": {tc.state}})
			u, err := url.Parse(got)
			if err != nil {
				t.Fatalf("%q does not parse: %v", got, err)
			}
			if u.Host != tc.wantHost {
				t.Errorf("host = %q, want %q (url %q)", u.Host, tc.wantHost, got)
			}
			if len(got) < len(tc.wantPrefix) || got[:len(tc.wantPrefix)] != tc.wantPrefix {
				t.Errorf("url %q does not start with the registered uri %q", got, tc.wantPrefix)
			}
			if u.Query().Get("code") != "c1" || u.Query().Get("state") != tc.state {
				t.Errorf("query = %v, want code=c1 state=%q", u.Query(), tc.state)
			}
		})
	}
}
