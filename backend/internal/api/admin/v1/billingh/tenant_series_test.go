package billingh

import (
	"context"
	"slices"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// One sum of the ledger across the named tenants, grouped by tenant, gives
// each its own series, in the order asked; a tenant that spent nothing gets
// none, in the platform's currency.
func TestListTenantTimeSeries(t *testing.T) {
	day := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	busy, quiet := uuid.New(), uuid.New()
	ledger := &fakeLedger{sums: func(meter.SumChargesRequest) []meter.ChargeSum {
		return []meter.ChargeSum{
			{BucketStart: day.AddDate(0, 0, 1), TenantID: busy, UnitCode: spendCurrency, Charged: units(5), Refunded: units(1), Charges: 3},
			{BucketStart: day, TenantID: busy, UnitCode: spendCurrency, Charged: units(2), Charges: 1},
		}
	}}
	h := NewHandler(ledger, nil, allowAuthorizer{}).WithCurrency(spendCurrency)
	got, err := h.ListTenantTimeSeries(ctxWithAdmin(t), []uuid.UUID{quiet, busy}, day, day.AddDate(0, 0, 2), granularityDay)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].TenantID != quiet || got[1].TenantID != busy {
		t.Fatalf("series for %+v, want quiet then busy", got)
	}
	if got[0].Series.UnitCode != spendCurrency || len(got[0].Series.Buckets) != 0 {
		t.Errorf("quiet tenant = %+v, want %s and no buckets", got[0].Series, spendCurrency)
	}
	want := []TimeBucket{
		{Start: day, Amount: units(2), ChargeCount: 1},
		{Start: day.AddDate(0, 0, 1), Amount: units(4), ChargeCount: 3},
	}
	if got[1].Series.UnitCode != spendCurrency || !slices.Equal(got[1].Series.Buckets, want) {
		t.Errorf("busy tenant = %+v, want %+v", got[1].Series, want)
	}

	req := ledger.requests[0]
	if !req.AllTenants || req.Bucket != meter.ChargeBucketDay ||
		!slices.Equal(req.GroupBy, []meter.ChargeDimension{meter.ChargeDimensionTenant}) ||
		!slices.Equal(req.Filter.Tenants, []uuid.UUID{quiet, busy}) {
		t.Errorf("request = %+v, want the two tenants across tenants, by tenant and day", req)
	}
	if len(ledger.requests) != 1 {
		t.Errorf("the ledger was asked %d times, want once for every tenant", len(ledger.requests))
	}
	if !auth.CrossTenantRead(ledger.ctx) {
		t.Error("the ledger was not read across tenants")
	}
}

// A series of each tenant is the platform admin's alone; a bad window and an
// unwired ledger are refused as the platform's series refuses them.
func TestListTenantTimeSeriesRefusals(t *testing.T) {
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
			_, err := h.ListTenantTimeSeries(tc.ctx, []uuid.UUID{uuid.New()}, time.Time{}, time.Time{}, tc.granularity)
			if connect.CodeOf(err) != tc.want {
				t.Errorf("code = %v, want %v (%v)", connect.CodeOf(err), tc.want, err)
			}
			if len(ledger.requests) != 0 {
				t.Error("a refused request reached the ledger")
			}
		})
	}
}
