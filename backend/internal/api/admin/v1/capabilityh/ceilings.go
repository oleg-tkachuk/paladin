package capabilityh

import (
	"fmt"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// A capability's request and budget ceilings are explicit on the wire, as in
// limes: a limit, where 0 allows nothing, or unlimited. Caveats that set
// neither on an axis are refused on Issue, which has nothing to fall back
// on, and keep the parent's ceiling on Delegate.

// The oneofs' names, for the refusal that names what is missing.
const (
	fieldRequestCeiling = "caveats.request_ceiling"
	fieldBudgetCeiling  = "caveats.budget_ceiling"
	fieldMaxBudget      = "caveats.max_budget"
	fieldUnlimitedUnit  = "caveats.unlimited_budget.currency_code"
)

// budgetCeiling is a budget ceiling and the unit it, and every charge
// against it, is in.
type budgetCeiling struct {
	ceiling limes.BudgetCeiling
	unit    string
}

// requestCeilingOf is the request ceiling c sets, and false when it sets none.
func requestCeilingOf(c *adminv1.CapabilityCaveats) (limes.RequestCeiling, bool) {
	switch r := c.GetRequestCeiling().(type) {
	case *adminv1.CapabilityCaveats_MaxRequests:
		return limes.RequestCeiling{Max: r.MaxRequests}, true
	case *adminv1.CapabilityCaveats_UnlimitedRequests:
		return limes.RequestCeiling{Unlimited: true}, true
	default:
		return limes.RequestCeiling{}, false
	}
}

// budgetCeilingOf is the budget ceiling c sets, and false when it sets none.
// An unlimited budget with no currency is in limes.DefaultUnitCode.
func budgetCeilingOf(c *adminv1.CapabilityCaveats) (budgetCeiling, bool, error) {
	switch b := c.GetBudgetCeiling().(type) {
	case *adminv1.CapabilityCaveats_MaxBudget:
		n, unit, err := apiutil.NanosOf(fieldMaxBudget, b.MaxBudget)
		if err != nil {
			return budgetCeiling{}, false, err
		}
		if unit == "" {
			unit = limes.DefaultUnitCode
		}
		return budgetCeiling{ceiling: limes.BudgetCeiling{Max: n}, unit: unit}, true, nil
	case *adminv1.CapabilityCaveats_UnlimitedBudget:
		unit := limes.DefaultUnitCode
		if code := b.UnlimitedBudget.GetCurrencyCode(); code != "" {
			normalized, err := limes.NormalizeUnitCode(code)
			if err != nil {
				return budgetCeiling{}, false, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("%s: %w", fieldUnlimitedUnit, err))
			}
			unit = normalized
		}
		return budgetCeiling{ceiling: limes.BudgetCeiling{Unlimited: true}, unit: unit}, true, nil
	default:
		return budgetCeiling{}, false, nil
	}
}

// requireCeilings refuses caveats that leave a ceiling unset, where there is
// no parent whose ceiling it could keep.
func requireCeilings(c *adminv1.CapabilityCaveats) error {
	if c.GetRequestCeiling() == nil {
		return connect.Errorf(connect.CodeInvalidArgument, "%s: set max_requests, or unlimited_requests for no limit", fieldRequestCeiling)
	}
	if c.GetBudgetCeiling() == nil {
		return connect.Errorf(connect.CodeInvalidArgument, "%s: set max_budget, or unlimited_budget for no limit", fieldBudgetCeiling)
	}
	return nil
}

// setRequestCeiling writes a request ceiling onto c.
func setRequestCeiling(c *adminv1.CapabilityCaveats, r limes.RequestCeiling) {
	if r.Unlimited {
		c.RequestCeiling = &adminv1.CapabilityCaveats_UnlimitedRequests{UnlimitedRequests: true}
		return
	}
	c.RequestCeiling = &adminv1.CapabilityCaveats_MaxRequests{MaxRequests: r.Max}
}

// setBudgetCeiling writes a budget ceiling in unit onto c.
func setBudgetCeiling(c *adminv1.CapabilityCaveats, b limes.BudgetCeiling, unit string) {
	if b.Unlimited {
		if unit == "" {
			unit = limes.DefaultUnitCode
		}
		c.BudgetCeiling = &adminv1.CapabilityCaveats_UnlimitedBudget{
			UnlimitedBudget: &adminv1.CapabilityUnlimitedBudget{CurrencyCode: unit},
		}
		return
	}
	c.BudgetCeiling = &adminv1.CapabilityCaveats_MaxBudget{MaxBudget: apiutil.MoneyOf(unit, b.Max)}
}
