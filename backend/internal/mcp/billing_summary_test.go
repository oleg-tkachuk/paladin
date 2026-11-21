package mcp

import (
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// unreadableArm is the key the summary's budget_unreadable arm is written
// under in a tool's result.
const unreadableArm = `"BudgetUnreadable"`

// A summary whose budget could not be read says so in the tool's result, so
// an agent does not read it as a tenant with no budget; one with no budget
// names no arm.
func TestBillingSummaryResultNamesAnUnreadableBudget(t *testing.T) {
	for name, tc := range map[string]struct {
		resp *adminv1.GetTenantSummaryResponse
		want bool
	}{
		"unreadable": {&adminv1.GetTenantSummaryResponse{BudgetCeiling: &adminv1.GetTenantSummaryResponse_BudgetUnreadable{BudgetUnreadable: &adminv1.BudgetUnreadable{}}}, true},
		"no budget":  {&adminv1.GetTenantSummaryResponse{}, false},
	} {
		t.Run(name, func(t *testing.T) {
			res, _, err := jsonResult(tc.resp, nil)
			if err != nil {
				t.Fatal(err)
			}
			text := res.Content[0].(*mcpsdk.TextContent).Text
			if strings.Contains(text, unreadableArm) != tc.want {
				t.Errorf("result %s: names %s = %v, want %v", text, unreadableArm, !tc.want, tc.want)
			}
		})
	}
}
