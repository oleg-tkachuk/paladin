package postgres

import (
	"reflect"
	"testing"

	"github.com/oleg-tkachuk/limes"
)

// Revocation ids and budgets of two copies: the outer one with a budget of
// its own, the inner one with none.
var (
	outerCopyID     = []byte("outer-copy")
	innerCopyID     = []byte("inner-copy")
	outerCopyBudget = limes.MustParseAmount("2.5")
)

// A reservation's copies survive the row: their ids and budgets, a copy
// with no budget of its own as a NULL element, in order.
func TestCopyBudgetsRoundTripThroughHeldCopies(t *testing.T) {
	copies := []limes.CopyCeiling{
		{RevocationID: outerCopyID, Budget: &limes.BudgetCeiling{Max: outerCopyBudget}},
		{RevocationID: innerCopyID},
	}
	ids, budgets := copyIDs(copies), copyBudgets(copies)
	if len(budgets) != len(copies) || budgets[1] != nil || *budgets[0] != int64(outerCopyBudget) {
		t.Fatalf("budgets = %v, want [%d <nil>]", budgets, outerCopyBudget)
	}
	held, err := heldCopiesOf(ids, budgets)
	if err != nil {
		t.Fatal(err)
	}
	want := []limes.HeldCopy{
		{RevocationID: outerCopyID, Budget: &limes.BudgetCeiling{Max: outerCopyBudget}},
		{RevocationID: innerCopyID},
	}
	if !reflect.DeepEqual(held, want) {
		t.Errorf("held = %+v, want %+v", held, want)
	}
}

// No copies is NULL in the row and none back.
func TestCopyBudgetsWithoutCopies(t *testing.T) {
	if ids, budgets := copyIDs(nil), copyBudgets(nil); ids != nil || budgets != nil {
		t.Errorf("ids = %v, budgets = %v, want both nil so the row stores NULL", ids, budgets)
	}
	held, err := heldCopiesOf(nil, nil)
	if err != nil || held != nil {
		t.Errorf("heldCopiesOf(nil, nil) = %v, %v; want nil, nil", held, err)
	}
}

// A row whose ids and budgets disagree in length is corrupt, never paired up.
func TestHeldCopiesOfRefusesALengthMismatch(t *testing.T) {
	budget := int64(outerCopyBudget)
	for name, tc := range map[string]struct {
		ids     [][]byte
		budgets []*int64
	}{
		"more ids":     {[][]byte{outerCopyID, innerCopyID}, []*int64{&budget}},
		"more budgets": {[][]byte{outerCopyID}, []*int64{&budget, nil}},
		"ids only":     {[][]byte{outerCopyID}, nil},
	} {
		if _, err := heldCopiesOf(tc.ids, tc.budgets); err == nil {
			t.Errorf("%s: a length mismatch was accepted", name)
		}
	}
}
