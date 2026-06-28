package oauth

import (
	"testing"
	"time"
)

func TestTokenBucket_BurstThenThrottle(t *testing.T) {
	cur := time.Now()
	tb := newTokenBucket(60) // capacity 60, refill 1/s
	tb.now = func() time.Time { return cur }

	for i := 0; i < 60; i++ {
		if ok, _ := tb.allow("c"); !ok {
			t.Fatalf("request %d within burst should be allowed", i+1)
		}
	}
	ok, retry := tb.allow("c")
	if ok {
		t.Fatal("request past the burst should be throttled")
	}
	if retry <= 0 {
		t.Fatalf("throttled request must report a positive Retry-After, got %v", retry)
	}

	// One token refills after ~1s.
	cur = cur.Add(time.Second)
	if ok, _ := tb.allow("c"); !ok {
		t.Fatal("a token should have refilled after 1s")
	}
}

func TestTokenBucket_PerKeyIsolation(t *testing.T) {
	tb := newTokenBucket(1) // capacity 1
	if ok, _ := tb.allow("a"); !ok {
		t.Fatal("first request for key a allowed")
	}
	if ok, _ := tb.allow("a"); ok {
		t.Fatal("second request for key a throttled")
	}
	if ok, _ := tb.allow("b"); !ok {
		t.Fatal("key b has its own independent bucket")
	}
}

func TestTokenBucket_ReapDropsIdle(t *testing.T) {
	cur := time.Now()
	tb := newTokenBucket(60)
	tb.now = func() time.Time { return cur }
	tb.allow("c")
	cur = cur.Add(10 * time.Minute) // long idle → fully refilled
	tb.reap()
	if len(tb.buckets) != 0 {
		t.Fatalf("idle bucket should be reaped, %d remain", len(tb.buckets))
	}
}
