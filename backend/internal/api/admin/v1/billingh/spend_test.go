package billingh

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// The platform's currency in these tests, and another unit beside it.
const (
	spendCurrency = "USD"
	otherUnit     = "EUR"
)

// recordingAuthorizer allows every call and records the resource of each.
type recordingAuthorizer struct{ resources []*cedar.Resource }

func (r *recordingAuthorizer) IsAuthorized(_ context.Context, _ *cedar.Principal, _ cedar.Action, res *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	r.resources = append(r.resources, res)
	return cedar.DecisionAllow, nil
}

// ctxWithTenantAdmin is a principal that administers its own tenant only.
func ctxWithTenantAdmin() context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		TenantID: uuid.New(), Subject: "owner@example.com", Roles: []string{"tenant.admin"}, Audience: "paladin-admin",
	})
}

// sorted is ids in the order rankSpend breaks ties in.
func sorted(ids ...uuid.UUID) []uuid.UUID {
	return slices.SortedFunc(slices.Values(ids), func(a, b uuid.UUID) int {
		return strings.Compare(a.String(), b.String())
	})
}

// Tenants are ranked in the platform's currency first, then other units by
// code, then by net spend, then by id; a tenant in several units counts in
// the one most of its charges are in.
func TestRankSpend(t *testing.T) {
	big, mixed, foreign := uuid.New(), uuid.New(), uuid.New()
	// Three tenants that spent alike: their ids alone order them.
	tied := sorted(uuid.New(), uuid.New(), uuid.New())
	sums := []meter.ChargeSum{
		{TenantID: tied[2], UnitCode: spendCurrency, Charged: units(3), Charges: 1},
		{TenantID: foreign, UnitCode: otherUnit, Charged: units(500), Charges: 9},
		{TenantID: big, UnitCode: spendCurrency, Charged: units(40), Refunded: units(10), Charges: 6},
		// Most of mixed's charges are in EUR, so it ranks there, at 7.
		{TenantID: mixed, UnitCode: spendCurrency, Charged: units(900), Charges: 1},
		{TenantID: mixed, UnitCode: otherUnit, Charged: units(7), Charges: 4},
		{TenantID: tied[0], UnitCode: spendCurrency, Charged: units(3), Charges: 2},
		{TenantID: tied[1], UnitCode: spendCurrency, Charged: units(3), Charges: 2},
	}
	want := []TenantSpend{
		{TenantID: big, UnitCode: spendCurrency, Amount: units(30), ChargeCount: 6},
		{TenantID: tied[0], UnitCode: spendCurrency, Amount: units(3), ChargeCount: 2},
		{TenantID: tied[1], UnitCode: spendCurrency, Amount: units(3), ChargeCount: 2},
		{TenantID: tied[2], UnitCode: spendCurrency, Amount: units(3), ChargeCount: 1},
		{TenantID: foreign, UnitCode: otherUnit, Amount: units(500), ChargeCount: 9},
		{TenantID: mixed, UnitCode: otherUnit, Amount: units(7), ChargeCount: 4},
	}
	if got := rankSpend(sums, spendCurrency); !slices.Equal(got, want) {
		t.Errorf("rankSpend =\n%+v\nwant\n%+v", got, want)
	}
	if got := rankSpend(nil, spendCurrency); len(got) != 0 {
		t.Errorf("no sums ranked %v", got)
	}
}

