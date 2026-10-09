package auth

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/limes"
)

// copiesUsage records the Copies each metering call was handed.
type copiesUsage struct {
	*fakeUsage
	bumped, charged, reserved []limes.CopyCeiling
	bumps                     int
}

func (u *copiesUsage) Bump(ctx context.Context, req limes.BumpRequest) (int64, error) {
	u.bumps++
	u.bumped = req.Copies
	return u.fakeUsage.Bump(ctx, req)
}

func (u *copiesUsage) Charge(ctx context.Context, req limes.ChargeRequest, on func(context.Context, pgx.Tx) error) (limes.ChargeReceipt, error) {
	u.charged = req.Copies
	return u.fakeUsage.Charge(ctx, req, on)
}

func (u *copiesUsage) Reserve(ctx context.Context, req limes.ReserveRequest) (limes.Reservation, error) {
	u.reserved = req.Copies
	return u.fakeUsage.Reserve(ctx, req)
}

// A Biscuit copy's own limits reach the usage store on every metering call,
// and a copy with limits is counted even when its capability sets none.
func TestCopiesReachTheMeter(t *testing.T) {
	copies := []limes.CopyCeiling{
		{RevocationID: []byte("inner"), MaxRequests: 2},
		{RevocationID: []byte("outer"), MaxBudget: limes.NanosPerUnit},
	}
	cap := &limes.Capability{ID: uuid.New(), Subject: limes.Principal{TenantID: uuid.New()}, Copies: copies}
	usage := &copiesUsage{fakeUsage: newFakeUsage()}

	i := &capabilityInterceptor{usage: usage}
	if err := i.enforceCaveats(context.Background(), cap); err != nil {
		t.Fatalf("enforceCaveats: %v", err)
	}
	if usage.bumps != 1 || !reflect.DeepEqual(usage.bumped, copies) {
		t.Errorf("bump: %d calls with %+v, want one with the copies", usage.bumps, usage.bumped)
	}

	ctx := withLastOpHolder(WithChargeStore(WithCapability(context.Background(), cap), usage))
	if err := ChargeCapability(ctx, limes.MustParseAmount("0.25"), ""); err != nil {
		t.Fatalf("charge: %v", err)
	}
	if !reflect.DeepEqual(usage.charged, copies) {
		t.Errorf("charge copies = %+v", usage.charged)
	}
	if _, err := ReserveCapability(ctx, limes.MustParseAmount("0.25"), time.Minute); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if !reflect.DeepEqual(usage.reserved, copies) {
		t.Errorf("reserve copies = %+v", usage.reserved)
	}
}
