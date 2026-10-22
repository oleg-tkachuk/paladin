package billingh

import (
	"context"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// One request sums every tenant's charges a bucket at a time, read across
// tenants with no write allowed, gated as ListTenantSpend is.
func TestGetPlatformTimeSeriesSumsEveryTenant(t *testing.T) {
	day := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	ledger := &fakeLedger{sums: func(meter.SumChargesRequest) []meter.ChargeSum {
		return []meter.ChargeSum{
			{BucketStart: day, UnitCode: spendCurrency, Charged: units(2), Charges: 1},
			{BucketStart: day.AddDate(0, 0, 1), UnitCode: spendCurrency, Charged: units(5), Refunded: units(1), Charges: 3},
		}
	}}
	policy := &recordingAuthorizer{}
	h := NewHandler(ledger, nil, policy).WithCurrency(spendCurrency)
	ctx := ctxWithAdmin(t)
	ts, err := h.GetPlatformTimeSeries(ctx, day, day.AddDate(0, 0, 2), granularityDay)
	if err != nil {
		t.Fatal(err)
	}
	want := []TimeBucket{
		{Start: day, Amount: units(2), ChargeCount: 1},
		{Start: day.AddDate(0, 0, 1), Amount: units(4), ChargeCount: 3},
	}
	if ts.UnitCode != spendCurrency || !slices.Equal(ts.Buckets, want) {
		t.Errorf("series = %s %+v, want %s %+v", ts.UnitCode, ts.Buckets, spendCurrency, want)
	}
	req := ledger.requests[0]
	if !req.AllTenants || req.TenantID != uuid.Nil || req.Bucket != meter.ChargeBucketDay {
		t.Errorf("request = %+v, want every tenant by day", req)
	}
	if !auth.CrossTenantRead(ledger.ctx) {
		t.Error("the ledger was not read across tenants")
	}
	caller, _ := auth.PrincipalFromContext(ctx)
	if len(policy.resources) != 1 || policy.resources[0].TenantID != caller.TenantID {
		t.Errorf("authorized on %+v, want the caller's own tenant", policy.resources)
	}
}

// A period with no charge is the platform's currency and no buckets, not an
// empty unit.
func TestGetPlatformTimeSeriesWithNoCharge(t *testing.T) {
	ledger := &fakeLedger{sums: func(meter.SumChargesRequest) []meter.ChargeSum { return nil }}
	ts, err := NewHandler(ledger, nil, allowAuthorizer{}).WithCurrency(spendCurrency).GetPlatformTimeSeries(ctxWithAdmin(t), time.Time{}, time.Time{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if ts.UnitCode != spendCurrency || len(ts.Buckets) != 0 {
		t.Errorf("series = %s %+v, want %s and none", ts.UnitCode, ts.Buckets, spendCurrency)
	}
}

// Every tenant's series is the platform admin's alone; a bad window and an
// unwired ledger are refused as a tenant's series refuses them.
func TestGetPlatformTimeSeriesRefusals(t *testing.T) {
	cases := map[string]struct {
		ctx         context.Context
		policy      cedar.Authorizer
		ledger      bool
		granularity string
		want        connect.Code
	}{
		"a tenant admin":         {ctxWithTenantAdmin(), allowAuthorizer{}, true, "", connect.CodePermissionDenied},
		"a policy that denies":   {ctxWithAdmin(t), denyAuthorizer{}, true, "", connect.CodePermissionDenied},
		"an unknown granularity": {ctxWithAdmin(t), allowAuthorizer{}, true, "month", connect.CodeInvalidArgument},
		"no ledger":              {ctxWithAdmin(t), allowAuthorizer{}, false, "", connect.CodeUnavailable},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ledger := &fakeLedger{sums: func(meter.SumChargesRequest) []meter.ChargeSum { return nil }}
			h := NewHandler(nil, nil, tc.policy)
			if tc.ledger {
				h = NewHandler(ledger, nil, tc.policy)
			}
			_, err := h.GetPlatformTimeSeries(tc.ctx, time.Time{}, time.Time{}, tc.granularity)
			if connect.CodeOf(err) != tc.want {
				t.Errorf("code = %v, want %v (%v)", connect.CodeOf(err), tc.want, err)
			}
			if len(ledger.requests) != 0 {
				t.Error("a refused request reached the ledger")
			}
		})
	}
}
