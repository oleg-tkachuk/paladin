package convx

import (
	"slices"
	"testing"

	commonpb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/batchh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
)

// A state crosses the wire by its OperationState name both ways, so a value
// the console reads from a count is one it can send back as a choice.
func TestOperationFacetsRoundTrip(t *testing.T) {
	t.Parallel()
	for domain, wire := range operationStates {
		got := OperationFacetsFromProto(&commonpb.OperationFacets{States: []commonpb.OperationState{wire}})
		if !slices.Equal(got.States, []operationh.State{domain}) {
			t.Errorf("%s from the wire = %v, want %s", wire, got.States, domain)
		}
		counts := FacetCountsToProto([]operationh.FacetCount{{Facet: operationh.FacetState, Value: string(domain), Count: 1}})
		if counts[0].GetValue() != wire.String() {
			t.Errorf("%s counted as %q, want %q", domain, counts[0].GetValue(), wire.String())
		}
	}
}

func TestOperationFacetsFromProto(t *testing.T) {
	t.Parallel()
	if got := OperationFacetsFromProto(nil); got.States != nil || got.Types != nil {
		t.Errorf("no facets = %+v, want none chosen", got)
	}
	got := OperationFacetsFromProto(&commonpb.OperationFacets{
		States: []commonpb.OperationState{commonpb.OperationState_OPERATION_STATE_UNSPECIFIED},
		Types:  []string{batchh.OpBatchCopy},
	})
	if len(got.States) != 0 || !slices.Equal(got.Types, []string{batchh.OpBatchCopy}) {
		t.Errorf("got %+v; an unknown state is left out, types pass through", got)
	}
}

// A type's value is its own name; only a state is renamed.
func TestFacetCountsToProtoKeepsTypes(t *testing.T) {
	t.Parallel()
	const typ, n = batchh.OpBatchCopy, 7
	got := FacetCountsToProto([]operationh.FacetCount{{Facet: operationh.FacetType, Value: typ, Count: n}})
	if len(got) != 1 || got[0].GetFacet() != operationh.FacetType || got[0].GetValue() != typ || got[0].GetCount() != n {
		t.Errorf("got %v", got)
	}
	if got := FacetCountsToProto(nil); got == nil || len(got) != 0 {
		t.Errorf("no counts = %#v, want an empty list", got)
	}
}
