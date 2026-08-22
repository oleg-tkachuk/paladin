package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestNewSessionRegistry covers the production constructor. The existing tests
// build a registry with an injected clock, so the wall-clock path — the one the
// server actually runs — was never exercised.
func TestNewSessionRegistry(t *testing.T) {
	t.Parallel()

	reg := NewSessionRegistry()
	if reg == nil {
		t.Fatal("NewSessionRegistry returned nil")
	}
	if got := reg.Snapshot(); len(got) != 0 {
		t.Fatalf("fresh registry lists %d sessions, want 0", len(got))
	}

	before := time.Now()
	reg.observe("sess-1", "agent-a", true)
	after := time.Now()

	got := reg.Snapshot()
	if len(got) != 1 {
		t.Fatalf("listed %d sessions, want 1", len(got))
	}
	s := got[0]
	if s.LastSeen.Before(before) || s.LastSeen.After(after) {
		t.Errorf("LastSeen = %v, want a wall-clock reading between %v and %v",
			s.LastSeen, before, after)
	}
	if s.ToolCallCount != 1 || s.RequestCount != 1 {
		t.Errorf("counters = {tools:%d requests:%d}, want {1 1}", s.ToolCallCount, s.RequestCount)
	}
}

// flushRecorder reports whether the tracked writer forwarded a Flush. SSE is
// the MCP streamable transport's delivery path for server→client messages, so a
// swallowed Flush means responses sit in a buffer until the stream ends —
// a hang, not an error, which is why this is worth pinning.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed int
}

func (f *flushRecorder) Flush() { f.flushed++ }

// TestSessionCapturingWriterForwardsFlush covers the wrapper's Flush and Write.
func TestSessionCapturingWriterForwardsFlush(t *testing.T) {
	t.Parallel()

	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	reg := NewSessionRegistry()

	h := TrackSessions(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(mcpSessionIDHeader, "sess-sse")
		// Write without an explicit WriteHeader: the wrapper must still snoop
		// the header on the way past, since that is where the SDK puts the
		// session id it minted.
		if _, err := w.Write([]byte("event: message\n")); err != nil {
			t.Errorf("write: %v", err)
		}
		f, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("tracked writer does not implement http.Flusher — SSE streaming would buffer")
		}
		f.Flush()
	}), reg, nil)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","method":"initialize","id":1}`))
	h.ServeHTTP(rec, req)

	if rec.flushed != 1 {
		t.Errorf("Flush forwarded %d times, want 1", rec.flushed)
	}
	if body := rec.Body.String(); !strings.Contains(body, "event: message") {
		t.Errorf("body = %q, want the handler's bytes to reach the client", body)
	}
	if got := reg.Snapshot(); len(got) != 1 || got[0].ID != "sess-sse" {
		t.Errorf("registry = %+v, want the session id captured from the response header", got)
	}
}

// TestSessionCapturingWriterNonFlusher asserts the wrapper degrades quietly
// when the underlying writer cannot flush, rather than panicking on the type
// assertion.
func TestSessionCapturingWriterNonFlusher(t *testing.T) {
	t.Parallel()

	w := &sessionCapturingWriter{ResponseWriter: nonFlusher{httptest.NewRecorder()}}
	w.Flush() // must not panic
}

type nonFlusher struct{ http.ResponseWriter }
