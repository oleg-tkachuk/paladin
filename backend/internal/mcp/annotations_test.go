package mcp

import (
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// listedTools lists the full catalog the way a client sees it.
func listedTools(t *testing.T) map[string]*mcpsdk.Tool {
	t.Helper()
	cs := dialWithFilter(t, nil)
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) == 0 {
		t.Fatal("no tools listed")
	}
	out := make(map[string]*mcpsdk.Tool, len(res.Tools))
	for _, tool := range res.Tools {
		out[tool.Name] = tool
	}
	return out
}

func TestEveryToolCarriesItsCatalogHints(t *testing.T) {
	for name, tool := range listedTools(t) {
		a, meta := tool.Annotations, catalogHints[name]
		if a == nil {
			t.Errorf("%s: no annotations", name)
			continue
		}
		if a.ReadOnlyHint == meta.Mutates {
			t.Errorf("%s: readOnlyHint=%v, catalog Mutates=%v", name, a.ReadOnlyHint, meta.Mutates)
		}
		if a.OpenWorldHint == nil || *a.OpenWorldHint != meta.OpenWorld {
			t.Errorf("%s: openWorldHint=%v, catalog OpenWorld=%v", name, a.OpenWorldHint, meta.OpenWorld)
		}
		if meta.Mutates && (a.DestructiveHint == nil || *a.DestructiveHint != meta.Destructive) {
			t.Errorf("%s: destructiveHint=%v, catalog Destructive=%v", name, a.DestructiveHint, meta.Destructive)
		}
		if !meta.Mutates && !a.IdempotentHint {
			t.Errorf("%s: a read is not marked idempotent", name)
		}
	}
}

// Only a tool that changes something can destroy anything.
func TestOnlyMutatingToolsAreDestructive(t *testing.T) {
	for _, m := range DefaultCatalog {
		if m.Destructive && !m.Mutates {
			t.Errorf("%s: Destructive without Mutates", m.Name)
		}
	}
}

// The hints a client confirms by, for the tools where a wrong one costs data
// or reaches outside the platform. Each was wrong once: deleting tags and
// aborting an upload read as additive, and a test delivery to a real sink
// read as a read that stays inside Paladin.
func TestHintsThatDecideAConfirmation(t *testing.T) {
	tools := listedTools(t)
	for _, tc := range []struct {
		name                             string
		readOnly, destructive, openWorld bool
	}{
		{"paladin_delete_object_tags", false, true, false},
		{"paladin_abort_multipart_upload", false, true, false},
		{"paladin_set_backend_read_only", false, true, false},
		{"paladin_restore_version", false, true, false},
		{"paladin_delete_object", false, true, false},
		{"paladin_test_subscription", false, false, true},
		{"paladin_test_backend", false, false, false},
		{"paladin_upload_object", false, false, false},
		{"paladin_list_backends", true, false, false},
	} {
		tool, ok := tools[tc.name]
		if !ok {
			t.Errorf("%s not listed", tc.name)
			continue
		}
		a := tool.Annotations
		destructive := a.DestructiveHint != nil && *a.DestructiveHint
		if a.ReadOnlyHint != tc.readOnly || destructive != tc.destructive || *a.OpenWorldHint != tc.openWorld {
			t.Errorf("%s: readOnly=%v destructive=%v openWorld=%v, want %v %v %v",
				tc.name, a.ReadOnlyHint, destructive, *a.OpenWorldHint, tc.readOnly, tc.destructive, tc.openWorld)
		}
	}
}

// Registrations once shared one annotations value, which annotate then
// changed in place, so one tool's hints could become another's.
func TestAnnotateGivesEachToolItsOwnHints(t *testing.T) {
	shared := &mcpsdk.ToolAnnotations{}
	del := &mcpsdk.Tool{Name: "paladin_delete_object", Annotations: shared}
	list := &mcpsdk.Tool{Name: "paladin_list_backends", Annotations: shared}
	annotate(del)
	annotate(list)
	if del.Annotations == list.Annotations {
		t.Fatal("two tools share one annotations value")
	}
	if del.Annotations.ReadOnlyHint || !list.Annotations.ReadOnlyHint {
		t.Errorf("readOnlyHint: delete=%v list=%v", del.Annotations.ReadOnlyHint, list.Annotations.ReadOnlyHint)
	}
}
