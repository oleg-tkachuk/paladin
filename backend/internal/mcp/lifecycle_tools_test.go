package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/proto"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	adminv1connect "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
)

// lifecyclePlane records the last request each lifecycle RPC received.
type lifecyclePlane struct {
	adminv1connect.UnimplementedBackendServiceHandler
	adminv1connect.UnimplementedBucketServiceHandler
	adminv1connect.UnimplementedCollectionServiceHandler
	adminv1connect.UnimplementedEventSubscriptionServiceHandler

	got proto.Message
}

func (p *lifecyclePlane) SetBackendReadOnly(_ context.Context, r *adminv1.SetBackendReadOnlyRequest) (*adminv1.StorageBackend, error) {
	p.got = r
	return &adminv1.StorageBackend{}, nil
}

func (p *lifecyclePlane) CreateBucket(_ context.Context, r *adminv1.CreateBucketRequest) (*adminv1.Bucket, error) {
	p.got = r
	return &adminv1.Bucket{}, nil
}

func (p *lifecyclePlane) UpdateBucket(_ context.Context, r *adminv1.UpdateBucketRequest) (*adminv1.Bucket, error) {
	p.got = r
	return &adminv1.Bucket{}, nil
}

func (p *lifecyclePlane) ListAccessibleBuckets(_ context.Context, r *adminv1.ListAccessibleBucketsRequest) (*adminv1.ListBucketsResponse, error) {
	p.got = r
	return &adminv1.ListBucketsResponse{}, nil
}

func (p *lifecyclePlane) UpdateCollection(_ context.Context, r *adminv1.UpdateCollectionRequest) (*adminv1.Collection, error) {
	p.got = r
	return &adminv1.Collection{}, nil
}

func (p *lifecyclePlane) RedriveFailedDeliveries(_ context.Context, r *adminv1.RedriveFailedDeliveriesRequest) (*adminv1.RedriveFailedDeliveriesResponse, error) {
	p.got = r
	return &adminv1.RedriveFailedDeliveriesResponse{Requeued: 3}, nil
}

func callLifecycleTool(t *testing.T, plane *lifecyclePlane, tool string, args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()
	server := connect.NewServer()
	adminv1connect.RegisterBackendServiceHandler(server, plane)
	adminv1connect.RegisterBucketServiceHandler(server, plane)
	adminv1connect.RegisterCollectionServiceHandler(server, plane)
	adminv1connect.RegisterEventSubscriptionServiceHandler(server, plane)
	mux := http.NewServeMux()
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

func toolText(res *mcpsdk.CallToolResult) string {
	return res.Content[0].(*mcpsdk.TextContent).Text
}

func TestUpdateBucketSendsOnlyTheNamedFields(t *testing.T) {
	for _, tc := range []struct {
		name string
		args map[string]any
		want []string
	}{
		{"display name", map[string]any{"display_name": "Assets"}, []string{maskDisplayName}},
		{"labels", map[string]any{"labels": map[string]any{"env": "prod"}}, []string{maskLabels}},
		{"both", map[string]any{"display_name": "", "labels": map[string]any{}}, []string{maskDisplayName, maskLabels}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plane := &lifecyclePlane{}
			args := map[string]any{"name": "storageBackends/b/buckets/n", "resource_version": "4"}
			for k, v := range tc.args {
				args[k] = v
			}
			if res := callLifecycleTool(t, plane, "paladin_update_bucket", args); res.IsError {
				t.Fatalf("tool error: %s", toolText(res))
			}
			got := plane.got.(*adminv1.UpdateBucketRequest)
			if !slices.Equal(got.GetUpdateMask().GetPaths(), tc.want) {
				t.Errorf("mask = %v, want %v", got.GetUpdateMask().GetPaths(), tc.want)
			}
			if got.GetResourceVersion() != "4" {
				t.Errorf("resource_version = %q", got.GetResourceVersion())
			}
		})
	}
}

func TestUpdateBucketRefusesAnEmptyChange(t *testing.T) {
	plane := &lifecyclePlane{}
	res := callLifecycleTool(t, plane, "paladin_update_bucket",
		map[string]any{"name": "storageBackends/b/buckets/n", "resource_version": "4"})
	if !res.IsError || plane.got != nil {
		t.Fatalf("an update naming no field reached the plane: %v", plane.got)
	}
}

