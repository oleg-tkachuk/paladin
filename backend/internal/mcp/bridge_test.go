package mcp

import (
	"context"
	"net/http"
	"sort"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// dialInProcess wires an in-memory MCP client to a server built over the given
// Clients bundle, with the nil filter (every tool registered). Returns a live
// client session the caller drives with ListTools / CallTool.
func dialInProcess(t *testing.T, c *Clients) *mcpsdk.ClientSession {
	t.Helper()
	srv := NewServer("paladin-mcp-test", "v0.0.0", c, nil)
	st, ct := mcpsdk.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// TestServerRegistersDefaultCatalog pins the bridge's core invariant: the set
// of tools an unfiltered NewServer actually registers is exactly the set listed
// in DefaultCatalog. DefaultCatalog is what MCPInspectService reports and what
// profile allow-lists expand against, so any drift between it and the real
// addTool calls would make the inspect view + gating lie about reality.
func TestServerRegistersDefaultCatalog(t *testing.T) {
	t.Parallel()

	cs := dialInProcess(t, NewInlineClients(InlineHandlers{}, "tok"))
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	got := make([]string, 0, len(res.Tools))
	for _, tl := range res.Tools {
		got = append(got, tl.Name)
	}
	want := make([]string, 0, len(DefaultCatalog))
	for _, m := range DefaultCatalog {
		want = append(want, m.Name)
	}
	sort.Strings(got)
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("registered %d tools, DefaultCatalog lists %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("catalog/registration drift at %d: registered=%q catalog=%q\nregistered=%v\ncatalog=%v",
				i, got[i], want[i], got, want)
		}
	}
}

// recordingPlane returns an http.Handler that records every request path and
// answers with a minimal Connect-JSON unary success body, so the bridge's
// Connect client decodes a zero-value response without erroring (see
// inline_test.go for the same stub shape).
func recordingPlane(paths *[]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*paths = append(*paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	})
}

// TestDataMutationToolsDispatch verifies the new destructive data-plane tools
// each dispatch to the correct ObjectService / BatchService RPC. The recording
// handler captures the Connect procedure path; we assert the tool reached the
// intended method (a wrong dispatch — e.g. GetObject instead of DeleteObject —
// would still compile, so the test earns its keep).
func TestDataMutationToolsDispatch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		tool string
		args map[string]any
		want string
	}{
		{
			tool: "paladin_delete_object",
			args: map[string]any{"name": "objects/o1", "permanent": false},
			want: "/paladin.data.v1.ObjectService/DeleteObject",
		},
		{
			tool: "paladin_copy_object",
			args: map[string]any{
				"source_name":            "objects/o1",
				"destination_object_key": "tenants/t1/objectKeys/ok1",
				"destination_key":        "copy/of/o1",
			},
			want: "/paladin.data.v1.ObjectService/CopyObject",
		},
		{
			tool: "paladin_batch_delete",
			args: map[string]any{
				"parent":    "tenants/t1/objectKeys/ok1",
				"names":     []string{"objects/o1", "objects/o2"},
				"permanent": false,
			},
			want: "/paladin.data.v1.BatchService/BatchDeleteObjects",
		},
		{
			tool: "paladin_batch_copy",
			args: map[string]any{
				"source_parent":            "tenants/t1/objectKeys/ok1",
				"names":                    []string{"objects/o1"},
				"destination_object_key":   "tenants/t1/objectKeys/ok2",
				"destination_key_template": "object.key",
			},
			want: "/paladin.data.v1.BatchService/BatchCopyObjects",
		},
		{
			tool: "paladin_batch_restore",
			args: map[string]any{
				"parent": "tenants/t1/objectKeys/ok1",
				"names":  []string{"objects/o1"},
			},
			want: "/paladin.data.v1.BatchService/BatchRestoreObjects",
		},
		{
			tool: "paladin_lookup_object",
			args: map[string]any{
				"parent": "tenants/t1/objectKeys/ok1",
				"key":    "path/to/file.txt",
			},
			want: "/paladin.data.v1.ObjectService/LookupObject",
		},
		{
			tool: "paladin_count_objects",
			args: map[string]any{
				"parent": "tenants/t1/objectKeys/ok1",
				"filter": "state == 'AVAILABLE'",
			},
			want: "/paladin.data.v1.ObjectService/CountObjects",
		},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			var paths []string
			clients := NewInlineClients(InlineHandlers{
				Data: recordingPlane(&paths),
			}, "tok")
			cs := dialInProcess(t, clients)

			_, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
				Name:      tc.tool,
				Arguments: tc.args,
			})
			if err != nil {
				t.Fatalf("CallTool %s: %v", tc.tool, err)
			}
			if len(paths) != 1 {
				t.Fatalf("%s: recorded %d data-plane calls, want 1 (%v)", tc.tool, len(paths), paths)
			}
			if paths[0] != tc.want {
				t.Fatalf("%s: dispatched to %q, want %q", tc.tool, paths[0], tc.want)
			}
		})
	}
}

