package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
)

// oauthClientReapKey is the config path the reaper's bound error names.
const oauthClientReapKey = "oauth_client_reap"

// A running reaper needs an age, or every idle self-registered client would
// go the moment it registered; a disabled one needs none.
func TestValidateOAuthClientReapNeedsAnAge(t *testing.T) {
	cases := map[string]struct {
		interval, maxAge time.Duration
		ok               bool
	}{
		"running with an age":  {time.Hour, 7 * 24 * time.Hour, true},
		"disabled without one": {0, 0, true},
		"running without one":  {time.Hour, 0, false},
		"running with less":    {time.Hour, -time.Hour, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := minimalValidConfig()
			c.Worker.Jobs.OAuthClientReap = OAuthClientReap{Interval: tc.interval, MaxAge: tc.maxAge}
			if tc.ok {
				if err := c.Validate(); err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			mustReject(t, c, oauthClientReapKey)
		})
	}
}

// What the schema supplies when a configuration names no reaper: hourly, for
// clients idle a week.
func TestOAuthClientReapDefaults(t *testing.T) {
	const (
		wantInterval = time.Hour
		wantMaxAge   = 7 * 24 * time.Hour
	)
	minPath := filepath.Join(t.TempDir(), "min.yaml")
	if err := os.WriteFile(minPath, []byte(minimalConfigYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load([]string{minPath}, zap.NewNop())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r := cfg.Worker.Jobs.OAuthClientReap; r.Interval != wantInterval || r.MaxAge != wantMaxAge {
		t.Errorf("defaults = %+v, want interval %s, max_age %s", r, wantInterval, wantMaxAge)
	}
}
