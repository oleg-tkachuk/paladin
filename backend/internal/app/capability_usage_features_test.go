package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/memstore"
	"github.com/oleg-tkachuk/limes/meter"
)

// bareUsage is a usage store with none of the optional features: only the
// interface every store must implement.
type bareUsage struct{ meter.UsageStore }

func memUsage(t *testing.T) *memstore.UsageStore {
	t.Helper()
	records, err := memstore.NewStore(memstore.StoreConfig{})
	if err != nil {
		t.Fatal(err)
	}
	usage, err := memstore.NewUsageStore(memstore.UsageStoreConfig{Records: records})
	if err != nil {
		t.Fatal(err)
	}
	return usage
}

// leasesOff is a usage store whose configuration turned leasing off, as
// pgstore's Config.DisableLeases does: it still implements meter.Leaser.
type leasesOff struct{ *memstore.UsageStore }

func (leasesOff) FeatureEnabled(f meter.Feature) bool { return f != meter.FeatureLeases }

// The usage store the server runs on, metered as it runs, has every
// optional interface the server calls; one that dropped any is refused at
// startup, naming each. Leases are required only when the deployment leases.
func TestCapabilityUsageParts(t *testing.T) {
	metered, err := meteredUsage(memUsage(t))
	if err != nil {
		t.Fatal(err)
	}
	parts, err := usagePartsOf(metered, true)
	if err != nil {
		t.Fatalf("the metered usage store: %v", err)
	}
	if parts.copies == nil || parts.holds == nil || parts.leases == nil || parts.budgets == nil {
		t.Fatalf("parts of the metered usage store left unset: %+v", parts)
	}

	_, err = usagePartsOf(bareUsage{metered}, true)
	if !errors.Is(err, limes.ErrInvalidConfig) {
		t.Fatalf("a store with no optional interface: err = %v, want ErrInvalidConfig", err)
	}
	for _, name := range []string{"CopyUsageReader", "MatchingReleaser", "Leaser", "TenantBudgetStore"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the refusal %q does not name %s", err, name)
		}
	}

	// Leasing turned off: the store need not lease, and is not asked to.
	off, err := usagePartsOf(leasesOff{memUsage(t)}, false)
	if err != nil {
		t.Fatalf("leasing off: %v", err)
	}
	if off.leases != nil {
		t.Errorf("leasing off wired a leaser")
	}
	if _, err := usagePartsOf(leasesOff{memUsage(t)}, true); !errors.Is(err, limes.ErrInvalidConfig) {
		t.Errorf("leasing on over a store that turned it off: err = %v, want ErrInvalidConfig", err)
	}
}
