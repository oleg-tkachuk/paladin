package mcp

import "testing"

func TestEveryToolCarriesItsHints(t *testing.T) {
	cs := dialWithFilter(t, nil)
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) == 0 {
		t.Fatal("no tools listed")
	}
	for _, tool := range res.Tools {
		a := tool.Annotations
		if a == nil {
			t.Errorf("%s: no annotations", tool.Name)
			continue
		}
		mutates := catalogMutates[tool.Name]
		if a.ReadOnlyHint == mutates {
			t.Errorf("%s: readOnlyHint=%v, catalog Mutates=%v", tool.Name, a.ReadOnlyHint, mutates)
		}
		if a.OpenWorldHint == nil || *a.OpenWorldHint {
			t.Errorf("%s: openWorldHint unset or true", tool.Name)
		}
		if mutates && a.DestructiveHint == nil {
			t.Errorf("%s: mutating tool without destructiveHint", tool.Name)
		}
	}
}

// Deleting stays destructive: the hand-set hint is not overwritten.
func TestDestructiveToolsStayDestructive(t *testing.T) {
	cs := dialWithFilter(t, nil)
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Name == "paladin_delete_object" {
			if d := tool.Annotations.DestructiveHint; d == nil || !*d {
				t.Errorf("paladin_delete_object destructiveHint = %v, want true", d)
			}
			return
		}
	}
	t.Fatal("paladin_delete_object not listed")
}
