package paladin

import (
	"errors"
	"fmt"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// UnlimitedCaveats is caveats granting ops with no request and no budget
// limit, the budget in the platform's currency: the least Issue takes, which
// refuses caveats that leave either ceiling unset. To limit one, set its
// ceiling on the result:
//
//	c := paladin.UnlimitedCaveats(paladin.OpGet, paladin.OpList)
//	c.RequestCeiling = &adminv1.CapabilityCaveats_MaxRequests{MaxRequests: 100}
func UnlimitedCaveats(ops ...Op) *adminv1.CapabilityCaveats {
	return &adminv1.CapabilityCaveats{
		Ops:            OpNames(ops...),
		RequestCeiling: &adminv1.CapabilityCaveats_UnlimitedRequests{UnlimitedRequests: true},
		BudgetCeiling:  &adminv1.CapabilityCaveats_UnlimitedBudget{UnlimitedBudget: &adminv1.UnlimitedBudget{}},
	}
}

// ValidateIssueCaveats refuses, before anything is sent, caveats Issue
// refuses for their shape: no operation, an operation Op.Validate refuses,
// or a request or budget ceiling left unset. Each wraps ErrInvalidArgument;
// all of them are returned together. It checks nothing else: not the
// resources, where empty ResourcePrefixes and ResourceURIs grant every
// resource of the tenant rather than none, nor what Issue decides by the
// caller or the tenant. Delegate takes an unset ceiling as the parent's, so
// this is for Issue alone.
func ValidateIssueCaveats(c *adminv1.CapabilityCaveats) error {
	var errs []error
	if len(c.GetOps()) == 0 {
		errs = append(errs, fmt.Errorf("%w: caveats.ops: name at least one operation", ErrInvalidArgument))
	}
	for _, op := range c.GetOps() {
		if err := Op(op).Validate(); err != nil {
			errs = append(errs, fmt.Errorf("caveats.ops: %w", err))
		}
	}
	if c.GetRequestCeiling() == nil {
		errs = append(errs, fmt.Errorf("%w: caveats.request_ceiling: set max_requests, or unlimited_requests for no limit", ErrInvalidArgument))
	}
	if c.GetBudgetCeiling() == nil {
		errs = append(errs, fmt.Errorf("%w: caveats.budget_ceiling: set max_budget, or unlimited_budget for no limit", ErrInvalidArgument))
	}
	return errors.Join(errs...)
}
