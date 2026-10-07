package apiutil

import (
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

func TestHoldsPlatformRole(t *testing.T) {
	for _, tc := range []struct {
		roles []string
		want  bool
	}{
		{[]string{RolePlatformAdmin}, true},
		{[]string{RoleCapabilityIssuer}, true},
		{[]string{RoleTenantProvisioner}, true},
		{[]string{RoleTenantAdmin, RoleTenantProvisioner}, true},
		{[]string{RoleTenantAdmin, RoleIAMAdmin, RoleBucketAdmin}, false},
		{nil, false},
	} {
		if got := HoldsPlatformRole(&auth.Principal{Roles: tc.roles}); got != tc.want {
			t.Errorf("HoldsPlatformRole(%v) = %v, want %v", tc.roles, got, tc.want)
		}
	}
}
