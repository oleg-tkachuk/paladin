package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestServerDoesNotAdvertiseDeprecatedLogging pins the capability set the
// bridge negotiates. The SDK's default (ServerOptions.Capabilities == nil) is
// {"logging":{}} "for historical reasons" — a feature we implement no handler
// for, and one SEP-2577 deprecated as of protocol version 2026-07-28. NewServer
// passes an empty non-nil ServerCapabilities to suppress it.
//
// The second half matters as much as the first: suppressing the default must
// not suppress *inference*. Tools / resources / prompts are still advertised
// because they are left nil and inferred from the registrations.
func TestServerDoesNotAdvertiseDeprecatedLogging(t *testing.T) {
	t.Parallel()

	cs := dialInProcess(t, mustClients(t)(NewInlineClients(InlineHandlers{}, "tok")))
	caps := cs.InitializeResult().Capabilities

	// Reading the deprecated field is the entire point of the assertion.
	//nolint:staticcheck // SA1019: asserting we do NOT advertise a deprecated capability
	if caps.Logging != nil {
		t.Error("server advertises the deprecated logging capability (SEP-2577); " +
			"NewServer should pass a non-nil empty ServerCapabilities")
	}
	if caps.Tools == nil {
		t.Error("tools capability not advertised — inference from AddTool broke")
	}
	if caps.Resources == nil {
		t.Error("resources capability not advertised — inference from AddResource broke")
	}
	if caps.Prompts == nil {
		t.Error("prompts capability not advertised — inference from AddPrompt broke")
	}
	if caps.Completions != nil {
		t.Error("completions advertised but no completion handler is registered")
	}
}

// TestRegisterResourcesDispatch covers registerResources + addJSONResource end
// to end: the three URIs are listed, each read reaches the admin RPC it claims
// to expose, and the payload comes back as the application/json the resource
// descriptor promises. A resource wired to the wrong client would still
// compile and still return valid JSON — asserting the Connect path is what
// makes this load-bearing.
func TestRegisterResourcesDispatch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		uri  string
		want string
	}{
		{"paladin://backends", "/paladin.admin.v1.BackendService/ListBackends"},
		{"paladin://buckets", "/paladin.admin.v1.BucketService/ListBuckets"},
		{"paladin://tenants", "/paladin.admin.v1.TenantService/ListTenants"},
	}

	t.Run("catalog", func(t *testing.T) {
		cs := dialInProcess(t, mustClients(t)(NewInlineClients(InlineHandlers{}, "tok")))
		res, err := cs.ListResources(context.Background(), &mcpsdk.ListResourcesParams{})
		if err != nil {
			t.Fatalf("ListResources: %v", err)
		}
		var got []string
		for _, r := range res.Resources {
			got = append(got, r.URI)
			if r.MIMEType != "application/json" {
				t.Errorf("%s: MIMEType = %q, want application/json", r.URI, r.MIMEType)
			}
			if r.Name == "" || r.Description == "" {
				t.Errorf("%s: name/description must be non-empty for client display", r.URI)
			}
		}
		var want []string
		for _, c := range cases {
			want = append(want, c.uri)
		}
		sort.Strings(got)
		sort.Strings(want)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("resources = %v, want %v", got, want)
		}
	})

	for _, tc := range cases {
		t.Run(tc.uri, func(t *testing.T) {
			var paths []string
			cs := dialInProcess(t, mustClients(t)(NewInlineClients(InlineHandlers{
				Admin: recordingProtoPlane(&paths),
			}, "tok")))

			res, err := cs.ReadResource(context.Background(), &mcpsdk.ReadResourceParams{URI: tc.uri})
			if err != nil {
				t.Fatalf("ReadResource %s: %v", tc.uri, err)
			}
			if len(paths) != 1 || paths[0] != tc.want {
				t.Fatalf("%s dispatched to %v, want [%s]", tc.uri, paths, tc.want)
			}
			if len(res.Contents) != 1 {
				t.Fatalf("%s: %d contents, want 1", tc.uri, len(res.Contents))
			}
			c := res.Contents[0]
			if c.URI != tc.uri {
				t.Errorf("content URI = %q, want %q (must echo the request)", c.URI, tc.uri)
			}
			if c.MIMEType != "application/json" {
				t.Errorf("content MIMEType = %q, want application/json", c.MIMEType)
			}
			if !json.Valid([]byte(c.Text)) {
				t.Errorf("content is not valid JSON: %q", c.Text)
			}
		})
	}
}

// TestReadResourcePropagatesUpstreamError asserts a failing upstream surfaces
// as a protocol error rather than an empty-but-successful read. Returning
// {} with no error here would leave an agent unable to tell "no backends" from
// "the admin plane is down".
func TestReadResourcePropagatesUpstreamError(t *testing.T) {
	t.Parallel()

	cs := dialInProcess(t, mustClients(t)(NewInlineClients(InlineHandlers{
		Admin: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}),
	}, "tok")))

	if _, err := cs.ReadResource(context.Background(), &mcpsdk.ReadResourceParams{
		URI: "paladin://tenants",
	}); err == nil {
		t.Fatal("ReadResource succeeded despite a 500 from the admin plane")
	}
}

// TestRegisterPrompts covers the prompt surface: both prompts are listed with
// their declared arguments, and a get renders the argument into the message.
func TestRegisterPrompts(t *testing.T) {
	t.Parallel()

	cs := dialInProcess(t, mustClients(t)(NewInlineClients(InlineHandlers{}, "tok")))
	ctx := context.Background()

	list, err := cs.ListPrompts(ctx, &mcpsdk.ListPromptsParams{})
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	if len(list.Prompts) == 0 {
		t.Fatal("no prompts registered")
	}
	byName := map[string]*mcpsdk.Prompt{}
	for _, p := range list.Prompts {
		if p.Description == "" {
			t.Errorf("prompt %q has no description", p.Name)
		}
		byName[p.Name] = p
	}

	audit, ok := byName["audit_access_for_tenant"]
	if !ok {
		t.Fatalf("audit_access_for_tenant missing; have %v", byName)
	}
	if len(audit.Arguments) != 1 || audit.Arguments[0].Name != "tenant_id" || !audit.Arguments[0].Required {
		t.Fatalf("audit_access_for_tenant arguments = %+v, want one required tenant_id", audit.Arguments)
	}

	got, err := cs.GetPrompt(ctx, &mcpsdk.GetPromptParams{
		Name:      "audit_access_for_tenant",
		Arguments: map[string]string{"tenant_id": "acme-corp"},
	})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	if len(got.Messages) == 0 {
		t.Fatal("GetPrompt returned no messages")
	}
	text, ok := got.Messages[0].Content.(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("message content is %T, want *mcpsdk.TextContent", got.Messages[0].Content)
	}
	if !strings.Contains(text.Text, "acme-corp") {
		t.Errorf("rendered prompt does not mention the tenant argument:\n%s", text.Text)
	}
}

// recordingProtoPlane is recordingPlane's honest sibling: it records the
// Connect procedure path AND answers in the codec the client actually speaks.
// An empty body is a valid empty protobuf message, so the client decodes a
// zero-value response instead of failing on the content type.
//
// This distinction is invisible in tools/call — a transport failure there comes
// back as CallToolResult.IsError with a nil Go error, so a dispatch test still
// passes. It is not invisible in resources/read, which surfaces the failure as
// a protocol error.
func recordingProtoPlane(paths *[]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*paths = append(*paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/proto")
		w.WriteHeader(http.StatusOK)
	})
}
