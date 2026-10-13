package config

import (
	"strings"
	"testing"

	"github.com/oleg-tkachuk/limes"
)

// The platform's currency replaces limes.DefaultUnitCode — USD — as what an
// unnamed unit means. Unstated it is XXX, no currency, not a currency nobody
// chose.
func TestCapabilityCurrencyDefaultsToNoCurrency(t *testing.T) {
	cfg, err := loadTrashRetention(t, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Capability.Currency != limes.AbstractUnitCode {
		t.Errorf("currency = %q, want %q", cfg.Capability.Currency, limes.AbstractUnitCode)
	}
}

func TestCapabilityCurrencyIsConfigurable(t *testing.T) {
	cfg, err := loadTrashRetention(t, "capability:\n  currency: EUR\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Capability.Currency != "EUR" {
		t.Errorf("currency = %q, want EUR", cfg.Capability.Currency)
	}
}

func TestCapabilityCurrencyMustBeAnISOCode(t *testing.T) {
	for _, bad := range []string{"usd", "EURO", "ABC"} {
		_, err := loadTrashRetention(t, "capability:\n  currency: "+bad+"\n")
		if err == nil || !strings.Contains(err.Error(), "currency") {
			t.Errorf("currency %q: err = %v, want it refused", bad, err)
		}
	}
}

// A config built in code, not loaded, has no schema to fill the default; it
// must still name a currency.
func TestValidateRefusesAMissingCurrency(t *testing.T) {
	c := minimalValidConfig()
	c.Capability.Currency = ""
	mustReject(t, c, "capability.currency")
}
