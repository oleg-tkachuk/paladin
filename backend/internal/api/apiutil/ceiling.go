package apiutil

import "github.com/oleg-tkachuk/limes"

// A tenant's budget is the one ceiling the API still spells as a number where
// 0 sets none (TenantBudgetService, BillingService). limes spells "none" as
// Unlimited and reads 0 as a ceiling of nothing. These helpers are the only
// place the two meet, so a 0 from a client never freezes a tenant it meant
// to leave open. A capability's ceilings are explicit on the wire instead.

// BudgetCeilingOf is the tenant budget ceiling an API amount sets: 0 none.
func BudgetCeilingOf(n limes.Nanos) limes.BudgetCeiling {
	if n == 0 {
		return limes.BudgetCeiling{Unlimited: true}
	}
	return limes.BudgetCeiling{Max: n}
}

// BudgetCeilingAmount is a tenant budget ceiling as the API reports it: 0 for
// none. A ceiling of nothing, which the API cannot set, reads as 0 too.
func BudgetCeilingAmount(c limes.BudgetCeiling) limes.Nanos {
	if c.Unlimited {
		return 0
	}
	return c.Max
}
