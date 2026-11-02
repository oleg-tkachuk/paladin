package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// limesRefusal is a Connect error as the planes answer the capability
// library's refusal: its limes code in the Paladin ErrorInfo.
func limesRefusal(t *testing.T, code connect.Code, message, limesCode string) error {
	t.Helper()
	detail, err := connectproto.NewErrorDetail(&errdetails.ErrorInfo{
		Domain:   paladin.ErrorDomain,
		Metadata: map[string]string{paladin.ErrorMetadataLimesCode: limesCode},
	})
	if err != nil {
		t.Fatal(err)
	}
	return connect.NewError(code, message).WithDetail(detail)
}

func TestToolError(t *testing.T) {
	cases := []struct {
		name    string
		tool    string
		err     error
		want    []string
		notWant []string
	}{
		{
			name: "a token for another plane says which one is needed",
			tool: "paladin_query_objects",
			err:  connect.NewError(connect.CodeUnauthenticated, "jwt: audience mismatch"),
			want: []string{"data plane", "paladin-data", "API token"},
		},
		{
			name:    "an outage names no internal host",
			tool:    "paladin_list_tenants",
			err:     connect.NewError(connect.CodeUnavailable, "dial tcp 10.0.3.7:8090: connection refused"),
			want:    []string{"admin plane", "try again"},
			notWant: []string{"10.0.3.7", "dial tcp"},
		},
		{
			name: "a NotFound keeps its message",
			tool: "paladin_get_tenant",
			err:  connect.NewError(connect.CodeNotFound, "tenant not found"),
			want: []string{"not_found", "tenant not found"},
		},
		{
			name: "a capability refused names why, as a token's refusal does not",
			tool: "paladin_get_tenant",
			err:  limesRefusal(t, connect.CodeUnauthenticated, "capability: revoked", "revoked"),
			want: []string{"capability", "revoked"},
		},
		{
			name: "an expired token says so",
			tool: "paladin_get_tenant",
			err:  connect.NewError(connect.CodeUnauthenticated, "jwt: token expired"),
			want: []string{"refused", "expired"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := toolError(tc.tool, tc.err).Error()
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("%q lacks %q", got, w)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(got, w) {
					t.Errorf("%q leaks %q", got, w)
				}
			}
		})
	}
}

func TestEveryCatalogToolHasAPlane(t *testing.T) {
	for _, m := range DefaultCatalog {
		switch m.Audience {
		case "admin", "data", "iam":
		default:
			t.Errorf("%s: audience %q is not a plane", m.Name, m.Audience)
		}
	}
}

func TestToolCallsReportTheExplainedError(t *testing.T) {
	plane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"unauthenticated","message":"jwt: audience mismatch"}`))
	}))
	t.Cleanup(plane.Close)
	cs := dialInProcess(t, mustClients(t)(NewClients(plane.Client(), plane.URL, plane.URL, plane.URL, "tok")))

	res, err := cs.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "paladin_query_objects",
		Arguments: map[string]any{"tenant_id": "acme", "collection": "c"},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := res.Content[0].(*mcpsdk.TextContent).Text
	if !res.IsError || !strings.Contains(text, "paladin-data") {
		t.Errorf("isError=%v text=%q, want the audience explanation", res.IsError, text)
	}
}
