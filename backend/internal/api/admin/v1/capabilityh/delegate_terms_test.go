package capabilityh

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// The terms a delegation names are refused before anything is minted when
// they cannot be read.
func TestDelegate_RefusesUnreadableTerms(t *testing.T) {
	m := captest.NewMinter(t)
	parent := mintParent(t, m, limes.OpGet, limes.OpShare)
	h := delegateHandler(m, &allowAuthorizer{})
	ctx := auth.WithCapability(context.Background(), parent)
	for _, tc := range []struct {
		name  string
		spoil func(*adminv1.CapabilityServiceDelegateRequest)
	}{
		{"parent id not a uuid", func(r *adminv1.CapabilityServiceDelegateRequest) { r.ParentId = "parent" }},
		{"subject without a tenant", func(r *adminv1.CapabilityServiceDelegateRequest) {
			r.Subject = &adminv1.CapabilityPrincipal{TenantId: "nobody", Subject: "child"}
		}},
		{"budget in no currency", func(r *adminv1.CapabilityServiceDelegateRequest) {
			r.Caveats = &adminv1.CapabilityCaveats{
				BudgetCeiling: &adminv1.CapabilityCaveats_MaxBudget{MaxBudget: &money.Money{CurrencyCode: "not-a-currency", Units: 1}},
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := delegateReq(parent)
			tc.spoil(req)
			if _, err := h.Delegate(ctx, req); connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("code = %v (%v), want InvalidArgument", connect.CodeOf(err), err)
			}
		})
	}
}

// A delegation may name its own subject and a later not-before; the child
// carries both.
func TestDelegate_ChildCarriesTheTermsAskedFor(t *testing.T) {
	m := captest.NewMinter(t)
	parent := mintParent(t, m, limes.OpGet, limes.OpShare)
	h := delegateHandler(m, &allowAuthorizer{})
	ctx := auth.WithCapability(context.Background(), parent)
	nbf := time.Now().Add(10 * time.Second).Truncate(time.Second) // inside the delegateTTLSeconds lifetime
	req := delegateReq(parent)
	req.Subject = &adminv1.CapabilityPrincipal{TenantId: parent.Capability().Subject.TenantID.String(), Subject: "child-svc", Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_SERVICE}
	req.NotBefore = timestamppb.New(nbf)
	resp, err := h.Delegate(ctx, req)
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	got := resp.GetCapability()
	if got.GetSubject().GetSubject() != "child-svc" {
		t.Errorf("subject = %q, want child-svc", got.GetSubject().GetSubject())
	}
	if !got.GetNotBefore().AsTime().Equal(nbf) {
		t.Errorf("not_before = %v, want %v", got.GetNotBefore().AsTime(), nbf)
	}
}
