package apiutil

import "github.com/oleg-tkachuk/limes"

// The API spells a ceiling as a number where 0 sets none: max_requests and
// max_budget on a capability, max_budget on a tenant. limes spells "none" as
// Unlimited and reads 0 as a ceiling of nothing. These helpers are the only
// place the two meet, so a 0 from a client never freezes what it meant to
// leave open. A ceiling of nothing is not reachable through the API.

// RequestCeilingOf is the request ceiling an API number sets: 0 none.
func RequestCeilingOf(n int64) limes.RequestCeiling {
	if n == 0 {
		return limes.RequestCeiling{Unlimited: true}
	}
	return limes.RequestCeiling{Max: n}
}

// BudgetCeilingOf is the budget ceiling an API amount sets: 0 none.
func BudgetCeilingOf(n limes.Nanos) limes.BudgetCeiling {
	if n == 0 {
		return limes.BudgetCeiling{Unlimited: true}
	}
	return limes.BudgetCeiling{Max: n}
}

// RequestCeilingNumber is a request ceiling as the API reports it: 0 for none.
// A ceiling of nothing, which the API cannot set, reads as 0 too.
func RequestCeilingNumber(c limes.RequestCeiling) int64 {
	if c.Unlimited {
		return 0
	}
	return c.Max
}

// BudgetCeilingAmount is a budget ceiling as the API reports it: 0 for none.
func BudgetCeilingAmount(c limes.BudgetCeiling) limes.Nanos {
	if c.Unlimited {
		return 0
	}
	return c.Max
}

// DelegatedRequests is the request ceiling a delegation's API number sets on
// the child: the parent's own for 0, since a child set unlimited under a
// bounded parent would be refused as wider than it.
func DelegatedRequests(n int64, parent limes.RequestCeiling) limes.RequestCeiling {
	if n == 0 {
		return parent
	}
	return limes.RequestCeiling{Max: n}
}

// DelegatedBudget is the budget ceiling a delegation's API amount sets on the
// child: the parent's own for 0.
func DelegatedBudget(n limes.Nanos, parent limes.BudgetCeiling) limes.BudgetCeiling {
	if n == 0 {
		return parent
	}
	return limes.BudgetCeiling{Max: n}
}
