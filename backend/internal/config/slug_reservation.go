package config

import (
	"fmt"
	"time"
)

// SlugReservationPeriodKey is the config key of
// AdminTenants.SlugReservationPeriod, as Validate names it.
const SlugReservationPeriodKey = "admin.tenants.slug_reservation_period"

// validateSlugReservationPeriod refuses a period that is not positive, or
// that ends before a token naming the purged tenant by slug can expire:
// a new tenant taking the slug would be named by it.
func validateSlugReservationPeriod(period *time.Duration, a Auth) error {
	if period == nil {
		return nil
	}
	if *period <= 0 {
		return fmt.Errorf("%s must be positive", SlugReservationPeriodKey)
	}
	if floor := max(a.AccessTokenTTL, a.ScopedTokenMaxTTL); *period < floor {
		return fmt.Errorf("%s (%s) must be at least the longest token lifetime naming a tenant by slug, %s (auth.access_token_ttl, auth.scoped_token_max_ttl)",
			SlugReservationPeriodKey, *period, floor)
	}
	return nil
}
