package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// The version and next page historyPlane answers with.
const (
	historyVersion  int32 = 3
	historyWriter         = "agent:ci"
	historyNextPage       = "v2"
)

// historyPlane answers ListPolicyVersions with one version and keeps the
// request.
type historyPlane struct {
	paladinadminv1connect.UnimplementedPolicyServiceHandler
	got *adminv1.ListPolicyVersionsRequest
}

func (p *historyPlane) ListPolicyVersions(_ context.Context, req *adminv1.ListPolicyVersionsRequest) (*adminv1.ListPolicyVersionsResponse, error) {
	p.got = req
	writer := historyWriter
	return &adminv1.ListPolicyVersionsResponse{
		Versions: []*adminv1.PolicyVersion{{Version: historyVersion, CedarPolicy: "permit (principal, action, resource);", WrittenBy: &writer}},
		Page:     &commonv1.PageResponse{NextPageToken: historyNextPage},
	}, nil
}

// An agent reads a layer's history page by page: the layer and the page it
// asks for reach PolicyService as it asked, and the versions come back with
// their writers and the cursor to the next page.
func TestListPolicyVersionsReadsTheLayersHistory(t *testing.T) {
	plane := &historyPlane{}
	server := connect.NewServer()
	paladinadminv1connect.RegisterPolicyServiceHandler(server, plane)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	cs := dialInProcess(t, mustClients(t)(NewClients(ts.Client(), ts.URL, ts.URL, ts.URL, "tok")))

	const (
		layer    = "tenants/acme/collections/docs"
		pageSize = 10
		cursor   = "v4"
	)
	res, err := cs.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: "paladin_list_policy_versions",
		Arguments: map[string]any{
			"resource_name": layer, "page_size": pageSize, "page_token": cursor,
		},
	})
	if err != nil || res.IsError {
		t.Fatalf("call: %v %+v", err, res.Content)
	}
	if plane.got.GetResourceName() != layer || plane.got.GetPage().GetPageSize() != pageSize || plane.got.GetPage().GetPageToken() != cursor {
		t.Errorf("PolicyService got %+v", plane.got)
	}
	text, ok := res.Content[0].(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("content = %T, want text", res.Content[0])
	}
	var got struct {
		Versions []struct {
			Version   int32  `json:"version"`
			WrittenBy string `json:"written_by"`
		} `json:"versions"`
		Page struct {
			NextPageToken string `json:"next_page_token"`
		} `json:"page"`
	}
	if err := json.Unmarshal([]byte(text.Text), &got); err != nil {
		t.Fatalf("result %q: %v", text.Text, err)
	}
	if len(got.Versions) != 1 || got.Versions[0].Version != historyVersion || got.Versions[0].WrittenBy != historyWriter || got.Page.NextPageToken != historyNextPage {
		t.Errorf("result = %+v", got)
	}
}
