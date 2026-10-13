package postgres

import (
	"errors"
	"testing"
)

// A store over no pool is refused when it is built, not on its first call.
func TestStoresRefuseANilPool(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, errNoPool) {
		t.Errorf("New(nil): err = %v, want errNoPool", err)
	}
	if _, err := NewUsageStore(nil); !errors.Is(err, errNoPool) {
		t.Errorf("NewUsageStore(nil): err = %v, want errNoPool", err)
	}
}
