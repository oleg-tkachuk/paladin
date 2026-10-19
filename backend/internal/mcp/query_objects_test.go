package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	datav1connect "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
)

// folderPlane answers ListObjects with one folder and keeps the request.
type folderPlane struct {
	datav1connect.UnimplementedObjectServiceHandler
	got *datav1.ListObjectsRequest
}

func (p *folderPlane) ListObjects(_ context.Context, req *datav1.ListObjectsRequest) (*datav1.ListObjectsResponse, error) {
	p.got = req
	return &datav1.ListObjectsResponse{CommonPrefixes: []string{req.GetPrefix() + "b/"}}, nil
}

// An agent can browse a collection by folder: the prefix and delimiter it
// passes reach the data plane's ListObjects as it passed them.
func TestQueryObjectsPassesTheFolderLevel(t *testing.T) {
	plane := &folderPlane{}
	server := connect.NewServer()
	datav1connect.RegisterObjectServiceHandler(server, plane)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	cs := dialInProcess(t, mustClients(t)(NewClients(ts.Client(), ts.URL, ts.URL, ts.URL, "tok")))

	res, err := cs.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: "paladin_query_objects",
		Arguments: map[string]any{
			"tenant_id": "acme", "collection": "c", "prefix": "a/", "delimiter": "/",
		},
	})
	if err != nil || res.IsError {
		t.Fatalf("call: %v %+v", err, res.Content)
	}
	if plane.got.GetPrefix() != "a/" || plane.got.GetDelimiter() != "/" {
		t.Errorf("data plane got prefix %q delimiter %q", plane.got.GetPrefix(), plane.got.GetDelimiter())
	}
}
