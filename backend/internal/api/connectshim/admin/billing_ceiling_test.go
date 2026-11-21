package admin

import (
	"testing"

	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/billingh"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// read is a summary of a budget read, capped at ceiling in EUR.
func read(ceiling limes.BudgetCeiling) *billingh.Summary {
	return &billingh.Summary{Budget: billingh.BudgetRead, Ceiling: &ceiling, CeilingUnit: "EUR"}
}

// A summary tells a tenant with no budget row, an unlimited one, a cap of
// zero and a budget that could not be read apart; the first three used to
// read as the same 0, and the last as no budget.
func TestSetSummaryCeiling(t *testing.T) {
	none := &pb.GetTenantSummaryResponse{}
	setSummaryCeiling(none, &billingh.Summary{})
	if none.GetBudgetCeiling() != nil {
		t.Errorf("no budget row: ceiling = %v, want unset", none.GetBudgetCeiling())
	}

	open := &pb.GetTenantSummaryResponse{}
	setSummaryCeiling(open, read(limes.BudgetCeiling{Unlimited: true}))
	if open.GetUnlimitedBudget().GetCurrencyCode() != "EUR" || open.GetMaxBudget() != nil {
		t.Errorf("unlimited: ceiling = %v, want unlimited in EUR", open.GetBudgetCeiling())
	}

	frozen := &pb.GetTenantSummaryResponse{}
	setSummaryCeiling(frozen, read(limes.BudgetCeiling{}))
	if !proto.Equal(frozen.GetMaxBudget(), &money.Money{CurrencyCode: "EUR"}) {
		t.Errorf("zero: ceiling = %v, want a limit of 0 EUR", frozen.GetBudgetCeiling())
	}

	unknown := &pb.GetTenantSummaryResponse{}
	setSummaryCeiling(unknown, &billingh.Summary{Budget: billingh.BudgetUnreadable})
	if unknown.GetBudgetUnreadable() == nil || unknown.GetMaxBudget() != nil || unknown.GetUnlimitedBudget() != nil {
		t.Errorf("unreadable: ceiling = %v, want budget_unreadable", unknown.GetBudgetCeiling())
	}
}
