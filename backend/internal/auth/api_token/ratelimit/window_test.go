package ratelimit

import (
	"testing"
	"time"
)

func TestWeighted(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		cur, prev int64
		elapsed   time.Duration
		want      float64
	}{
		{"start of a bucket counts all of the previous", 1, 4, 0, 5},
		{"halfway counts half", 1, 4, 30 * time.Second, 3},
		{"no previous bucket", 3, 0, 45 * time.Second, 3},
	}
	for _, tc := range cases {
		if got := Weighted(tc.cur, tc.prev, tc.elapsed); got != tc.want {
			t.Errorf("%s: Weighted(%d, %d, %s) = %v, want %v", tc.name, tc.cur, tc.prev, tc.elapsed, got, tc.want)
		}
	}
}

func TestRetryAfter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		cur, prev int64
		elapsed   time.Duration
		capacity  int
		want      time.Duration
	}{
		// 1 rpm, one request admitted at 10s: the next fits once that bucket
		// has rolled over and faded entirely — 50s to the roll, 60s more.
		{"current bucket full, previous empty", 1, 0, 10 * time.Second, 1, 110 * time.Second},
		// 1 rpm, the request came in the previous minute: wait for its share
		// to fade, which is the end of the current bucket.
		{"room now, previous bucket fading", 0, 1, 15 * time.Second, 1, 45 * time.Second},
		// 10 rpm, 4 now and 10 last minute: room for 5 of the previous
		// bucket's share, so half the window: 30s, less the 6s gone.
		{"previous share fades to the room left", 4, 10, 6 * time.Second, 10, 24 * time.Second},
		// 10 rpm, 10 now: into the next bucket until 9 of those 10 remain,
		// 10% of the window: 6s, after the 20s left in this one.
		{"current bucket full at a higher capacity", 10, 3, 40 * time.Second, 10, 26 * time.Second},
		{"a fraction of a second rounds up", 0, 1, 59500 * time.Millisecond, 1, time.Second},
		{"never less than a second", 0, 0, 0, 1, time.Second},
	}
	for _, tc := range cases {
		got := RetryAfter(tc.cur, tc.prev, tc.elapsed, tc.capacity)
		if got != tc.want {
			t.Errorf("%s: RetryAfter = %s, want %s", tc.name, got, tc.want)
		}
	}
}

// The property the header promises: after waiting RetryAfter, with nothing
// admitted meanwhile, one more request fits — and a second sooner it did not.
func TestRetryAfterIsExactAcrossTheWindow(t *testing.T) {
	t.Parallel()
	weightedAt := func(cur, prev int64, elapsed, wait time.Duration) float64 {
		at := elapsed + wait
		if at < Window {
			return Weighted(cur+1, prev, at)
		}
		return Weighted(1, cur, at-Window) // the current bucket is now the previous
	}
	for capacity := 1; capacity <= 5; capacity++ {
		for cur := int64(0); cur <= int64(capacity)+1; cur++ {
			for prev := int64(0); prev <= int64(capacity)+2; prev++ {
				for elapsed := time.Duration(0); elapsed < Window; elapsed += 7 * time.Second {
					if Weighted(cur+1, prev, elapsed) <= float64(capacity) {
						continue // admitted now; no wait to check
					}
					wait := RetryAfter(cur, prev, elapsed, capacity)
					if w := weightedAt(cur, prev, elapsed, wait); w > float64(capacity)+1e-9 {
						t.Fatalf("cap %d cur %d prev %d at %s: after %s weighted is %v, still over", capacity, cur, prev, elapsed, wait, w)
					}
					if wait > minRetryAfter {
						if w := weightedAt(cur, prev, elapsed, wait-time.Second); w <= float64(capacity) {
							t.Fatalf("cap %d cur %d prev %d at %s: %s would already do (weighted %v)", capacity, cur, prev, elapsed, wait-time.Second, w)
						}
					}
				}
			}
		}
	}
}
