package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
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

// Removing the ledger's old charges keeps at least meter.MinChargeRetention
// of them: a leap year of billing and the disputes after it. Off, any value
// stands, as nothing reads it.
func TestValidateChargeRetentionBounds(t *testing.T) {
	for name, tc := range map[string]struct {
		purge     bool
		olderThan time.Duration
		ok        bool
	}{
		"off, any retention":     {false, time.Hour, true},
		"on, at the minimum":     {true, meter.MinChargeRetention, true},
		"on, just below minimum": {true, meter.MinChargeRetention - time.Nanosecond, false},
		"on, unset":              {true, 0, false},
	} {
		t.Run(name, func(t *testing.T) {
			c := minimalValidConfig()
			c.Worker.Jobs.Capability.PurgeCharges = tc.purge
			c.Worker.Jobs.Capability.ChargesOlderThan = tc.olderThan
			if tc.ok {
				if err := c.Validate(); err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			mustReject(t, c, chargesOlderThanKey)
		})
	}
}

// schema.cue's default for charges_older_than is limes's floor itself: a
// floor that moves with a limes bump would turn the shipped default into a
// boot failure the moment purge_charges is set.
func TestChargeRetentionDefaultIsLimesFloor(t *testing.T) {
	minPath := filepath.Join(t.TempDir(), "min.yaml")
	if err := os.WriteFile(minPath, []byte(minimalConfigYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load([]string{minPath}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Worker.Jobs.Capability.ChargesOlderThan; got != meter.MinChargeRetention {
		t.Errorf("charges_older_than defaults to %s, want meter.MinChargeRetention %s", got, meter.MinChargeRetention)
	}
	if cfg.Worker.Jobs.Capability.PurgeCharges {
		t.Error("purge_charges is on by default")
	}
}
