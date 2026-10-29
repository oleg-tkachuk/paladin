package auth

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/biscuit"
	"github.com/oleg-tkachuk/limes/meter"
)

// copiesUsage records the copies each metering call was handed.
type copiesUsage struct {
	*usageStore
	bumped, charged, reserved []limes.CopyCeiling
	bumps                     int
}

func (u *copiesUsage) Bump(ctx context.Context, req meter.BumpRequest) (meter.BumpReceipt, error) {
	u.bumps++
	u.bumped = req.Subject.Copies()
	return u.usageStore.Bump(ctx, req)
}

func (u *copiesUsage) Charge(ctx context.Context, req meter.ChargeRequest, on func(context.Context, meter.Charged) error) (meter.ChargeReceipt, error) {
	u.charged = req.Subject.Copies()
	return u.usageStore.Charge(ctx, req, on)
}

func (u *copiesUsage) Reserve(ctx context.Context, req meter.ReserveRequest) (meter.Reservation, error) {
	u.reserved = req.Subject.Copies()
	return u.usageStore.Reserve(ctx, req)
}

// A Biscuit copy's own limits reach the usage store on every metering call,
// and a copy with limits is counted even when its capability sets none.
func TestCopiesReachTheMeter(t *testing.T) {
	const (
		copyRequests       = 2
		copyBudget         = limes.NanosPerUnit
		holdTTL            = time.Minute
		wantCopies         = 2
		chargeAndHoldNanos = limes.NanosPerUnit / 4
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
	if err := i.enforceCaveats(context.Background(), v); err != nil {
		t.Fatalf("enforceCaveats: %v", err)
	}
	if usage.bumps != 1 || !reflect.DeepEqual(usage.bumped, copies) {
		t.Errorf("bump: %d calls with %+v, want one with the copies", usage.bumps, usage.bumped)
	}

	ctx := withLastOpHolder(WithChargeStore(WithCapability(context.Background(), v), usage))
	if err := ChargeCapability(ctx, chargeAndHoldNanos); err != nil {
		t.Fatalf("charge: %v", err)
	}
	if !reflect.DeepEqual(usage.charged, copies) {
		t.Errorf("charge copies = %+v", usage.charged)
	}
	if _, err := ReserveCapability(ctx, chargeAndHoldNanos, holdTTL); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if !reflect.DeepEqual(usage.reserved, copies) {
		t.Errorf("reserve copies = %+v", usage.reserved)
	}
}

// A hold is released by the capability that made it. The store releases only
// for the subject that reserved, so a release naming none left the hold in
// place until it lapsed.
func TestReleaseReservationFreesTheCapabilitysHold(t *testing.T) {
	const (
		holdNanos = limes.NanosPerUnit / 4
		holdTTL   = time.Minute
	)
	m, store := newMeteredMinter(t)
	root, token := m.Mint(t, limes.Capability{})
	v := m.Verify(t, token, root.Capability().Audience[0])
	ctx := withLastOpHolder(WithChargeStore(WithCapability(context.Background(), v), store))

	id, err := ReserveCapability(ctx, holdNanos, holdTTL)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := ReleaseReservation(ctx, id); err != nil {
		t.Fatalf("release: %v", err)
	}
	u, err := store.GetUsage(ctx, v.Capability().ID)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if u.ReservedAmount != 0 {
		t.Errorf("held after release = %v, want 0", u.ReservedAmount)
	}
}
