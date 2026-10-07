package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	adminv1connect "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
)

type quotaPlane struct {
	adminv1connect.UnimplementedQuotaServiceHandler
	current *adminv1.Quota // nil = no quota yet
	set     *adminv1.SetQuotaRequest
}

func (p *quotaPlane) GetQuota(context.Context, *adminv1.GetQuotaRequest) (*adminv1.Quota, error) {
	if p.current == nil {
		return nil, connect.NewError(connect.CodeNotFound, "")
	}
	return p.current, nil
}

func (p *quotaPlane) SetQuota(_ context.Context, r *adminv1.SetQuotaRequest) (*adminv1.Quota, error) {
	p.set = r
	return r.GetQuota(), nil
}

func callSetQuota(t *testing.T, plane *quotaPlane, args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()
	mux := http.NewServeMux()
	server := connect.NewServer()
	adminv1connect.RegisterQuotaServiceHandler(server, plane)
	connecthttp.Mount(mux, server)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	cs := dialInProcess(t, mustClients(t)(NewClients(ts.Client(), ts.URL, ts.URL, ts.URL, "tok")))
	res, err := cs.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: "paladin_set_quota", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// The tool sent no resource_version and no update_mask, both required, so it
// never succeeded; and the plane writes all four caps, so a partial set would
// have zeroed the caps left out.
func TestSetQuotaChangesOnlyTheNamedCaps(t *testing.T) {
	plane := &quotaPlane{current: &adminv1.Quota{
		MaxTotalBytes: 100, MaxObjectCount: 10, MaxBytesPerDay: 5, MaxObjectsPerDay: 1,
		ResourceVersion: "7",
	}}
	res := callSetQuota(t, plane, map[string]any{"name": "tenants/acme/quota", "max_object_count": 20})
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content[0].(*mcpsdk.TextContent).Text)
	}
	got := plane.set
	if got.GetResourceVersion() != "7" {
		t.Errorf("resource_version = %q, want the one read", got.GetResourceVersion())
	}
	if !slices.Equal(got.GetUpdateMask().GetPaths(), []string{"max_object_count"}) {
		t.Errorf("mask = %v", got.GetUpdateMask().GetPaths())
	}
	q := got.GetQuota()
	if q.MaxObjectCount != 20 || q.MaxTotalBytes != 100 || q.MaxBytesPerDay != 5 || q.MaxObjectsPerDay != 1 {
		t.Errorf("quota sent = %+v, want only max_object_count changed", q)
	}
}

func TestSetQuotaCreatesWhenNoneExists(t *testing.T) {
	plane := &quotaPlane{}
	callSetQuota(t, plane, map[string]any{"name": "tenants/acme/quota", "max_total_bytes": 0})
	if plane.set.GetResourceVersion() != quotaNoRowVersion {
		t.Errorf("resource_version = %q, want %q", plane.set.GetResourceVersion(), quotaNoRowVersion)
	}
	// 0 is a cap (unlimited), not "left out".
	if !slices.Equal(plane.set.GetUpdateMask().GetPaths(), []string{"max_total_bytes"}) {
		t.Errorf("mask = %v", plane.set.GetUpdateMask().GetPaths())
	}
}

func TestSetQuotaRefusesAnEmptyChange(t *testing.T) {
	plane := &quotaPlane{}
	if res := callSetQuota(t, plane, map[string]any{"name": "tenants/acme/quota"}); !res.IsError || plane.set != nil {
		t.Errorf("isError=%v set=%v, want a refusal and no write", res.IsError, plane.set)
	}
}
