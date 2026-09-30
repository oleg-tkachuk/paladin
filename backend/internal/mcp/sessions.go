package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// mcpSessionIDHeader is the header the MCP streamable-HTTP transport uses to
// carry the server-minted session id (see the go-sdk streamable transport).
// The SDK sets it on the `initialize` RESPONSE and the client echoes it on the
// REQUEST of every continuation. The constant is duplicated here because the
// SDK keeps its copy unexported.
const mcpSessionIDHeader = "Mcp-Session-Id"

// SessionInfo is a point-in-time snapshot of one live MCP streamable-HTTP
// session. Times are wall-clock; counts are cumulative over the session life.
type SessionInfo struct {
	ID            string    `json:"id"`
	AgentSubject  string    `json:"agent_subject,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	LastSeen      time.Time `json:"last_seen"`
	ToolCallCount int64     `json:"tool_call_count"`
	RequestCount  int64     `json:"request_count"`
}

// SessionRegistry is an in-memory, concurrency-safe table of live MCP
// streamable-HTTP sessions, keyed by the Mcp-Session-Id the SDK mints.
//
// It is PROCESS-LOCAL to the MCP HTTP server: the SDK exposes no session
// enumeration hook, so the tracking middleware (TrackSessions) feeds this
// registry from the wire instead of forking the SDK. Surfacing it through the
// admin-plane MCPInspectService is a separate step — that service runs in a
// different process from the bridge, so it needs either co-location (the
// single-binary multi-mode migration) or a read endpoint to proxy. See
// BACKLOG "Live session enumeration on the streamable-HTTP transport".
type SessionRegistry struct {
	mu       sync.Mutex
	now      func() time.Time
	sessions map[string]*sessionEntry
}

type sessionEntry struct {
	agentSubject  string
	startedAt     time.Time
	lastSeen      time.Time
	toolCallCount int64
	requestCount  int64
}

// NewSessionRegistry returns an empty registry using the wall clock.
func NewSessionRegistry() *SessionRegistry {
	return &SessionRegistry{now: time.Now, sessions: map[string]*sessionEntry{}}
}

// observe records one request against session id. isToolCall increments the
// tool-call counter; subject (may be "") is stored on first sight and on any
// later request that finally carries one. A zero id is ignored — the very
// first `initialize` has no request id yet; the response-header capture in
// TrackSessions records that session under the id the SDK mints.
func (r *SessionRegistry) observe(id, subject string, isToolCall bool) {
	if id == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	e := r.sessions[id]
	if e == nil {
		e = &sessionEntry{agentSubject: subject, startedAt: now}
		r.sessions[id] = e
	}
	if e.agentSubject == "" && subject != "" {
		e.agentSubject = subject
	}
	e.lastSeen = now
	e.requestCount++
	if isToolCall {
		e.toolCallCount++
	}
}

// Snapshot returns all live sessions, oldest-first (stable order for a UI
// table). The returned slice is a copy — safe to read without the lock.
func (r *SessionRegistry) Snapshot() []SessionInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]SessionInfo, 0, len(r.sessions))
	for id, e := range r.sessions {
		out = append(out, SessionInfo{
			ID:            id,
			AgentSubject:  e.agentSubject,
			StartedAt:     e.startedAt,
			LastSeen:      e.lastSeen,
			ToolCallCount: e.toolCallCount,
			RequestCount:  e.requestCount,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].StartedAt.Before(out[j].StartedAt)
	})
	return out
}

// SnapshotPage returns one page of Snapshot's ordering, starting after the
// session whose id is afterID. An unknown or empty afterID starts from the
// beginning — a cursor pointing at a session the reaper has since evicted
// should resume the listing, not fail it.
//
// The scan is linear over an in-memory map that the idle reaper keeps small;
// this is a bound on the RESPONSE, which is what the caller and the wire care
// about, not an index.
func (r *SessionRegistry) SnapshotPage(afterID string, limit int) ([]SessionInfo, string) {
	all := r.Snapshot()
	start := 0
	if afterID != "" {
		for i := range all {
			if all[i].ID == afterID {
				start = i + 1
				break
			}
		}
	}
	if start >= len(all) {
		return []SessionInfo{}, ""
	}
	if limit <= 0 {
		limit = 50
	}
	end := start + limit
	next := ""
	if end < len(all) {
		next = all[end-1].ID
	} else {
		end = len(all)
	}
	return all[start:end], next
}

// Reap drops sessions whose last_seen is older than maxIdle and returns the
// number evicted. The streamable transport has no reliable disconnect signal
// (clients may vanish without a DELETE), so a long-running bridge would leak
// entries without this. maxIdle <= 0 disables reaping.
func (r *SessionRegistry) Reap(maxIdle time.Duration) int {
	if maxIdle <= 0 {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := r.now().Add(-maxIdle)
	n := 0
	for id, e := range r.sessions {
		if e.lastSeen.Before(cutoff) {
			delete(r.sessions, id)
			n++
		}
	}
	return n
}

// TrackSessions wraps the MCP streamable-HTTP handler so every request updates
// reg. The session id is read from the Mcp-Session-Id REQUEST header on
// continuation calls; on the `initialize` that mints it, it is captured from
// the RESPONSE header via a small ResponseWriter shim. subjectFn extracts a
// display identity (e.g. a JWT subject) from the request and may return "".
//
// Cost: POST bodies are buffered once to sniff the JSON-RPC method for the
// tool-call counter. MCP is low-volume control-plane traffic, so the extra
// copy is acceptable; the body is restored intact for the wrapped handler.
func TrackSessions(next http.Handler, reg *SessionRegistry, subjectFn func(*http.Request) string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isToolCall := r.Method == http.MethodPost && sniffToolCall(r)

		subject := ""
		if subjectFn != nil {
			subject = subjectFn(r)
		}

		reqID := r.Header.Get(mcpSessionIDHeader)
		// Continuation request: id known up-front. (No-op for the first
		// initialize, whose request carries no id yet.)
		reg.observe(reqID, subject, isToolCall)

		sw := &sessionCapturingWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)

		// initialize mints the id on the response. Record the new session
		// when the response id differs from the request id; the tool-call was
		// already counted above under reqID (empty on initialize, so no
		// double count).
		if respID := sw.captured; respID != "" && respID != reqID {
			reg.observe(respID, subject, false)
		}
	})
}

// sniffToolCall reads the JSON-RPC method from a POST body and reports whether
// it is a `tools/call`, restoring the body for the downstream handler. Bodies
// over 1 MiB are not fully buffered — the head is enough to read `method`.
func sniffToolCall(r *http.Request) bool {
	if r.Body == nil {
		return false
	}
	const maxSniff = 1 << 20
	head, err := io.ReadAll(io.LimitReader(r.Body, maxSniff))
	// Restore: head (already read) followed by any remainder still in r.Body.
	r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(head), r.Body))
	if err != nil {
		return false
	}
	var msg struct {
		Method string `json:"method"`
	}
	if json.Unmarshal(head, &msg) != nil {
		return false
	}
	return msg.Method == "tools/call"
}

// sessionCapturingWriter snoops the Mcp-Session-Id the SDK writes on the
// response, while otherwise delegating transparently — including Flush, so the
// streamable transport's SSE writes are not buffered.
type sessionCapturingWriter struct {
	http.ResponseWriter
	captured string
	sniffed  bool
}

func (w *sessionCapturingWriter) snoop() {
	if !w.sniffed {
		w.captured = w.Header().Get(mcpSessionIDHeader)
		w.sniffed = true
	}
}

func (w *sessionCapturingWriter) WriteHeader(code int) {
	w.snoop()
	w.ResponseWriter.WriteHeader(code)
}

func (w *sessionCapturingWriter) Write(b []byte) (int, error) {
	w.snoop()
	return w.ResponseWriter.Write(b)
}

// Flush forwards to the wrapped writer so SSE streaming keeps working.
func (w *sessionCapturingWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// SessionsHandler serves the live session registry as a JSON array of
// SessionInfo, for the admin plane's MCPInspectService.ListSessions proxy. It
// requires a Bearer JWT the verifier accepts AND that carries the platform-
// admin role — the same gate as the admin service it backs (the admin plane
// forwards the caller's token; this endpoint re-verifies it rather than
// trusting the network). GET only; a nil verifier rejects every request.
func SessionsHandler(reg *SessionRegistry, verifier auth.TokenVerifier) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if code := authorizePlatformAdmin(r, verifier); code != 0 {
			http.Error(w, http.StatusText(code), code)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reg.Snapshot())
	})
}

// authorizePlatformAdmin gates the bridge's operator endpoints: a Bearer JWT
// the verifier accepts, carrying the platform-admin role — the same gate as
// the admin service they back. Returns 0 when the request may proceed, or the
// HTTP status to answer with.
//
// Shared by /sessions and /status so the two cannot drift apart: an operator
// endpoint that quietly acquired a weaker gate than its sibling is the kind of
// difference nobody notices until it matters.
func authorizePlatformAdmin(r *http.Request, verifier auth.TokenVerifier) int {
	const bearer = "Bearer "
	authz := r.Header.Get("Authorization")
	if verifier == nil || !strings.HasPrefix(authz, bearer) {
		return http.StatusUnauthorized
	}
	p, err := verifier.Verify(r.Context(), strings.TrimSpace(authz[len(bearer):]))
	if err != nil {
		return http.StatusUnauthorized
	}
	if !p.HasRole(apiutil.RolePlatformAdmin) {
		return http.StatusForbidden
	}
	return 0
}
