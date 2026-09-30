package mcp

import (
	"context"
	"regexp"
	"slices"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

func dialWithFilter(t *testing.T, filter *ToolFilter) *mcpsdk.ClientSession {
	t.Helper()
	srv := NewServer("t", "0", NewClients(nil, "http://admin", "http://data", "http://iam", ""), filter)
	st, ct := mcpsdk.NewInMemoryTransports()
	if _, err := srv.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "t", Version: "0"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// A resource is another door to the data a list tool reads. Resources used to
// be registered whatever the profile, so one that withheld paladin_list_tenants
// still listed every tenant at paladin://tenants.
func TestResourcesFollowTheProfile(t *testing.T) {
	cfg := config.MCP{Profiles: map[string]config.MCPProfile{
		"backends_only": {Tools: []string{"paladin_list_backends"}},
	}}
	cs := dialWithFilter(t, NewToolFilter(cfg, "backends_only"))

	res, err := cs.ListResources(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var uris []string
	for _, r := range res.Resources {
		uris = append(uris, r.URI)
	}
	if !slices.Equal(uris, []string{"paladin://backends"}) {
		t.Errorf("resources = %v, want only paladin://backends", uris)
	}

	prompts, err := cs.ListPrompts(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range prompts.Prompts {
		if p.Name == "audit_access_for_tenant" {
			t.Error("audit_access_for_tenant offered without the tools it tells the model to call")
		}
	}
}

var toolName = regexp.MustCompile(`paladin_[a-z_]+`)

// rotate_backend_credentials told the model to call paladin_test_backend,
// which no catalog has ever had.
func TestPromptsNameOnlyToolsThatExist(t *testing.T) {
	cs := dialWithFilter(t, nil)
	known := map[string]bool{}
	for _, m := range DefaultCatalog {
		known[m.Name] = true
	}
	for name, args := range map[string]map[string]string{
		"audit_access_for_tenant":    {"tenant_id": "acme"},
		"rotate_backend_credentials": {"backend_id": "primary"},
	} {
		got, err := cs.GetPrompt(t.Context(), &mcpsdk.GetPromptParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, m := range got.Messages {
			text := m.Content.(*mcpsdk.TextContent).Text
			for _, tool := range toolName.FindAllString(text, -1) {
				if !known[tool] {
					t.Errorf("%s names %s, which is not in the catalog", name, tool)
				}
			}
		}
	}
}
