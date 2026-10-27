package admin

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/billingh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// spendBilling answers ListTenantSpend with fixed rows and records the limit.
type spendBilling struct {
	failingBilling
	rows      []billingh.TenantSpend
	truncated bool
	limit     int
}

func (s *spendBilling) ListTenantSpend(_ context.Context, _, _ time.Time, limit int) ([]billingh.TenantSpend, bool, error) {
	s.limit = limit
	return s.rows, s.truncated, nil
}

// The handler's ranking is kept, each row named by its tenant, a tenant in
// the trash marked rather than dropped, and the amount in the row's unit.
func TestListTenantSpendNamesEachRow(t *testing.T) {
	const limit = 2
	live, trashed := uuid.New(), uuid.New()
	billing := &spendBilling{truncated: true, rows: []billingh.TenantSpend{
		{TenantID: live, UnitCode: "USD", Amount: 30 * limes.NanosPerUnit, ChargeCount: 6},
		{TenantID: trashed, UnitCode: "EUR", Amount: 7 * limes.NanosPerUnit, ChargeCount: 4},
	}}
	names := &fakeNames{names: map[uuid.UUID]tenanth.Name{
		live:    {Slug: "acme", DisplayName: "Acme"},
		trashed: {Slug: "gone", DisplayName: "Gone", Deleted: true},
	}}
	srv := &BillingServer{H: billing, Tenants: names}
	res, err := srv.ListTenantSpend(context.Background(), &pb.ListTenantSpendRequest{Limit: limit})
	if err != nil {
		t.Fatal(err)
	}
	if billing.limit != limit || !res.GetTruncated() || len(res.GetTenants()) != len(billing.rows) {
		t.Fatalf("limit %d, truncated %v, %d rows", billing.limit, res.GetTruncated(), len(res.GetTenants()))
	}
	first, second := res.GetTenants()[0], res.GetTenants()[1]
	if first.GetTenantId() != live.String() || first.GetSlug() != "acme" || first.GetDeleted() ||
		!proto.Equal(first.GetSpent(), &money.Money{CurrencyCode: "USD", Units: 30}) || first.GetChargeCount() != 6 {
		t.Errorf("first row = %v", first)
	}
	if second.GetTenantId() != trashed.String() || !second.GetDeleted() ||
		!proto.Equal(second.GetSpent(), &money.Money{CurrencyCode: "EUR", Units: 7}) {
		t.Errorf("second row = %v", second)
	}
}

// A list whose tenants cannot be named is an internal failure, not rows
// with no names.
func TestListTenantSpendNamesFailureIsInternal(t *testing.T) {
	billing := &spendBilling{rows: []billingh.TenantSpend{{TenantID: uuid.New(), UnitCode: "USD"}}}
	srv := &BillingServer{H: billing, Tenants: &fakeNames{err: errors.New("db down")}}
	if _, err := srv.ListTenantSpend(context.Background(), &pb.ListTenantSpendRequest{}); connect.CodeOf(err) != connect.CodeInternal {
		t.Errorf("err = %v, want Internal", err)
	}
}