// The plane drops an owner it cannot parse and creates a shared bucket.
func TestCreateBucketRefusesAnOwnerThatIsNotAUUID(t *testing.T) {
	plane := &lifecyclePlane{}
	res := callLifecycleTool(t, plane, "paladin_create_bucket", map[string]any{
		"backend_id": "primary", "bucket_id": "assets", "owner_tenant_id": "acme",
	})
	if !res.IsError || plane.got != nil {
		t.Fatalf("a bucket for owner %q reached the plane", "acme")
	}
}

func TestCreateBucketNamesTheBackend(t *testing.T) {
	plane := &lifecyclePlane{}
	owner := "0193b9b0-6c0e-7000-8000-000000000001"
	res := callLifecycleTool(t, plane, "paladin_create_bucket", map[string]any{
		"backend_id": "primary", "bucket_id": "assets", "owner_tenant_id": owner,
	})
	if res.IsError {
		t.Fatalf("tool error: %s", toolText(res))
	}
	got := plane.got.(*adminv1.CreateBucketRequest)
	if got.GetParent() != backendNamePrefix+"primary" || got.GetBucketId() != "assets" ||
		got.GetBucket().GetOwnerTenantId() != owner {
		t.Errorf("request = %v", got)
	}
}

// The tool takes a display name only: no Cedar policy rides along.
func TestUpdateCollectionChangesTheDisplayNameOnly(t *testing.T) {
	plane := &lifecyclePlane{}
	res := callLifecycleTool(t, plane, "paladin_update_collection", map[string]any{
		"name": "tenants/acme/collections/docs", "resource_version": "2", "display_name": "Docs",
		"cedar_policy": "permit(principal, action, resource);",
	})
	if res.IsError {
		// The schema refused the stray argument before any call was made.
		if plane.got != nil {
			t.Fatalf("refused, yet the plane got %v", plane.got)
		}
		return
	}
	got := plane.got.(*adminv1.UpdateCollectionRequest)
	if !slices.Equal(got.GetUpdateMask().GetPaths(), []string{maskDisplayName}) ||
		got.GetCollectionResource().GetCedarPolicy() != "" {
		t.Errorf("request = %v, want a display_name change only", got)
	}
}

func TestBackendToggleCarriesTheValue(t *testing.T) {
	plane := &lifecyclePlane{}
	res := callLifecycleTool(t, plane, "paladin_set_backend_read_only", map[string]any{
		"name": "storageBackends/primary", "resource_version": "9", "value": true,
	})
	if res.IsError {
		t.Fatalf("tool error: %s", toolText(res))
	}
	got := plane.got.(*adminv1.SetBackendReadOnlyRequest)
	if !got.GetReadOnly() || got.GetResourceVersion() != "9" || got.GetName() != "storageBackends/primary" {
		t.Errorf("request = %v", got)
	}
}

func TestListAccessibleBucketsNamesTheTenant(t *testing.T) {
	plane := &lifecyclePlane{}
	if res := callLifecycleTool(t, plane, "paladin_list_accessible_buckets", map[string]any{"tenant_id": "acme"}); res.IsError {
		t.Fatalf("tool error: %s", toolText(res))
	}
	if got := plane.got.(*adminv1.ListAccessibleBucketsRequest).GetTenant(); got != tenantNamePrefix+"acme" {
		t.Errorf("tenant = %q", got)
	}
}

func TestRedriveReportsTheRequeuedCount(t *testing.T) {
	plane := &lifecyclePlane{}
	res := callLifecycleTool(t, plane, "paladin_redrive_failed_deliveries",
		map[string]any{"name": "tenants/acme/eventSubscriptions/s1"})
	if res.IsError || !strings.Contains(toolText(res), `"requeued": 3`) {
		t.Fatalf("result = %s", toolText(res))
	}
}

func TestLifecycleDeletesAreDestructive(t *testing.T) {
	cs := dialWithFilter(t, nil)
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"paladin_delete_backend": true, "paladin_delete_bucket": true, "paladin_delete_collection": true}
	for _, tool := range res.Tools {
		if !want[tool.Name] {
			continue
		}
		delete(want, tool.Name)
		if d := tool.Annotations.DestructiveHint; d == nil || !*d {
			t.Errorf("%s destructiveHint = %v, want true", tool.Name, d)
		}
	}
	if len(want) > 0 {
		t.Errorf("not listed: %v", want)
	}
}