// One request sums every tenant's charges, grouped by tenant, read across
// tenants with no write allowed; the gate is ReadBilling on the caller's own
// tenant.
func TestListTenantSpendSumsEveryTenantOnce(t *testing.T) {
	tenant := uuid.New()
	ledger := &fakeLedger{sums: func(meter.SumChargesRequest) []meter.ChargeSum {
		return []meter.ChargeSum{{TenantID: tenant, UnitCode: spendCurrency, Charged: units(2), Charges: 1}}
	}}
	policy := &recordingAuthorizer{}
	h := NewHandler(ledger, nil, policy).WithCurrency(spendCurrency)
	ctx := ctxWithAdmin(t)
	got, truncated, err := h.ListTenantSpend(ctx, time.Time{}, time.Time{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if truncated || len(got) != 1 || got[0].TenantID != tenant || got[0].Amount != units(2) {
		t.Errorf("spend = %+v, truncated %v", got, truncated)
	}
	if len(ledger.requests) != 1 {
		t.Fatalf("%d ledger requests, want one", len(ledger.requests))
	}
	req := ledger.requests[0]
	if !req.AllTenants || req.TenantID != uuid.Nil() || !slices.Equal(req.GroupBy, []meter.ChargeDimension{meter.ChargeDimensionTenant}) {
		t.Errorf("request = %+v, want every tenant grouped by tenant", req)
	}
	if !auth.CrossTenantRead(ledger.ctx) {
		t.Error("the ledger was not read across tenants")
	}
	caller, _ := auth.PrincipalFromContext(ctx)
	if len(policy.resources) != 1 || policy.resources[0].TenantID != caller.TenantID {
		t.Errorf("authorized on %+v, want the caller's own tenant", policy.resources)
	}
}

// Every tenant's billing is the platform admin's alone, and a policy that
// denies ReadBilling denies it too; neither reaches the ledger.
func TestListTenantSpendRefusals(t *testing.T) {
	cases := map[string]struct {
		ctx    context.Context
		policy cedar.Authorizer
		want   connect.Code
	}{
		"a tenant admin":       {ctxWithTenantAdmin(), allowAuthorizer{}, connect.CodePermissionDenied},
		"a policy that denies": {ctxWithAdmin(t), denyAuthorizer{}, connect.CodePermissionDenied},
		"no principal":         {context.Background(), allowAuthorizer{}, connect.CodeUnauthenticated},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ledger := &fakeLedger{sums: func(meter.SumChargesRequest) []meter.ChargeSum { return nil }}
			_, _, err := NewHandler(ledger, nil, tc.policy).ListTenantSpend(tc.ctx, time.Time{}, time.Time{}, 0)
			if connect.CodeOf(err) != tc.want {
				t.Errorf("code = %v, want %v (%v)", connect.CodeOf(err), tc.want, err)
			}
			if len(ledger.requests) != 0 {
				t.Error("a refused caller reached the ledger")
			}
		})
	}
}

// limit cuts the ranking and says so; 0 is the default, and one past the
// maximum is refused.
func TestListTenantSpendLimit(t *testing.T) {
	const listed = 3
	var sums []meter.ChargeSum
	for i := range listed {
		sums = append(sums, meter.ChargeSum{TenantID: uuid.New(), UnitCode: spendCurrency, Charged: limes.Nanos(i + 1), Charges: 1})
	}
	ledger := &fakeLedger{sums: func(meter.SumChargesRequest) []meter.ChargeSum { return sums }}
	h := NewHandler(ledger, nil, allowAuthorizer{}).WithCurrency(spendCurrency)
	ctx := ctxWithAdmin(t)
	got, truncated, err := h.ListTenantSpend(ctx, time.Time{}, time.Time{}, listed-1)
	if err != nil || len(got) != listed-1 || !truncated {
		t.Errorf("limit %d: %d tenants, truncated %v, %v", listed-1, len(got), truncated, err)
	}
	if got, truncated, err := h.ListTenantSpend(ctx, time.Time{}, time.Time{}, 0); err != nil || len(got) != listed || truncated {
		t.Errorf("default limit: %d tenants, truncated %v, %v", len(got), truncated, err)
	}
	for _, limit := range []int{-1, MaxSpendLimit + 1} {
		if _, _, err := h.ListTenantSpend(ctx, time.Time{}, time.Time{}, limit); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("limit %d: %v, want InvalidArgument", limit, err)
		}
	}
}
