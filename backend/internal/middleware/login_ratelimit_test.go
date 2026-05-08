package middleware

import (
	"testing"
	"time"
)

func newTestLimiter(perSubject, perIP int, window time.Duration, now time.Time) *LoginRateLimiter {
	return &LoginRateLimiter{
		PerSubjectMax: perSubject,
		PerIPMax:      perIP,
		Window:        window,
		buckets:       map[string][]time.Time{},
		now:           func() time.Time { return now },
	}
}

func TestLoginRateLimiterAdmitsUnderCap(t *testing.T) {
	l := newTestLimiter(3, 100, 1*time.Minute, time.Now())
	for i := 0; i < 3; i++ {
		if !l.admit("alice", "1.1.1.1") {
			t.Fatalf("attempt %d denied unexpectedly", i+1)
		}
	}
}

func TestLoginRateLimiterRejectsAtPerSubjectCap(t *testing.T) {
	l := newTestLimiter(2, 100, 1*time.Minute, time.Now())
	_ = l.admit("alice", "1.1.1.1")
	_ = l.admit("alice", "1.1.1.1")
	if l.admit("alice", "1.1.1.1") {
		t.Error("third attempt for same subject should be rejected")
	}
}

func TestLoginRateLimiterRejectsAtPerIPCap(t *testing.T) {
	// Per-IP cap = 5, per-subject cap = 100 — varying subjects shouldn't
	// dodge the per-IP layer.
	l := newTestLimiter(100, 5, 1*time.Minute, time.Now())
	for i := 0; i < 5; i++ {
		s := "fabricated-" + string(rune('a'+i))
		if !l.admit(s, "1.1.1.1") {
			t.Fatalf("attempt %d denied under per-IP cap", i+1)
		}
	}
	if l.admit("another-subject", "1.1.1.1") {
		t.Error("6th attempt should hit per-IP cap regardless of subject")
	}
}

func TestLoginRateLimiterIPCapDoesNotConsumeSubjectQuota(t *testing.T) {
	// A request rejected at the per-IP layer must NOT count toward the
	// per-subject bucket — otherwise an IP-flood would lock specific users
	// out by exhausting their per-subject window.
	l := newTestLimiter(2, 1, 1*time.Minute, time.Now())
	if !l.admit("alice", "1.1.1.1") {
		t.Fatal("alice's first attempt unexpectedly denied")
	}
	// Per-IP is now full. Bob trying from the same IP gets rejected — but
	// his per-subject bucket should remain at 0.
	if l.admit("bob", "1.1.1.1") {
		t.Fatal("per-IP cap should reject bob")
	}
	// From a different IP, bob still has full quota.
	if !l.admit("bob", "2.2.2.2") {
		t.Error("bob's first attempt from a different IP should succeed")
	}
}

func TestLoginRateLimiterReleasesOnWindowRoll(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cur := t0
	l := &LoginRateLimiter{
		PerSubjectMax: 1,
		PerIPMax:      100,
		Window:        30 * time.Second,
		buckets:       map[string][]time.Time{},
		now:           func() time.Time { return cur },
	}
	if !l.admit("alice", "1.1.1.1") {
		t.Fatal("first attempt rejected")
	}
	if l.admit("alice", "1.1.1.1") {
		t.Fatal("second attempt should still be rejected")
	}
	cur = t0.Add(31 * time.Second) // window has rolled
	if !l.admit("alice", "1.1.1.1") {
		t.Error("attempt after window-roll should be admitted")
	}
}

func TestLoginRateLimiterScopesPerTuple(t *testing.T) {
	l := newTestLimiter(1, 100, 1*time.Minute, time.Now())
	if !l.admit("alice", "1.1.1.1") {
		t.Fatal("alice@1 first attempt denied")
	}
	if l.admit("alice", "1.1.1.1") {
		t.Error("alice@1 should be capped")
	}
	if !l.admit("bob", "1.1.1.1") {
		t.Error("bob should not be impacted by alice's bucket")
	}
}

func TestFirstFwdedIP(t *testing.T) {
	cases := map[string]string{
		"":                    "",
		"1.2.3.4":             "1.2.3.4",
		"1.2.3.4, 5.6.7.8":    "1.2.3.4",
		"  1.2.3.4 , 5.6.7.8": "1.2.3.4",
	}
	for in, want := range cases {
		if got := firstFwdedIP(in); got != want {
			t.Errorf("firstFwdedIP(%q): got %q want %q", in, got, want)
		}
	}
}
