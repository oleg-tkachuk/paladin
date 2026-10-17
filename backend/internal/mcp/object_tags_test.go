package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/oleg-tkachuk/paladin/backend/internal/middleware"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	datav1connect "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
)

// tagPlane is the data plane's tag service behind the plane's own request
// validation, so a request the real plane refuses is refused here too.
type tagPlane struct {
	datav1connect.UnimplementedObjectTagServiceHandler
	put *datav1.PutObjectTagsRequest
	del *datav1.DeleteObjectTagsRequest
}

func (p *tagPlane) PutObjectTags(_ context.Context, r *datav1.PutObjectTagsRequest) (*datav1.PutObjectTagsResponse, error) {
	p.put = r
	return &datav1.PutObjectTagsResponse{}, nil
}

func (p *tagPlane) DeleteObjectTags(_ context.Context, r *datav1.DeleteObjectTagsRequest) (*datav1.DeleteObjectTagsResponse, error) {
	p.del = r
	return &datav1.DeleteObjectTagsResponse{}, nil
}

func callTagTool(t *testing.T, plane *tagPlane, tool string, args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()
	validate, err := middleware.ProtoValidate()
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	server := connect.NewServer(validate)
	datav1connect.RegisterObjectTagServiceHandler(server, plane)
	connecthttp.Mount(mux, server)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	cs := dialInProcess(t, mustClients(t)(NewClients(ts.Client(), ts.URL, ts.URL, ts.URL, "tok")))
	res, err := cs.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

const (
	testObjectName    = "tenants/acme/collections/docs/objects/0199"
	testObjectVersion = "7"
)

// Each tag write carries the object's version: the plane requires one, and
// refuses the call before any handler runs without it. paladin_set_object_tags
// sent none, so every call it made failed.
func TestTagToolsSendTheResourceVersion(t *testing.T) {
	plane := &tagPlane{}
	for tool, args := range map[string]map[string]any{
		"paladin_set_object_tags": {
			"name": testObjectName, "resource_version": testObjectVersion, "tags": map[string]string{"k": "v"},
		},
		"paladin_delete_object_tags": {
			"name": testObjectName, "resource_version": testObjectVersion, "keys": []string{"k"},
		},
	} {
		if res := callTagTool(t, plane, tool, args); res.IsError {
			t.Fatalf("%s: %v", tool, res.Content[0].(*mcpsdk.TextContent).Text)
		}
	}
	if got := plane.put.GetResourceVersion(); got != testObjectVersion {
		t.Errorf("set_object_tags sent resource_version %q, want %q", got, testObjectVersion)
	}
	if got := plane.put.GetTags()["k"]; got != "v" {
		t.Errorf("set_object_tags sent tags %v", plane.put.GetTags())
	}
	if got := plane.del.GetResourceVersion(); got != testObjectVersion {
		t.Errorf("delete_object_tags sent resource_version %q, want %q", got, testObjectVersion)
	}
}

// Without the version the plane refuses, and the agent is told why.
func TestSetObjectTagsWithoutAVersionIsRefused(t *testing.T) {
	plane := &tagPlane{}
	res := callTagTool(t, plane, "paladin_set_object_tags", map[string]any{
		"name": testObjectName, "tags": map[string]string{},
	})
	if !res.IsError {
		t.Fatal("a call without resource_version succeeded")
	}
	if plane.put != nil {
		t.Error("the request reached the handler")
	}
}
