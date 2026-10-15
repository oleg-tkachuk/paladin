package adapters

import (
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
)

// The state literal reaches search_object_ids as text, verbatim: the function
// (078) decides whether the enum holds it, so the adapter must neither drop
// nor normalise it.
func TestHintsFromPassesStateVerbatim(t *testing.T) {
	for _, state := range []string{"AVAILABLE", "available", "NOT_A_STATE"} {
		h := hintsFrom(cel.ObjectPushdown{StateEq: state})
		if h.state == nil || *h.state != state {
			t.Errorf("hintsFrom(%q).state = %v, want %q", state, h.state, state)
		}
	}
	if h := hintsFrom(cel.ObjectPushdown{}); h.state != nil {
		t.Errorf("no state predicate became %q", *h.state)
	}
}
