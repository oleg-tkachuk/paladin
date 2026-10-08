package ratelimit

import (
	"math"
	"time"
)

// Window is the sliding window a capacity is counted over; each bucket
// covers one Window.
const Window = time.Minute

// minRetryAfter is the shortest wait a denial asks for: Retry-After carries
// whole seconds, and zero would read as "retry now".
const minRetryAfter = time.Second

// roundingSlack absorbs float error before rounding up, so a wait that is
// exactly whole seconds (40s, computed as 40.0000001) is not a second longer.
const roundingSlack = 1e-9

// Weighted is the sliding-window count: the current bucket plus the share of
// the previous one the window still covers, elapsed into the current bucket.
func Weighted(cur, prev int64, elapsed time.Duration) float64 {
	return float64(cur) + float64(prev)*(1-elapsed.Seconds()/Window.Seconds())
}

// RetryAfter is how long until one more request fits capacity, given the
// current and previous buckets, elapsed into the current one, and no request
// admitted in between. A denied request is not counted, so waiting this long
// is enough: the answer is exact, rounded up to whole seconds.
//
// While the current bucket alone leaves room, the wait is for the previous
// bucket's share to fade enough. When it does not, the wait runs into the next
// bucket, where the current one becomes the previous and fades in turn.
func RetryAfter(cur, prev int64, elapsed time.Duration, capacity int) time.Duration {
	window := Window.Seconds()
	e := elapsed.Seconds()
	room := float64(capacity) - float64(cur) - 1 // left for the previous bucket's share
	var wait float64
	switch {
	case room >= 0 && prev > 0:
		// prev·(1 − (e+t)/W) ≤ room
		wait = window*(1-room/float64(prev)) - e
	case room < 0:
		// 1 + cur·(1 − e'/W) ≤ capacity, e' into the next bucket
		next := 0.0
		if cur > 0 {
			next = window * (1 - float64(capacity-1)/float64(cur))
		}
		wait = (window - e) + math.Max(next, 0)
	}
	secs := time.Duration(math.Ceil(wait-roundingSlack)) * time.Second
	return max(secs, minRetryAfter)
}
