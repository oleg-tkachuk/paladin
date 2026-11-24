package migstate

import (
	"slices"
	"testing"
	"uuid"
)

// Every holder of a tenant's lock must compute the same key for it, and two
// tenants mostly different ones.
func TestTenantLockKeyIsStablePerTenant(t *testing.T) {
	a := uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	b := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	same := uuid.MustParse(a.String())
	if TenantLockKey(a) != TenantLockKey(same) {
		t.Fatal("one tenant got two keys")
	}
	if TenantLockKey(a) == TenantLockKey(b) {
		t.Errorf("tenants %s and %s share key %d", a, b, TenantLockKey(a))
	}
}

// A late source write can be moved only while the migration has rebound the
// tenant and keeps its source: never before the copy, nor once cleaned or
// failed.
func TestReboundToTargetKeepsTheSource(t *testing.T) {
	got := ReboundToTarget()
	for _, want := range []string{Rebinding, Verifying, RollingBack, Completed} {
		if !slices.Contains(got, want) {
			t.Errorf("%s is not rebound to the target", want)
		}
	}
	for _, not := range []string{Provisioning, Copying, Cleaned, Failed} {
		if slices.Contains(got, not) {
			t.Errorf("%s counts as rebound to the target", not)
		}
	}
}
