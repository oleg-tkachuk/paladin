package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRequireToken pins the status code a credential-less MCP request gets.
//
// This is not cosmetic. Without the gate the request reaches the SDK's session
// factory, which returns nil for a missing token, and the SDK reports that as
// "400 Bad Request: no server available" — indistinguishable from a malformed
// request. An MCP client cannot tell it needs to authenticate, and an operator
// reading logs sees a client bug where there is an auth gap. 401 says the one
// thing that is true.
func TestRequireToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		header     string
		value      string
		wantStatus int
		wantNext   bool
	}{
		{name: "no credential", wantStatus: http.StatusUnauthorized},
		{name: "empty Authorization", header: "Authorization", value: "", wantStatus: http.StatusUnauthorized},
		{name: "Authorization without Bearer scheme", header: "Authorization", value: "Basic abc", wantStatus: http.StatusUnauthorized},
		{name: "Bearer with no token", header: "Authorization", value: "Bearer ", wantStatus: http.StatusUnauthorized},
		{name: "bearer token", header: "Authorization", value: "Bearer t0k", wantStatus: http.StatusOK, wantNext: true},
		{name: "lowercase bearer scheme", header: "Authorization", value: "bearer t0k", wantStatus: http.StatusOK, wantNext: true},
		{name: "legacy X-Paladin-Token", header: "X-Paladin-Token", value: "t0k", wantStatus: http.StatusOK, wantNext: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var reached bool
			h := RequireToken(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", nil)
			if tc.header != "" {
				req.Header.Set(tc.header, tc.value)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if reached != tc.wantNext {
				t.Errorf("next handler reached = %v, want %v", reached, tc.wantNext)
			}
			if tc.wantNext {
				return
			}

			// The challenge must be a bare "Bearer": with OAuth disabled there
			// is no RFC 9728 document served, so resource_metadata="" would
			// point a discovering client at nothing.
			got := rec.Header().Get("WWW-Authenticate")
			if got != "Bearer" {
				t.Errorf("WWW-Authenticate = %q, want exactly %q", got, "Bearer")
			}
			if body := rec.Body.String(); !strings.Contains(body, `"error"`) {
				t.Errorf("401 body carries no error field: %q", body)
			}
		})
	}
}

// TestWriteChallengeIncludesResourceMetadata is RequireToken's counterpart: the
// OAuth-enabled path must still emit the discovery pointer, and must keep the
// comma separator between parameters that RFC 9728 §5.1 requires.
func TestWriteChallengeIncludesResourceMetadata(t *testing.T) {
	t.Parallel()

	t.Run("metadata only", func(t *testing.T) {
		rec := httptest.NewRecorder()
		writeChallenge(rec, "/.well-known/oauth-protected-resource", "", "")
		want := `Bearer resource_metadata="/.well-known/oauth-protected-resource"`
		if got := rec.Header().Get("WWW-Authenticate"); got != want {
			t.Errorf("WWW-Authenticate = %q, want %q", got, want)
		}
	})

	t.Run("metadata and error", func(t *testing.T) {
		rec := httptest.NewRecorder()
		writeChallenge(rec, "/meta", "invalid_token", "expired")
		got := rec.Header().Get("WWW-Authenticate")
		want := `Bearer resource_metadata="/meta", error="invalid_token", error_description="expired"`
		if got != want {
			t.Errorf("WWW-Authenticate = %q, want %q", got, want)
		}
	})

	t.Run("error without metadata omits the leading comma", func(t *testing.T) {
		rec := httptest.NewRecorder()
		writeChallenge(rec, "", "invalid_token", "expired")
		got := rec.Header().Get("WWW-Authenticate")
		want := `Bearer error="invalid_token", error_description="expired"`
		if got != want {
			t.Errorf("WWW-Authenticate = %q, want %q", got, want)
		}
	})
}
