package apiutil

import (
	"testing"

	"connectrpc.com/connect/v2"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/limes"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

const ceilingField = "budget_ceiling"

func TestBudgetCeilingOf(t *testing.T) {
	for name, tc := range map[string]struct {
		limit     *money.Money
		unlimited *adminv1.UnlimitedBudget
		ceiling   limes.BudgetCeiling
		unit      string
		set       bool
	}{
		"neither":             {},
		"zero allows nothing": {limit: &money.Money{CurrencyCode: "EUR"}, ceiling: limes.BudgetCeiling{UnitCode: "EUR"}, unit: "EUR", set: true},
		"a limit": {
			limit:   &money.Money{CurrencyCode: "USD", Units: 2},
			ceiling: limes.BudgetCeiling{Max: 2 * limes.NanosPerUnit, UnitCode: "USD"}, unit: "USD", set: true,
		},
		"unlimited in a unit": {
			unlimited: &adminv1.UnlimitedBudget{CurrencyCode: "UAH"},
			ceiling:   limes.BudgetCeiling{Unlimited: true, UnitCode: "UAH"}, unit: "UAH", set: true,
		},
		"unlimited, no unit": {
			unlimited: &adminv1.UnlimitedBudget{},
			ceiling:   limes.BudgetCeiling{Unlimited: true}, set: true,
		},
	} {
		c, unit, set, err := BudgetCeilingOf(ceilingField, tc.limit, tc.unlimited)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if c != tc.ceiling || unit != tc.unit || set != tc.set {
			t.Errorf("%s: = %s %q %v, want %s %q %v", name, c, unit, set, tc.ceiling, tc.unit, tc.set)
		}
	}
}

func TestBudgetCeilingOfRefusesMalformedArms(t *testing.T) {
	for name, tc := range map[string]struct {
		limit     *money.Money
		unlimited *adminv1.UnlimitedBudget
	}{
		"a negative limit":      {limit: &money.Money{CurrencyCode: "USD", Units: -1}},
		"an unknown limit unit": {limit: &money.Money{CurrencyCode: "XYZ", Units: 1}},
		"an unknown open unit":  {unlimited: &adminv1.UnlimitedBudget{CurrencyCode: "XYZ"}},
	} {
		if _, _, _, err := BudgetCeilingOf(ceilingField, tc.limit, tc.unlimited); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: err = %v, want InvalidArgument", name, err)
		}
	}
}

// Each ceiling writes back on its own arm: in the unit given, else in the
// ceiling's own, else in NoUnit.
func TestBudgetCeilingArms(t *testing.T) {
	limit, open := BudgetCeilingArms(limes.BudgetCeiling{UnitCode: "USD"}, "EUR")
	if open != nil || !proto.Equal(limit, &money.Money{CurrencyCode: "EUR"}) {
		t.Errorf("zero: %v / %v, want a limit of 0 EUR", limit, open)
	}
	limit, open = BudgetCeilingArms(limes.BudgetCeiling{Unlimited: true, UnitCode: "UAH"}, "")
	if limit != nil || open.GetCurrencyCode() != "UAH" {
		t.Errorf("unlimited: %v / %v, want unlimited in the ceiling's UAH", limit, open)
	}
	limit, open = BudgetCeilingArms(limes.BudgetCeiling{Unlimited: true}, "")
	if limit != nil || open.GetCurrencyCode() != NoUnit {
		t.Errorf("unlimited in no unit: %v / %v, want unlimited in %s", limit, open, NoUnit)
	}
}
