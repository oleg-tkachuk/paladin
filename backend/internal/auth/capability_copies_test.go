package auth

import (
	"context"
	"reflect"
	"testing"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/biscuit"
	"github.com/oleg-tkachuk/limes/meter"
)

// copiesUsage records the copies each metering call was handed.
type copiesUsage struct {
	*usageStore
	bumped, charged []limes.CopyCeiling
	bumps           int
}

func (u *copiesUsage) Bump(ctx context.Context, req meter.BumpRequest, onBumped func(context.Context, meter.Bumped) error) (meter.BumpReceipt, error) {
	u.bumps++
	u.bumped = req.Subject.Copies()
	return u.usageStore.Bump(ctx, req, onBumped)
}

func (u *copiesUsage) Charge(ctx context.Context, req meter.ChargeRequest, on func(context.Context, meter.Charged) error) (meter.ChargeReceipt, error) {
	u.charged = req.Subject.Copies()
	return u.usageStore.Charge(ctx, req, on)
}

// A Biscuit copy's own limits reach the usage store on every metering call,
// and a copy with limits is counted even when its capability sets none.
func TestCopiesReachTheMeter(t *testing.T) {
	const (
		copyRequests = 2
		copyBudget   = limes.NanosPerUnit
		wantCopies   = 2
		chargeNanos  = limes.NanosPerUnit / 4
	)
	m, store := newMeteredMinter(t)
	root, _ := m.Mint(t, limes.Capability{Caveats: limes.Caveats{Ops: []limes.Op{limes.OpGet, limes.OpShare}}})
	_, inner := m.Attenuated(t, root, limes.Attenuation{Requests: &limes.RequestCeiling{Max: copyRequests}})
	outer, err := biscuit.Attenuate(inner, limes.Attenuation{Budget: &limes.BudgetCeiling{Max: copyBudget}})
	if err != nil {
		t.Fatalf("attenuate: %v", err)
	}
	v := m.Verify(t, outer, root.Capability().Audience[0])
	copies := v.Copies()
	if len(copies) != wantCopies {
		t.Fatalf("the verified copy carries %d limits, want %d", len(copies), wantCopies)
	}
	usage := &copiesUsage{usageStore: store}

	i := &capabilityInterceptor{usage: usage}
	if err := i.enforceCaveats(context.Background(), countedProcedure, v); err != nil {
		t.Fatalf("enforceCaveats: %v", err)
	}
	if usage.bumps != 1 || !reflect.DeepEqual(usage.bumped, copies) {
		t.Errorf("bump: %d calls with %+v, want one with the copies", usage.bumps, usage.bumped)
	}

	ctx := withLastOpHolder(WithChargeStore(WithCapability(context.Background(), v), usage))
	if err := ChargeCapability(ctx, chargeNanos); err != nil {
		t.Fatalf("charge: %v", err)
	}
	if !reflect.DeepEqual(usage.charged, copies) {
		t.Errorf("charge copies = %+v", usage.charged)
	}
}
