package apiutil

import (
	"fmt"

	"connectrpc.com/connect/v2"
	"google.golang.org/genproto/googleapis/type/money"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// Every budget ceiling on the wire is a budget_ceiling oneof: a Money limit,
// where a zero amount allows no spend, or an UnlimitedBudget. These helpers
// read and write its two arms, so no RPC reads a 0 as "no limit" again.

// BudgetCeilingOf reads a budget_ceiling oneof from its arms, of which a oneof
// sets at most one; set is false when it sets neither. unit is the arm's
// currency, normalized, and "" when the arm names none: what that means is
// the caller's to decide. The ceiling carries unit as its UnitCode. A
// malformed arm is InvalidArgument naming field.
func BudgetCeilingOf(field string, limit *money.Money, unlimited *adminv1.UnlimitedBudget) (ceiling limes.BudgetCeiling, unit string, set bool, err error) {
	switch {
	case limit != nil:
		n, unit, err := NanosOf(field+".max_budget", limit)
		if err != nil {
			return limes.BudgetCeiling{}, "", false, err
		}
		return limes.BudgetCeiling{Max: n, UnitCode: unit}, unit, true, nil
	case unlimited != nil:
		if code := unlimited.GetCurrencyCode(); code != "" {
			normalized, err := NormalizeUnitCode(code)
			if err != nil {
				return limes.BudgetCeiling{}, "", false,
					rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("%s.unlimited_budget.currency_code: %w", field, err))
			}
			unit = normalized
		}
		return limes.BudgetCeiling{Unlimited: true, UnitCode: unit}, unit, true, nil
	default:
		return limes.BudgetCeiling{}, "", false, nil
	}
}

// BudgetCeilingArms is a ceiling in unit as a budget_ceiling oneof's arms:
// exactly one is non-nil. An empty unit is the ceiling's own UnitCode, and a
// ceiling in none is NoUnit.
func BudgetCeilingArms(c limes.BudgetCeiling, unit string) (*money.Money, *adminv1.UnlimitedBudget) {
	unit = unitOrNone(unit, c.UnitCode)
	if c.Unlimited {
		return nil, &adminv1.UnlimitedBudget{CurrencyCode: unit}
	}
	return MoneyOf(unit, c.Max), nil
}
