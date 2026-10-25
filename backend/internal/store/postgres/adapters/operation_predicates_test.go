package adapters

import (
	"fmt"
	"slices"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/batchh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
)

func TestWithin(t *testing.T) {
	for _, tc := range []struct {
		name    string
		a, b    []string
		want    []string
		wantNil bool
	}{
		{name: "both any", wantNil: true},
		{name: "a any", b: []string{"x"}, want: []string{"x"}},
		{name: "b any", a: []string{"x"}, want: []string{"x"}},
		{name: "overlap", a: []string{"x", "y"}, b: []string{"y", "z"}, want: []string{"y"}},
		{name: "disjoint matches nothing", a: []string{"x"}, b: []string{"z"}, want: []string{}},
	} {
		got := within(tc.a, tc.b)
		if tc.wantNil != (got == nil) || !slices.Equal(got, tc.want) {
			t.Errorf("%s: within(%v, %v) = %#v, want %#v", tc.name, tc.a, tc.b, got, tc.want)
		}
	}
}

// The facets, the typed state and the filter's hints all narrow the same
// columns; each is a constraint, so the query gets what all of them allow.
func TestPredicatesOfNarrowsByEveryConstraint(t *testing.T) {
	failed, running := operationh.StateFailed, operationh.StateRunning
	p := predicatesOf(operationh.ListArgs{
		Filter: fmt.Sprintf(`type in [%q, %q] && state in [%q, %q]`, batchh.OpBatchDelete, batchh.OpBatchCopy, failed, running),
		State:  &failed,
		Facets: operationh.Facets{States: []operationh.State{failed, running}, Types: []string{batchh.OpBatchCopy, batchh.OpBatchUpdateTags}},
	})
	if !slices.Equal(p.stateIn, []string{string(failed)}) {
		t.Errorf("stateIn = %v, want only %s", p.stateIn, failed)
	}
	if !slices.Equal(p.typeIn, []string{batchh.OpBatchCopy}) {
		t.Errorf("typeIn = %v, want only %s", p.typeIn, batchh.OpBatchCopy)
	}
	if p := predicatesOf(operationh.ListArgs{}); p.stateIn != nil || p.typeIn != nil {
		t.Errorf("no constraint narrowed state %v, type %v", p.stateIn, p.typeIn)
	}
}
