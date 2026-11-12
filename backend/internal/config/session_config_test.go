package config

import (
	"strings"
	"testing"
	"time"
)

func TestSessionDefaults(t *testing.T) {
	cfg, err := loadTrashRetention(t, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := cfg.Session
	if s.Server.Addr != "0.0.0.0:8086" {
		t.Errorf("session.server.addr = %q, want 0.0.0.0:8086", s.Server.Addr)
	}
	if s.RotateAfter != 5*time.Minute {
		t.Errorf("session.rotate_after = %v, want 5m", s.RotateAfter)
	}
	if len(s.ConsoleOrigins) != 0 {
		t.Errorf("session.console_origins = %v, want none", s.ConsoleOrigins)
	}
	if s.Upstreams.IAMURL == "" || s.Upstreams.DataURL == "" || s.Upstreams.AdminURL == "" {
		t.Errorf("session.upstreams = %+v, want every plane defaulted", s.Upstreams)
	}
}

func TestSessionLoadsItsOrigins(t *testing.T) {
	cfg, err := loadTrashRetention(t, "session:\n  console_origins: [\"https://console.example\", \"http://localhost:3000\"]\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Session.ConsoleOrigins; len(got) != 2 || got[0] != "https://console.example" {
		t.Errorf("session.console_origins = %v", got)
	}
}

func TestSessionRefusesWhatCannotWork(t *testing.T) {
	for name, tc := range map[string]struct{ extra, names string }{
		"origin with a path":      {"session:\n  console_origins: [\"https://console.example/app\"]\n", "console_origins"},
		"origin without a scheme": {"session:\n  console_origins: [\"console.example\"]\n", "console_origins"},
		"zero rotation":           {"session:\n  rotate_after: 0s\n", "session.rotate_after"},
		"https without a CA":      {"session:\n  upstreams:\n    iam_url: \"https://iam:8085\"\n", "session.upstreams"},
	} {
		_, err := loadTrashRetention(t, tc.extra)
		if err == nil || !strings.Contains(err.Error(), tc.names) {
			t.Errorf("%s: %v, want an error naming %s", name, err, tc.names)
		}
	}
}
