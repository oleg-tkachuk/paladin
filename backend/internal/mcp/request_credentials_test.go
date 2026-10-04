package mcp

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// The planes must see the token the caller holds now. The bridge used to fix
// the token that opened the session into its Clients, so after a refresh every
// call went out with the expired one.
func TestToolCallsPresentTheRequestsToken(t *testing.T) {
	var (
		mu   sync.Mutex
		seen []string
	)
	plane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/proto")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(plane.Close)

	server := NewServer("t", "0", mustClients(t)(NewClients(plane.Client(), plane.URL, plane.URL, plane.URL, "")), nil)
	h := mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return server }, nil)
	ts := httptest.NewServer(RequireBearer(h, twoUsers{}, ""))
	t.Cleanup(ts.Close)

	sid := openSession(t, ts.URL, "Authorization", "alice-tok")
	post(t, ts.URL, "Authorization", "alice-tok", sid, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	call := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"paladin_list_backends","arguments":{}}}`
	if got := post(t, ts.URL, "Authorization", "alice-tok-refreshed", sid, call).StatusCode; got != http.StatusOK {
		t.Fatalf("tools/call status %d", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || seen[0] != "Bearer alice-tok-refreshed" {
		t.Errorf("plane saw %q, want the refreshed token", seen)
	}
}

// A session holding only a capability calls the planes with it and with no
// Authorization at all: an empty bearer is not a credential to send.
func TestCapabilityOnlyCallsSendNoBearer(t *testing.T) {
	var (
		mu   sync.Mutex
		seen []http.Header
	)
	plane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Clone())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/proto")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(plane.Close)

	const capability = "cap-only"
	cs := dialInProcess(t, mustClients(t)(NewClientsWithCapability(plane.Client(), plane.URL, plane.URL, plane.URL, "", capability)))
	if _, err := cs.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: "paladin_list_backends", Arguments: map[string]any{}}); err != nil {
		t.Fatalf("call: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 {
		t.Fatalf("plane calls = %d, want 1", len(seen))
	}
	if got, sent := seen[0]["Authorization"]; sent {
		t.Errorf("Authorization sent: %q", got)
	}
	if got := seen[0].Get(paladin.HeaderCapability); got != capability {
		t.Errorf("capability = %q, want %q", got, capability)
	}
}
