package paladin_test

import (
	"errors"
	"strings"
	"testing"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// UnlimitedCaveats sets both ceilings Issue requires, so they pass the check
// that refuses caveats which leave one unset.
func TestUnlimitedCaveats(t *testing.T) {
	c := paladin.UnlimitedCaveats(paladin.OpGet, paladin.OpList)
	if got := c.GetOps(); len(got) != 2 || got[0] != "get" || got[1] != "list" {
		t.Errorf("ops = %v", got)
	}
	if !c.GetUnlimitedRequests() {
		t.Error("the request ceiling is not unlimited")
	}
	if c.GetUnlimitedBudget() == nil || c.GetUnlimitedBudget().GetCurrencyCode() != "" {
		t.Errorf("the budget ceiling is %v, want unlimited in the platform's currency", c.GetBudgetCeiling())
	}
	if err := paladin.ValidateIssueCaveats(c); err != nil {
		t.Errorf("unlimited caveats are refused: %v", err)
	}
}

func TestValidateIssueCaveats(t *testing.T) {
	limited := func(edit func(*adminv1.CapabilityCaveats)) *adminv1.CapabilityCaveats {
		c := paladin.UnlimitedCaveats(paladin.OpGet)
		edit(c)
		return c
	}
	cases := map[string]struct {
		caveats *adminv1.CapabilityCaveats
		names   []string
	}{
		"no ops":           {limited(func(c *adminv1.CapabilityCaveats) { c.Ops = nil }), []string{"caveats.ops"}},
		"a bare op":        {limited(func(c *adminv1.CapabilityCaveats) { c.Ops = []string{"presign"} }), []string{"caveats.ops"}},
		"no request limit": {limited(func(c *adminv1.CapabilityCaveats) { c.RequestCeiling = nil }), []string{"caveats.request_ceiling"}},
		"no budget limit":  {limited(func(c *adminv1.CapabilityCaveats) { c.BudgetCeiling = nil }), []string{"caveats.budget_ceiling"}},
		"nothing":          {nil, []string{"caveats.ops", "caveats.request_ceiling", "caveats.budget_ceiling"}},
	}
	for name, tc := range cases {
		err := paladin.ValidateIssueCaveats(tc.caveats)
		if !errors.Is(err, paladin.ErrInvalidArgument) {
			t.Errorf("%s: %v, want ErrInvalidArgument", name, err)
			continue
		}
		for _, field := range tc.names {
			if !strings.Contains(err.Error(), field) {
				t.Errorf("%s: %v does not name %s", name, err, field)
			}
		}
	}
	finite := limited(func(c *adminv1.CapabilityCaveats) {
		c.RequestCeiling = &adminv1.CapabilityCaveats_MaxRequests{MaxRequests: 0}
	})
	if err := paladin.ValidateIssueCaveats(finite); err != nil {
		t.Errorf("a limit of 0 is a limit, refused: %v", err)
	}
}
