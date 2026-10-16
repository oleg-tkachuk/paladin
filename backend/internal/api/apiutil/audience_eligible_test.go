package apiutil

import (
	"errors"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

func TestAudienceEligible(t *testing.T) {
	user := []string{RoleTenantUser}
	admin := []string{RoleTenantAdmin}
	for _, tc := range []struct {
		name     string
		roles    []string
		audience string
		want     error
	}{
		{"any user holds data", user, auth.AudienceData, nil},
		{"any user holds iam", user, auth.AudienceIAM, nil},
		{"a user without an admin role is refused admin", user, auth.AudienceAdmin, ErrAudienceRole},
		{"no roles at all is refused admin", nil, auth.AudienceAdmin, ErrAudienceRole},
		{"an admin role holds admin", admin, auth.AudienceAdmin, nil},
		{"mcp is not a plane", admin, auth.AudienceMCP, ErrUnsupportedAudience},
		{"an unknown audience", admin, "paladin-bogus", ErrUnsupportedAudience},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := AudienceEligible(tc.roles, tc.audience); !errors.Is(err, tc.want) {
				t.Errorf("AudienceEligible(%v, %q) = %v, want %v", tc.roles, tc.audience, err, tc.want)
			}
		})
	}
}
