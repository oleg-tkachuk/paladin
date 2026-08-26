package mcpinspecth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"

	adminv1 "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/config"
	mcppkg "github.com/oleg-tkachuk/paladin/internal/mcp"
)

// The behaviour this RPC exists for: when the bridge cannot be reached, say
// so. ListSessions answers an empty list in the same situation — deliberately,
// so one dead replica cannot fail the call — which left "the bridge is down"
// and "nobody is using MCP" looking identical on the page an operator opens
// when something is wrong.
func TestGetBridgeStatus_UnreachableBridgeIsReported(t *testing.T) {
	h := newTestHandler(t, "http://127.0.0.1:1/sessions") // nothing listens on port 1

	res, err := h.GetBridgeStatus(ctxAs(apiutil.RolePlatformAdmin),
		connect.NewRequest(&adminv1.GetBridgeStatusRequest{}))
	if err != nil {
		t.Fatalf("GetBridgeStatus returned an error instead of a verdict: %v", err)
	}
	if res.Msg.GetReachable() {
		t.Error("reachable=true for a bridge that is not listening")
	}
	if res.Msg.GetError() == "" {
		t.Error("no reason given — the reason is what the operator came for")
	}
}

// No MCP server configured is a different answer from one that is down, and
// the message has to say which.
func TestGetBridgeStatus_UnconfiguredSaysSo(t *testing.T) {
	h := newTestHandler(t, "")

	res, err := h.GetBridgeStatus(ctxAs(apiutil.RolePlatformAdmin),
		connect.NewRequest(&adminv1.GetBridgeStatusRequest{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Msg.GetReachable() {
		t.Error("reachable=true with no MCP server configured")
	}
	if got := res.Msg.GetError(); got == "" {
		t.Error("unconfigured bridge reported no reason")
	}
}

// A reachable bridge's own view of its upstreams passes through intact — an
// unreachable plane must arrive as unreachable, with its reason, rather than
// being flattened into "the bridge is up".
func TestGetBridgeStatus_PassesThroughUpstreamHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			t.Errorf("admin plane asked for %q, want /status", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(mcppkg.BridgeStatus{
			Sessions: 3,
			Upstreams: []mcppkg.UpstreamStatus{
				{Name: "admin", URL: "https://admin", Reachable: true, LatencyMs: 4},
				{Name: "data", URL: "https://data", Reachable: false, Error: "connection refused"},
			},
		})
	}))
	defer srv.Close()

	h := newTestHandler(t, srv.URL+"/sessions")
	res, err := h.GetBridgeStatus(ctxAs(apiutil.RolePlatformAdmin),
		connect.NewRequest(&adminv1.GetBridgeStatusRequest{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Msg.GetReachable() {
		t.Fatalf("reachable=false for a bridge that answered: %s", res.Msg.GetError())
	}
	if res.Msg.GetSessions() != 3 {
		t.Errorf("sessions = %d, want 3", res.Msg.GetSessions())
	}
	if len(res.Msg.GetUpstreams()) != 2 {
		t.Fatalf("upstreams = %d, want 2", len(res.Msg.GetUpstreams()))
	}
	var data *adminv1.MCPUpstreamHealth
	for _, u := range res.Msg.GetUpstreams() {
		if u.GetName() == "data" {
			data = u
		}
	}
	if data == nil || data.GetReachable() || data.GetError() != "connection refused" {
		t.Errorf("the unreachable plane did not survive the proxy: %+v", data)
	}
}

func newTestHandler(t *testing.T, sessionsURL string) *Handler {
	t.Helper()
	cfg := config.MCP{}
	cfg.HTTP.SessionsURL = sessionsURL
	return NewHandler(cfg, allowAll())
}