// TestCoverageToolsDispatch pins the RPC routing of every tool added to close
// the MCP tool-coverage gap (data-plane object/tag/multipart/operation + admin
// audit/system/quota). A wrong client wiring would still compile, so asserting
// the captured Connect procedure path is what makes this test load-bearing.
func TestCoverageToolsDispatch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		tool  string
		plane string // "data" | "admin"
		args  map[string]any
		want  string
	}{
		{"paladin_update_object", "data", map[string]any{"name": "objects/o1", "update_mask": []string{"tags"}, "tags": map[string]any{"k": "v"}}, "/paladin.data.v1.ObjectService/UpdateObject"},
		{"paladin_delete_object_tags", "data", map[string]any{"name": "objects/o1", "keys": []string{"k"}}, "/paladin.data.v1.ObjectTagService/DeleteObjectTags"},
		{"paladin_list_distinct_tags", "data", map[string]any{"parent": "tenants/t1/objectKeys/ok1"}, "/paladin.data.v1.ObjectTagService/ListDistinctTags"},
		{"paladin_batch_update_tags", "data", map[string]any{"parent": "tenants/t1/objectKeys/ok1", "names": []string{"objects/o1"}, "tags": map[string]any{"k": "v"}}, "/paladin.data.v1.BatchService/BatchUpdateTags"},
		{"paladin_regenerate_upload_url", "data", map[string]any{"name": "objects/o1"}, "/paladin.data.v1.PresignService/RegenerateUploadUrl"},
		{"paladin_initiate_multipart_upload", "data", map[string]any{"parent": "tenants/t1/objectKeys/ok1", "content_type": "application/octet-stream"}, "/paladin.data.v1.MultipartUploadService/InitiateMultipartUpload"},
		{"paladin_presign_part", "data", map[string]any{"object_name": "objects/o1", "upload_id": "u1", "part_number": 1}, "/paladin.data.v1.MultipartUploadService/PresignPart"},
		{"paladin_complete_multipart_upload", "data", map[string]any{"object_name": "objects/o1", "upload_id": "u1", "parts": []any{map[string]any{"part_number": 1, "etag": "e1"}}}, "/paladin.data.v1.MultipartUploadService/CompleteMultipartUpload"},
		{"paladin_abort_multipart_upload", "data", map[string]any{"object_name": "objects/o1", "upload_id": "u1"}, "/paladin.data.v1.MultipartUploadService/AbortMultipartUpload"},
		{"paladin_list_parts", "data", map[string]any{"object_name": "objects/o1", "upload_id": "u1"}, "/paladin.data.v1.MultipartUploadService/ListParts"},
		{"paladin_cancel_operation", "data", map[string]any{"name": "operations/op1"}, "/paladin.data.v1.OperationService/CancelOperation"},
		{"paladin_get_audit_entry", "admin", map[string]any{"entry_id": "a1"}, "/paladin.admin.v1.AuditLogService/GetAuditLogEntry"},
		{"paladin_system_config", "admin", map[string]any{}, "/paladin.admin.v1.SystemService/GetConfig"},
		{"paladin_reset_usage", "admin", map[string]any{"name": "tenants/t1"}, "/paladin.admin.v1.QuotaService/ResetUsage"},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			var paths []string
			h := InlineHandlers{}
			switch tc.plane {
			case "admin":
				h.Admin = recordingPlane(&paths)
			default:
				h.Data = recordingPlane(&paths)
			}
			cs := dialInProcess(t, NewInlineClients(h, "tok"))

			if _, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
				Name:      tc.tool,
				Arguments: tc.args,
			}); err != nil {
				t.Fatalf("CallTool %s: %v", tc.tool, err)
			}
			if len(paths) != 1 {
				t.Fatalf("%s: recorded %d %s-plane calls, want 1 (%v)", tc.tool, len(paths), tc.plane, paths)
			}
			if paths[0] != tc.want {
				t.Fatalf("%s: dispatched to %q, want %q", tc.tool, paths[0], tc.want)
			}
		})
	}
}
