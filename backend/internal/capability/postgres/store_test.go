package postgres

import (
	"testing"
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
