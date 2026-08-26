package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/auth"
)

// probeStub answers per URL so a test can make one plane unreachable.
func probeStub(failing map[string]error) func(context.Context, string) error {
	return func(_ context.Context, url string) error { return failing[url] }
}

func statusRequest(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/status", nil)
	req.Header.Set("Authorization", "Bearer platform-admin")
	h.ServeHTTP(rec, req)
	return rec
}

// The point of the endpoint: an unreachable plane is reported as unreachable,
// with the reason, rather than omitted. The bridge is the only process that
// knows, and until this existed nobody asked it.
func TestStatusHandler_ReportsUnreachableUpstreams(t *testing.T) {
	targets := []UpstreamTarget{
		{Name: "admin", URL: "https://admin.invalid"},
		{Name: "data", URL: "https://data.invalid"},
	}
	h := StatusHandler(NewSessionRegistry(), fakeVerifier{principal: &auth.Principal{Roles: []string{apiutil.RolePlatformAdmin}}}, targets,
		probeStub(map[string]error{"https://data.invalid": errors.New("connection refused")}))

	rec := statusRequest(t, h)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got BridgeStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Upstreams) != 2 {
		t.Fatalf("upstreams = %d, want 2 — a failing probe must be reported, not dropped", len(got.Upstreams))
	}
	byName := map[string]UpstreamStatus{}
	for _, u := range got.Upstreams {
		byName[u.Name] = u
	}
	if !byName["admin"].Reachable {
		t.Error("admin reported unreachable")
	}
	if byName["data"].Reachable {
		t.Error("data reported reachable while its probe failed")
	}
	if byName["data"].Error == "" {
		t.Error("no reason given for the unreachable plane — the reason is the whole point")
	}
	if got.CheckedAt.IsZero() {
		t.Error("checked_at unset: a cached answer would be indistinguishable from a fresh one")
	}
}

// The gate is the same as /sessions: an admin-audience JWT carrying
// platform-admin. An operator endpoint that quietly took a weaker gate than
// its sibling is exactly what sharing the function prevents.
func TestStatusHandler_RequiresPlatformAdmin(t *testing.T) {
	targets := []UpstreamTarget{{Name: "admin", URL: "https://admin.invalid"}}
	probe := probeStub(nil)

	for name, tc := range map[string]struct {
		verifier auth.TokenVerifier
		header   string
		want     int
	}{
		"no bearer":      {fakeVerifier{principal: &auth.Principal{Roles: []string{apiutil.RolePlatformAdmin}}}, "", http.StatusUnauthorized},
		"nil verifier":   {nil, "Bearer x", http.StatusUnauthorized},
		"bad token":      {fakeVerifier{err: errors.New("nope")}, "Bearer x", http.StatusUnauthorized},
		"missing role":   {fakeVerifier{principal: &auth.Principal{Roles: []string{"tenant.admin"}}}, "Bearer x", http.StatusForbidden},
		"platform.admin": {fakeVerifier{principal: &auth.Principal{Roles: []string{apiutil.RolePlatformAdmin}}}, "Bearer x", http.StatusOK},
	} {
		t.Run(name, func(t *testing.T) {
			h := StatusHandler(NewSessionRegistry(), tc.verifier, targets, probe)
			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/status", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}
