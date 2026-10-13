package capabilityh

import (
	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// A capability's request and budget ceilings are explicit on the wire, as in
// limes: a limit, where 0 allows nothing, or unlimited. Caveats that set
// neither on an axis are refused on Issue, which has nothing to fall back
// on, and keep the parent's ceiling on Delegate.

// The oneofs' names, for the refusal that names what is missing.
const (
	fieldCaveats        = "caveats"
	fieldRequestCeiling = fieldCaveats + ".request_ceiling"
	fieldBudgetCeiling  = fieldCaveats + ".budget_ceiling"
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
// A budget that names no currency comes back with an empty unit; the caller
// decides what it means (protoToCaveats).
func budgetCeilingOf(c *adminv1.CapabilityCaveats) (budgetCeiling, bool, error) {
	ceiling, unit, set, err := apiutil.BudgetCeilingOf(fieldCaveats, c.GetMaxBudget(), c.GetUnlimitedBudget())
	if err != nil || !set {
		return budgetCeiling{}, false, err
	}
	return budgetCeiling{ceiling: ceiling, unit: unit}, true, nil
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
	limit, unlimited := apiutil.BudgetCeilingArms(b, unit)
	if unlimited != nil {
		c.BudgetCeiling = &adminv1.CapabilityCaveats_UnlimitedBudget{UnlimitedBudget: unlimited}
		return
	}
	c.BudgetCeiling = &adminv1.CapabilityCaveats_MaxBudget{MaxBudget: limit}
}
