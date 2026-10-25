package config

import (
	"testing"
	"time"

	"github.com/oleg-tkachuk/limes"
)

// verifierLeewayKey and purgeRetentionKey are the config paths the bound
// errors name, so an operator can find the value that stopped the boot.
const (
	verifierLeewayKey = "verifier_leeway"
	purgeRetentionKey = "expired_for"
)

// The verifier leeway is held to 0..limes.MaxLeeway, both ends included:
// limes refuses a verifier outside it, so Validate stops the boot first.
func TestValidateVerifierLeewayBounds(t *testing.T) {
	cases := map[string]struct {
		leeway time.Duration
		ok     bool
	}{
		"zero":            {0, true},
		"one second":      {time.Second, true},
		"exactly maximum": {limes.MaxLeeway, true},
		"just past max":   {limes.MaxLeeway + time.Nanosecond, false},
		"negative":        {-time.Nanosecond, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := minimalValidConfig()
			c.Capability.VerifierLeeway = tc.leeway
			if tc.ok {
				if err := c.Validate(); err != nil {
					t.Fatalf("leeway %s refused: %v", tc.leeway, err)
				}
				return
			}
			mustReject(t, c, verifierLeewayKey)
		})
	}
}

// The purge retention is 0 (the job's default) or at least
// limes.MinPurgeRetention: a shorter one would drop a revocation while a
// verifier still accepts its token.
func TestValidatePurgeRetentionBounds(t *testing.T) {
	cases := map[string]struct {
		expiredFor time.Duration
		ok         bool
	}{
		"zero keeps the default": {0, true},
		"exactly minimum":        {limes.MinPurgeRetention, true},
		"above minimum":          {limes.MinPurgeRetention + time.Hour, true},
		"just below minimum":     {limes.MinPurgeRetention - time.Nanosecond, false},
		"one second":             {time.Second, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := minimalValidConfig()
			c.Worker.Jobs.Capability.ExpiredFor = tc.expiredFor
			if tc.ok {
				if err := c.Validate(); err != nil {
					t.Fatalf("expired_for %s refused: %v", tc.expiredFor, err)
				}
				return
			}
			mustReject(t, c, purgeRetentionKey)
		})
	}
}

// Both revocation cache TTLs are held to limes.MaxRevocationCacheSyncedTTL:
// limes caches for the longer of the two and refuses a cache past it, so
// Validate stops the boot first and names the key.
func TestValidateRevocationCacheTTLBounds(t *testing.T) {
	for key, set := range map[string]func(*Config, time.Duration){
		revocationCacheTTLKey:       func(c *Config, d time.Duration) { c.Capability.RevocationCacheTTL = d },
		revocationCacheSyncedTTLKey: func(c *Config, d time.Duration) { c.Capability.RevocationCacheSyncedTTL = d },
	} {
		t.Run(key, func(t *testing.T) {
			c := minimalValidConfig()
			set(&c, limes.MaxRevocationCacheSyncedTTL)
			if err := c.Validate(); err != nil {
				t.Fatalf("%s at the ceiling refused: %v", key, err)
			}
			set(&c, limes.MaxRevocationCacheSyncedTTL+time.Nanosecond)
			mustReject(t, c, key)
		})
	}
}
