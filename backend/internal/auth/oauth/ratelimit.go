package oauth

import (
	"sync"
	"time"
)

// tokenBucket is a per-key token-bucket rate limiter for the /oauth/token
// endpoint (ADR-0009 hardening). It's in-memory and per-process: good enough
// to blunt code/secret brute-forcing at the edge without a DB round-trip per
// request. Each pod limits independently — for a strict global cap, front the
// endpoint with an ingress rate-limit too.
type tokenBucket struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	rate     float64 // tokens refilled per second
	capacity float64 // burst ceiling
	now      func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// newTokenBucket builds a limiter that allows ~perMinute requests/minute per
// key with a burst up to perMinute. perMinute <= 0 falls back to 60.
func newTokenBucket(perMinute int) *tokenBucket {
	if perMinute <= 0 {
		perMinute = 60
	}
	return &tokenBucket{
		buckets:  make(map[string]*bucket),
		rate:     float64(perMinute) / 60.0,
		capacity: float64(perMinute),
		now:      time.Now,
	}
}

// allow consumes one token for key. It returns ok=false plus the duration to
// wait before the next token is available when the bucket is empty.
func (t *tokenBucket) allow(key string) (ok bool, retryAfter time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	b, exists := t.buckets[key]
	if !exists {
		// First request for this key starts with a full bucket, minus this one.
		t.buckets[key] = &bucket{tokens: t.capacity - 1, last: now}
		return true, 0
	}

	// Refill based on elapsed time, capped at capacity.
	elapsed := now.Sub(b.last).Seconds()
	b.tokens = min(t.capacity, b.tokens+elapsed*t.rate)
	b.last = now

	if b.tokens < 1 {
		// Time until one token accrues.
		deficit := 1 - b.tokens
		return false, time.Duration(deficit/t.rate*float64(time.Second)) + time.Millisecond
	}
	b.tokens--
	return true, 0
}

// reap drops buckets idle long enough to have fully refilled, bounding memory.
// Call periodically; safe to skip (the map just grows with distinct keys).
func (t *tokenBucket) reap() {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	fullAfter := time.Duration(t.capacity/t.rate*float64(time.Second)) + time.Second
	for k, b := range t.buckets {
		if now.Sub(b.last) > fullAfter {
			delete(t.buckets, k)
		}
	}
}
