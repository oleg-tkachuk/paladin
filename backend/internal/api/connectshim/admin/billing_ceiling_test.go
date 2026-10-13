package admin

import (
	"testing"

	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/limes"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// A summary tells a tenant with no budget row, an unlimited one and a cap of
// zero apart; the three used to read as the same 0.
func TestSetSummaryCeiling(t *testing.T) {
	none := &pb.GetTenantSummaryResponse{}
	setSummaryCeiling(none, nil, "EUR")
	if none.GetBudgetCeiling() != nil {
		t.Errorf("no budget row: ceiling = %v, want unset", none.GetBudgetCeiling())
	}

	open := &pb.GetTenantSummaryResponse{}
	setSummaryCeiling(open, &limes.BudgetCeiling{Unlimited: true}, "EUR")
	if open.GetUnlimitedBudget().GetCurrencyCode() != "EUR" || open.GetMaxBudget() != nil {
		t.Errorf("unlimited: ceiling = %v, want unlimited in EUR", open.GetBudgetCeiling())
	}

	frozen := &pb.GetTenantSummaryResponse{}
	setSummaryCeiling(frozen, &limes.BudgetCeiling{}, "EUR")
	if !proto.Equal(frozen.GetMaxBudget(), &money.Money{CurrencyCode: "EUR"}) {
		t.Errorf("zero: ceiling = %v, want a limit of 0 EUR", frozen.GetBudgetCeiling())
	}
}
