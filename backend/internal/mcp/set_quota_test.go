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

// The tool sends the version it read, and only the caps it was given: the
// plane changes exactly what the update mask names, so caps left out keep
// their values without being sent back.
func TestSetQuotaChangesOnlyTheNamedCaps(t *testing.T) {
	plane := &quotaPlane{current: &adminv1.Quota{
		MaxTotalBytes: new(int64(100)), MaxObjectCount: new(int64(10)), MaxBytesPerDay: new(int64(5)), MaxObjectsPerDay: new(int64(1)),
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
	if !slices.Equal(got.GetUpdateMask().GetPaths(), []string{quotaCapObjectCount}) {
		t.Errorf("mask = %v", got.GetUpdateMask().GetPaths())
	}
	if q := got.GetQuota(); q.MaxObjectCount == nil || *q.MaxObjectCount != 20 {
		t.Errorf("quota sent = %+v, want max_object_count 20", q)
	}
}

func TestSetQuotaCreatesWhenNoneExists(t *testing.T) {
	plane := &quotaPlane{}
	callSetQuota(t, plane, map[string]any{"name": "tenants/acme/quota", "max_total_bytes": 0})
	if plane.set.GetResourceVersion() != quotaNoRowVersion {
		t.Errorf("resource_version = %q, want %q", plane.set.GetResourceVersion(), quotaNoRowVersion)
	}
	// 0 is a cap — one that allows nothing — not "left out".
	if !slices.Equal(plane.set.GetUpdateMask().GetPaths(), []string{quotaCapTotalBytes}) {
		t.Errorf("mask = %v", plane.set.GetUpdateMask().GetPaths())
	}
	if q := plane.set.GetQuota(); q.MaxTotalBytes == nil || *q.MaxTotalBytes != 0 {
		t.Errorf("max_total_bytes sent = %v, want a cap of 0", q.MaxTotalBytes)
	}
}

// Lifting a cap masks it and leaves it absent; there is no number for "none".
func TestSetQuotaLiftsACap(t *testing.T) {
	plane := &quotaPlane{}
	res := callSetQuota(t, plane, map[string]any{
		"name": "tenants/acme/quota", "max_total_bytes": 10, "lift": []string{quotaCapObjectsPerDay},
	})
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content[0].(*mcpsdk.TextContent).Text)
	}
	if !slices.Equal(plane.set.GetUpdateMask().GetPaths(), []string{quotaCapTotalBytes, quotaCapObjectsPerDay}) {
		t.Errorf("mask = %v", plane.set.GetUpdateMask().GetPaths())
	}
	if plane.set.GetQuota().MaxObjectsPerDay != nil {
		t.Errorf("a lifted cap was sent with a value: %v", *plane.set.GetQuota().MaxObjectsPerDay)
	}
}

func TestQuotaChangeRefusesAContradiction(t *testing.T) {
	ten := int64(10)
	for name, in := range map[string]setQuotaArgs{
		"set and lifted": {MaxTotalBytes: &ten, Lift: []string{quotaCapTotalBytes}},
		"not a cap":      {Lift: []string{"usage"}},
		"nothing":        {},
	} {
		if _, _, err := quotaChange(in); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestSetQuotaRefusesAnEmptyChange(t *testing.T) {
	plane := &quotaPlane{}
	if res := callSetQuota(t, plane, map[string]any{"name": "tenants/acme/quota"}); !res.IsError || plane.set != nil {
		t.Errorf("isError=%v set=%v, want a refusal and no write", res.IsError, plane.set)
	}
}
