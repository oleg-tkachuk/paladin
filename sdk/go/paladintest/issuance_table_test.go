package paladintest_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

// issuanceTable is how CapabilityService answers Issue and Delegate, which
// the server and both SDKs' fakes are held to; the backend's tests run it
// through the server's handler too.
const issuanceTable = "../../testdata/capability_issuance.json"

// The table's not_before values, and the lifetimes its cases ask for.
const (
	notBeforeLater      = "later"
	notBeforePastExpiry = "past_expiry"
	issuanceTTLSeconds  = 300
	delegateTTLSeconds  = 60
)

type issuanceWant struct {
	Code          string `json:"code"`
	BiscuitStatus string `json:"biscuit_status"`
	Biscuit       bool   `json:"biscuit"`
}

type issuanceCase struct {
	Name             string       `json:"name"`
	Ops              *[]string    `json:"ops"`
	ParentOps        []string     `json:"parent_ops"`
	ChildOps         *[]string    `json:"child_ops"`
	KeyBound         bool         `json:"key_bound"`
	NotBefore        string       `json:"not_before"`
	RequireBiscuit   bool         `json:"require_biscuit"`
	NoRequestCeiling bool         `json:"no_request_ceiling"`
	NoBudgetCeiling  bool         `json:"no_budget_ceiling"`
	Want             issuanceWant `json:"want"`
}

func answerOf(resp *adminv1.CapabilityServiceIssueResponse, err error) issuanceWant {
	if err != nil {
		return issuanceWant{Code: connect.CodeOf(err).String()}
	}
	return issuanceWant{BiscuitStatus: resp.GetBiscuitStatus().String(), Biscuit: resp.GetBiscuit() != ""}
}

func tableCaveats(ops []string) *adminv1.CapabilityCaveats {
	return &adminv1.CapabilityCaveats{
		Ops:            ops,
		RequestCeiling: &adminv1.CapabilityCaveats_UnlimitedRequests{UnlimitedRequests: true},
		BudgetCeiling:  &adminv1.CapabilityCaveats_UnlimitedBudget{UnlimitedBudget: &adminv1.UnlimitedBudget{}},
	}
}

func TestIssueAndDelegateAnswerAsTheTableSays(t *testing.T) {
	raw, err := os.ReadFile(issuanceTable)
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		Issue    []issuanceCase `json:"issue"`
		Delegate []issuanceCase `json:"delegate"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	srv := paladintest.New(t)
	admin := srv.Connect().Admin.Capability
	ctx := context.Background()
	for _, tc := range table.Issue {
		t.Run("issue/"+tc.Name, func(t *testing.T) {
			caveats := tableCaveats([]string{string(paladin.OpGet), string(paladin.OpList)})
			if tc.Ops != nil {
				caveats.Ops = *tc.Ops
			}
			if tc.NoRequestCeiling {
				caveats.RequestCeiling = nil
			}
			if tc.NoBudgetCeiling {
				caveats.BudgetCeiling = nil
			}
			req := &adminv1.CapabilityServiceIssueRequest{
				Subject:        &adminv1.CapabilityPrincipal{Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT, TenantId: srv.Tenant(), Subject: "agent:worker"},
				Audience:       paladin.PlaneNames(paladin.PlaneData),
				TtlSeconds:     issuanceTTLSeconds,
				Caveats:        caveats,
				RequireBiscuit: tc.RequireBiscuit,
			}
			if tc.KeyBound {
				req.ConfirmationJkt = testJKT
			}
			switch tc.NotBefore {
			case notBeforeLater:
				req.NotBefore = timestamppb.New(time.Now().Add(time.Minute))
			case notBeforePastExpiry:
				req.NotBefore = timestamppb.New(time.Now().Add(time.Hour))
			}
			if got := answerOf(admin.Issue(ctx, req)); got != tc.Want {
				t.Errorf("the fake answers %+v, the table %+v", got, tc.Want)
			}
		})
	}
	for _, tc := range table.Delegate {
		t.Run("delegate/"+tc.Name, func(t *testing.T) {
			parent, err := admin.Issue(ctx, &adminv1.CapabilityServiceIssueRequest{
				Subject:  &adminv1.CapabilityPrincipal{Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT, TenantId: srv.Tenant(), Subject: "agent:parent"},
				Audience: paladin.PlaneNames(paladin.PlaneData, paladin.PlaneAdmin),
				Caveats:  tableCaveats(tc.ParentOps),
			})
			if err != nil {
				t.Fatal(err)
			}
			req := &adminv1.CapabilityServiceDelegateRequest{
				ParentId: parent.GetCapability().GetId(), TtlSeconds: delegateTTLSeconds, RequireBiscuit: tc.RequireBiscuit,
			}
			if tc.ChildOps != nil {
				req.Caveats = tableCaveats(*tc.ChildOps)
			}
			if tc.KeyBound {
				req.ConfirmationJkt = testJKT
			}
			got := answerOf(srv.Connect(paladin.WithCapability(parent.GetToken())).Admin.Capability.Delegate(ctx, req))
			if got != tc.Want {
				t.Errorf("the fake answers %+v, the table %+v", got, tc.Want)
			}
		})
	}
}
