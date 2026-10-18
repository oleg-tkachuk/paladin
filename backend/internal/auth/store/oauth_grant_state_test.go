package store

import (
	"testing"
	"time"
)

func TestOAuthGrantStateOf(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		revoked   bool
		expiresAt time.Time
		want      OAuthGrantState
	}{
		{"live and unexpired", false, now.Add(time.Hour), OAuthGrantActive},
		{"expiring this instant", false, now, OAuthGrantExpired},
		{"past its expiry", false, now.Add(-time.Hour), OAuthGrantExpired},
		{"revoked before its expiry", true, now.Add(time.Hour), OAuthGrantRevoked},
		// The revoke ended it; lapsing afterwards does not rename why.
		{"revoked and since expired", true, now.Add(-time.Hour), OAuthGrantRevoked},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := OAuthGrantStateOf(tc.revoked, tc.expiresAt, now); got != tc.want {
				t.Errorf("state = %v, want %v", got, tc.want)
			}
		})
	}
}
