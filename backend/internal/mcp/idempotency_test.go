package mcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// An agent's own key must reach the plane as the header: the plane refuses a
// request whose header and idempotency_key field disagree, and the bridge
// used to stamp a fresh UUID beside every agent-supplied key. The keys come
// from the Go SDK's clients now; this pins that the bridge's calls get them.
func TestAgentIdempotencyKeyBecomesTheHeader(t *testing.T) {
	var got []string
	plane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get(paladin.HeaderIdempotencyKey))
		w.Header().Set("Content-Type", "application/proto")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(plane.Close)
	cs := dialInProcess(t, mustClients(t)(NewClients(plane.Client(), plane.URL, plane.URL, plane.URL, "tok")))

	for _, args := range []map[string]any{
		{"parent": "tenants/t1/collections/c1", "content_type": "text/plain", "size_bytes": 0, "checksum_value": "47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=", "idempotency_key": "agent-key-1"},
		{"parent": "tenants/t1/collections/c1", "content_type": "text/plain", "size_bytes": 0, "checksum_value": "47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="},
	} {
		if _, err := cs.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: "paladin_upload_object", Arguments: args}); err != nil {
			t.Fatalf("call: %v", err)
		}
	}
	if len(got) != 2 || got[0] != "agent-key-1" || got[1] == "" {
		t.Errorf("headers = %q, want the agent's key, then a generated one", got)
	}
}
