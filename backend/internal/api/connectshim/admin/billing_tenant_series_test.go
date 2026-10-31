package admin

import (
	"context"
	"slices"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/billingh"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// seriesBilling answers ListTenantTimeSeries with fixed series and records
// the tenants and granularity asked.
type seriesBilling struct {
	failingBilling
	series      []billingh.TenantSeries
	asked       []uuid.UUID
	granularity string
}

func (s *seriesBilling) ListTenantTimeSeries(_ context.Context, ids []uuid.UUID, _, _ time.Time, granularity string) ([]billingh.TenantSeries, error) {
	s.asked, s.granularity = ids, granularity
	return s.series, nil
}

// The tenants go to the handler in the order named, and each series comes
// back under its tenant, its buckets in its unit.
func TestListTenantTimeSeriesForwardsAndAnswers(t *testing.T) {
	one, two := uuid.New(), uuid.New()
	day := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	billing := &seriesBilling{series: []billingh.TenantSeries{
		{TenantID: two, Series: billingh.TimeSeries{UnitCode: "USD", Buckets: []billingh.TimeBucket{{Start: day, Amount: 3 * limes.NanosPerUnit, ChargeCount: 2}}}},
		{TenantID: one, Series: billingh.TimeSeries{UnitCode: "USD", Buckets: []billingh.TimeBucket{}}},
	}}
	res, err := (&BillingServer{H: billing}).ListTenantTimeSeries(context.Background(), &pb.ListTenantTimeSeriesRequest{
		TenantIds: []string{two.String(), one.String()}, Granularity: "day",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(billing.asked, []uuid.UUID{two, one}) || billing.granularity != "day" {
		t.Errorf("asked %v by %q, want two then one by day", billing.asked, billing.granularity)
	}
	got := res.GetSeries()
	if len(got) != 2 || got[0].GetTenantId() != two.String() || got[1].GetTenantId() != one.String() {
		t.Fatalf("series = %v", got)
	}
	b := got[0].GetBuckets()
	if len(b) != 1 || !b[0].GetStart().AsTime().Equal(day) || b[0].GetChargeCount() != 2 ||
		!proto.Equal(b[0].GetSpent(), &money.Money{CurrencyCode: "USD", Units: 3}) {
		t.Errorf("buckets = %v", b)
	}
	if len(got[1].GetBuckets()) != 0 {
		t.Errorf("a quiet tenant has buckets %v", got[1].GetBuckets())
	}
}

func TestListTenantTimeSeriesRefusesABadTenantID(t *testing.T) {
	billing := &seriesBilling{}
	_, err := (&BillingServer{H: billing}).ListTenantTimeSeries(context.Background(), &pb.ListTenantTimeSeriesRequest{TenantIds: []string{"nope"}})
	if connect.CodeOf(err) != connect.CodeInvalidArgument || billing.asked != nil {
		t.Errorf("code = %v, asked %v; want InvalidArgument before the handler", connect.CodeOf(err), billing.asked)
	}
}
