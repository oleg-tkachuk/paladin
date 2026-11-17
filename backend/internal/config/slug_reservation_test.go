package config

import (
	"strings"
	"testing"
	"time"
)

// Said nothing, the operator keeps every retired slug until it is released:
// the absent key loads as nil, not as a zero period.
func TestSlugReservationPeriodAbsentKeepsForever(t *testing.T) {
	cfg, err := loadTrashRetention(t, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p := cfg.Admin.Tenants.SlugReservationPeriod; p != nil {
		t.Fatalf("%s = %v, want nil", SlugReservationPeriodKey, *p)
	}
}

func TestSlugReservationPeriodIsConfigurable(t *testing.T) {
	const ninetyDays = 90 * 24 * time.Hour
	cfg, err := loadTrashRetention(t, "admin:\n  tenants:\n    slug_reservation_period: 2160h\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p := cfg.Admin.Tenants.SlugReservationPeriod; p == nil || *p != ninetyDays {
		t.Fatalf("%s = %v, want %v", SlugReservationPeriodKey, p, ninetyDays)
	}
}

func TestSlugReservationPeriodRefused(t *testing.T) {
	const (
		accessTTL = 15 * time.Minute
		scopedTTL = 24 * time.Hour
	)
	for _, tc := range []struct {
		name   string
		period time.Duration
		want   string
	}{
		{"zero", 0, "must be positive"},
		{"negative", -time.Hour, "must be positive"},
		{"shorter than an access token", accessTTL - time.Minute, "longest token lifetime"},
		{"shorter than a scoped token", scopedTTL - time.Minute, "longest token lifetime"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := minimalValidConfig()
			c.Auth.AccessTokenTTL, c.Auth.ScopedTokenMaxTTL = accessTTL, scopedTTL
			p := tc.period
			c.Admin.Tenants.SlugReservationPeriod = &p
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), SlugReservationPeriodKey) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate = %v, want it to name %s and say %q", err, SlugReservationPeriodKey, tc.want)
			}
		})
	}
}

// The floor itself is allowed: a token minted the instant before the purge
// has expired when the slug is released.
func TestSlugReservationPeriodAtTheFloor(t *testing.T) {
	c := minimalValidConfig()
	c.Auth.AccessTokenTTL, c.Auth.ScopedTokenMaxTTL = 15*time.Minute, 24*time.Hour
	p := c.Auth.ScopedTokenMaxTTL
	c.Admin.Tenants.SlugReservationPeriod = &p
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate at the floor: %v", err)
	}
}

// Through the loader as well as Validate, a config file with a zero period
// does not start a pod.
func TestSlugReservationPeriodZeroRefusedOnLoad(t *testing.T) {
	_, err := loadTrashRetention(t, "admin:\n  tenants:\n    slug_reservation_period: 0s\n")
	if err == nil || !strings.Contains(err.Error(), SlugReservationPeriodKey) {
		t.Fatalf("Load with a zero period: err = %v, want it refused", err)
	}
}
