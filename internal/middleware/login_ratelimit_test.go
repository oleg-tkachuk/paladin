package middleware

import (
	"testing"
	"time"
)

func TestLoginRateLimiterAdmitsUnderCap(t *testing.T) {
	now := time.Now()
	l := &LoginRateLimiter{
		MaxAttempts: 3,
		Window:      1 * time.Minute,
		buckets:     map[string][]time.Time{},
		now:         func() time.Time { return now },
	}
	for i := 0; i < 3; i++ {
		if !l.allow("alice|1.1.1.1") {
			t.Fatalf("attempt %d denied unexpectedly", i+1)
		}
	}
}

func TestLoginRateLimiterRejectsAtCap(t *testing.T) {
	now := time.Now()
	l := &LoginRateLimiter{
		MaxAttempts: 2,
		Window:      1 * time.Minute,
		buckets:     map[string][]time.Time{},
		now:         func() time.Time { return now },
	}
	_ = l.allow("k")
	_ = l.allow("k")
	if l.allow("k") {
		t.Error("third attempt should be rejected")
	}
}

func TestLoginRateLimiterReleasesOnWindowRoll(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cur := t0
	l := &LoginRateLimiter{
		MaxAttempts: 1,
		Window:      30 * time.Second,
		buckets:     map[string][]time.Time{},
		now:         func() time.Time { return cur },
	}
	if !l.allow("k") {
		t.Fatal("first attempt rejected")
	}
	if l.allow("k") {
		t.Fatal("second attempt should still be rejected")
	}
	cur = t0.Add(31 * time.Second) // window has rolled
	if !l.allow("k") {
		t.Error("attempt after window-roll should be admitted")
	}
}

func TestLoginRateLimiterScopesPerTuple(t *testing.T) {
	now := time.Now()
	l := &LoginRateLimiter{
		MaxAttempts: 1,
		Window:      1 * time.Minute,
		buckets:     map[string][]time.Time{},
		now:         func() time.Time { return now },
	}
	if !l.allow("alice|1.1.1.1") {
		t.Fatal("alice@1 first attempt denied")
	}
	if l.allow("alice|1.1.1.1") {
		t.Error("alice@1 should be capped")
	}
	if !l.allow("bob|1.1.1.1") {
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
