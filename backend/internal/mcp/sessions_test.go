package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin-private/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin-private/internal/auth"
)

type fakeVerifier struct {
	principal *auth.Principal
	err       error
}

func (f fakeVerifier) Verify(context.Context, string) (*auth.Principal, error) {
	return f.principal, f.err
}

// fakeClock lets the test drive last_seen / reap deterministically.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func newTestRegistry(c *fakeClock) *SessionRegistry {
	return &SessionRegistry{now: c.now, sessions: map[string]*sessionEntry{}}
}

// upstream is a stand-in for the SDK streamable handler: on a body with no
// session header (the initialize) it MINTS one on the response; otherwise it
// just echoes 200. It also asserts the body survived the sniff intact.
func upstream(t *testing.T, mintID string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drain the body to confirm the sniff in TrackSessions restored it.
		if r.ContentLength > 0 {
			_, _ = r.Body.Read(make([]byte, r.ContentLength))
		}
		if r.Header.Get(mcpSessionIDHeader) == "" {
			w.Header().Set(mcpSessionIDHeader, mintID)
		}
		w.WriteHeader(http.StatusOK)
	})
}

func TestTrackSessions_MintAndContinue(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	reg := newTestRegistry(clk)
	h := TrackSessions(upstream(t, "sess-1"), reg, func(*http.Request) string { return "agent-a" })

	// 1) initialize: no request session id → server mints "sess-1" on response.
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","method":"initialize","id":1}`))
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get(mcpSessionIDHeader); got != "sess-1" {
		t.Fatalf("minted session header = %q, want sess-1", got)
	}

	snap := reg.Snapshot()
	if len(snap) != 1 || snap[0].ID != "sess-1" {
		t.Fatalf("after initialize: snapshot = %+v, want one sess-1", snap)
	}
	if snap[0].AgentSubject != "agent-a" {
		t.Errorf("agent_subject = %q, want agent-a", snap[0].AgentSubject)
	}
	if snap[0].ToolCallCount != 0 {
		t.Errorf("tool_call_count after initialize = %d, want 0", snap[0].ToolCallCount)
	}

	// 2) continuation tools/call carries the session id → counts + touches.
	clk.t = clk.t.Add(30 * time.Second)
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","method":"tools/call","id":2}`))
	req2.Header.Set(mcpSessionIDHeader, "sess-1")
	h.ServeHTTP(rec2, req2)

	snap = reg.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("after tools/call: %d sessions, want 1 (no duplicate)", len(snap))
	}
	s := snap[0]
	if s.ToolCallCount != 1 {
		t.Errorf("tool_call_count = %d, want 1", s.ToolCallCount)
	}
	if s.RequestCount != 2 {
		t.Errorf("request_count = %d, want 2", s.RequestCount)
	}
	if !s.LastSeen.After(s.StartedAt) {
		t.Errorf("last_seen %v not after started_at %v", s.LastSeen, s.StartedAt)
	}

	// 3) a non-tool continuation does not bump the tool counter.
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","method":"tools/list","id":3}`))
	req3.Header.Set(mcpSessionIDHeader, "sess-1")
	h.ServeHTTP(rec3, req3)
	if got := reg.Snapshot()[0].ToolCallCount; got != 1 {
		t.Errorf("tool_call_count after tools/list = %d, want 1", got)
	}
}

func TestSessionRegistry_Reap(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	reg := newTestRegistry(clk)
	reg.observe("old", "", false)
	clk.t = clk.t.Add(10 * time.Minute)
	reg.observe("fresh", "", false)

	if n := reg.Reap(5 * time.Minute); n != 1 {
		t.Fatalf("reaped %d, want 1", n)
	}
	snap := reg.Snapshot()
	if len(snap) != 1 || snap[0].ID != "fresh" {
		t.Fatalf("after reap: %+v, want only fresh", snap)
	}
	if n := reg.Reap(0); n != 0 {
		t.Errorf("Reap(0) evicted %d, want 0 (disabled)", n)
	}
}

func TestSessionsHandler(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	reg := newTestRegistry(clk)
	reg.observe("s1", "agent-a", true)

	admin := fakeVerifier{principal: &auth.Principal{Roles: []string{apiutil.RolePlatformAdmin}}}
	nonAdmin := fakeVerifier{principal: &auth.Principal{Roles: []string{"tenant.admin"}}}
	bad := fakeVerifier{err: errors.New("bad token")}

	call := func(v auth.TokenVerifier, authz string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/sessions", nil)
		if authz != "" {
			req.Header.Set("Authorization", authz)
		}
		rec := httptest.NewRecorder()
		SessionsHandler(reg, v).ServeHTTP(rec, req)
		return rec
	}

	rec := call(admin, "Bearer ok")
	if rec.Code != http.StatusOK {
		t.Fatalf("admin GET = %d, want 200", rec.Code)
	}
	var got []SessionInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].ID != "s1" || got[0].ToolCallCount != 1 {
		t.Fatalf("snapshot = %+v, want one s1 with 1 tool call", got)
	}

	if c := call(admin, "").Code; c != http.StatusUnauthorized {
		t.Errorf("no auth = %d, want 401", c)
	}
	if c := call(bad, "Bearer x").Code; c != http.StatusUnauthorized {
		t.Errorf("bad token = %d, want 401", c)
	}
	if c := call(nonAdmin, "Bearer ok").Code; c != http.StatusForbidden {
		t.Errorf("non-admin = %d, want 403", c)
	}
	if c := call(nil, "Bearer ok").Code; c != http.StatusUnauthorized {
		t.Errorf("nil verifier = %d, want 401", c)
	}
}
