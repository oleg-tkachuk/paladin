package postgres

import (
	"context"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/limes/meter"
)

// A store over no pool is refused when it is built, not on its first call.
func TestStoresRefuseANilPool(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Error("New(nil) built a store")
	}
	if _, err := NewUsageStore(nil); err == nil {
		t.Error("NewUsageStore(nil) built a store")
	}
	if _, err := NewLedger(nil); err == nil {
		t.Error("NewLedger(nil) built a ledger")
	}
}

// The options reach the pgstore configuration the stores are built from;
// none given leaves pgstore's own defaults.
func TestOptionsReachTheStoreConfig(t *testing.T) {
	pool := &pgxpool.Pool{}

	cfg, _, err := config(pool, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StripeUnlimitedTenants || cfg.DisableLeases || cfg.Logger != nil || cfg.Events.OnTenantWait != nil {
		t.Fatalf("no options changed pgstore's defaults: %+v", cfg)
	}

	log := slog.New(slog.DiscardHandler)
	var waited int
	events := meter.StoreEvents{OnTenantWait: func(context.Context, meter.TenantWaitEvent) { waited++ }}
	cfg, _, err = config(pool, []Option{WithStripes(true), WithLeases(false), WithLogger(log), WithEvents(events)})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.StripeUnlimitedTenants {
		t.Error("WithStripes(true) did not stripe unlimited tenants")
	}
	if !cfg.DisableLeases {
		t.Error("WithLeases(false) left leasing on")
	}
	if cfg.Logger != log {
		t.Error("WithLogger did not reach the store")
	}
	if cfg.Events.OnTenantWait == nil {
		t.Fatal("WithEvents did not reach the store")
	}
	cfg.Events.OnTenantWait(context.Background(), meter.TenantWaitEvent{})
	if waited != 1 {
		t.Errorf("the store's OnTenantWait called the given callback %d times, want 1", waited)
	}
}
