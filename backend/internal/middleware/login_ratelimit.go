package middleware

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
)

// LoginRateLimiter is a Connect interceptor that throttles credential-
// bearing IAM RPCs (Login + RefreshToken) with two layered sliding
// windows:
//
//  1. Per-IP cap (`PerIPMax` / `Window`) — fires first; defends against
//     enumeration attacks where the attacker varies `subject` to dodge the
//     tighter cap. Without this layer a single IP could allocate one bucket
//     per fabricated subject and exhaust process memory.
//  2. Per-(subject, IP) cap (`PerSubjectMax` / `Window`) — defends a
//     specific account against credential stuffing.
//
// A request must clear BOTH caps. Either layer fires `CodeResourceExhausted`.
// Successful logins are NOT exempted — bots would otherwise game the limit
// by occasionally guessing right.
//
// Memory is bounded two ways: an opportunistic sweep drops emptied buckets
// once per window, and a hard maxKeys cap rejects new keys (fail-closed)
// if the map ever blows past it — so a botnet rotating subjects/IPs can't
// OOM the IAM pod.
type LoginRateLimiter struct {
	PerSubjectMax int
	PerIPMax      int
	Window        time.Duration
	procedures    map[string]struct{}
	realIPHeader  string
	maxKeys       int

	mu        sync.Mutex
	buckets   map[string][]time.Time
	lastSweep time.Time
	now       func() time.Time
}

// NewLoginRateLimiter constructs a limiter with sensible defaults:
// 10 attempts/min per (subject, IP), 60 attempts/min per IP. realIPHeader
// names the header the ingress writes the client IP into (it MUST
// overwrite, not append — otherwise a client can spoof the per-IP key);
// empty falls back to the first X-Forwarded-For hop. procedures lists the
// RPC paths to throttle; empty defaults to Login + RefreshToken.
func NewLoginRateLimiter(realIPHeader string, procedures ...string) *LoginRateLimiter {
	if len(procedures) == 0 {
		procedures = []string{
			"/paladin.iam.v1.AuthService/Login",
			"/paladin.iam.v1.AuthService/RefreshToken",
		}
	}
	procSet := make(map[string]struct{}, len(procedures))
	for _, p := range procedures {
		procSet[p] = struct{}{}
	}
	return &LoginRateLimiter{
		PerSubjectMax: 10,
		PerIPMax:      60,
		Window:        1 * time.Minute,
		procedures:    procSet,
		realIPHeader:  realIPHeader,
		maxKeys:       100_000,
		buckets:       map[string][]time.Time{},
		now:           time.Now,
	}
}

func (l *LoginRateLimiter) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if _, throttled := l.procedures[req.Spec().Procedure]; !throttled {
			return next(ctx, req)
		}
		subject, ip := l.coords(req)
		if !l.admit(subject, ip) {
			return nil, connect.NewError(connect.CodeResourceExhausted,
				errors.New("too many attempts; try again later"))
		}
		return next(ctx, req)
	}
}

func (l *LoginRateLimiter) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (l *LoginRateLimiter) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

// coords extracts (subject, ip). Subject from the proto body; IP from the
// configured real-IP header (first hop), or X-Forwarded-For if unset. Both
// empty when absent — the limiter still throttles unidentified clients
// under the shared "ip=" bucket.
func (l *LoginRateLimiter) coords(req connect.AnyRequest) (string, string) {
	type subjectGetter interface{ GetSubject() string }
	subject := ""
	if m, ok := req.Any().(subjectGetter); ok {
		subject = m.GetSubject()
	}
	header := l.realIPHeader
	if header == "" {
		header = "X-Forwarded-For"
	}
	ip := firstFwdedIP(req.Header().Get(header))
	return subject, ip
}

func firstFwdedIP(h string) string {
	if h == "" {
		return ""
	}
	if i := strings.IndexByte(h, ','); i >= 0 {
		return strings.TrimSpace(h[:i])
	}
	return strings.TrimSpace(h)
}

// admit checks both layers atomically. Returns true only when both
// buckets have capacity AND records the timestamp in both. Failing to
// clear the per-IP cap leaves the per-subject bucket untouched.
func (l *LoginRateLimiter) admit(subject, ip string) bool {
	now := l.now()
	cutoff := now.Add(-l.Window)
	subjectKey := "s:" + subject + "|" + ip
	ipKey := "i:" + ip

	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweepLocked(cutoff, now)

	// maxKeys backstop: at saturation, admit only when BOTH keys already
	// exist — otherwise admitting would create the missing bucket below
	// (checkLocked writes l.buckets[key]) and grow the map past the cap.
	// Rejecting when EITHER key is new closes the memory-exhaustion vector
	// where one known IP rotates subjects to mint unbounded subjectKeys.
	// Fail-closed at the cap is acceptable; legitimate repeat clients
	// (both keys present) are unaffected. maxKeys<=0 disables it (tests).
	if l.maxKeys > 0 && len(l.buckets) >= l.maxKeys {
		_, haveIP := l.buckets[ipKey]
		_, haveSubject := l.buckets[subjectKey]
		if !haveIP || !haveSubject {
			return false
		}
	}

	// Per-IP gate first — coarsest layer, also the cheapest to reject on.
	if !l.checkLocked(ipKey, cutoff, l.PerIPMax) {
		return false
	}
	if !l.checkLocked(subjectKey, cutoff, l.PerSubjectMax) {
		return false
	}
	// Both layers have capacity. Record on both so a future credential-
	// stuffing burst sees the right count regardless of which layer it
	// races against.
	l.buckets[ipKey] = append(l.buckets[ipKey], now)
	l.buckets[subjectKey] = append(l.buckets[subjectKey], now)
	return true
}

// sweepLocked drops buckets that hold no in-window timestamps. Runs at
// most once per Window so the cost is amortised. Caller holds l.mu.
func (l *LoginRateLimiter) sweepLocked(cutoff, now time.Time) {
	if now.Sub(l.lastSweep) < l.Window {
		return
	}
	l.lastSweep = now
	for key, ts := range l.buckets {
		fresh := false
		for _, t := range ts {
			if t.After(cutoff) {
				fresh = true
				break
			}
		}
		if !fresh {
			delete(l.buckets, key)
		}
	}
}

// checkLocked reports whether `key` has capacity under `max`, trimming
// stale timestamps as a side-effect. Caller holds l.mu. max <= 0 → no cap.
func (l *LoginRateLimiter) checkLocked(key string, cutoff time.Time, max int) bool {
	if max <= 0 {
		return true
	}
	timestamps := l.buckets[key]
	keep := timestamps[:0]
	for _, t := range timestamps {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	l.buckets[key] = keep
	return len(keep) < max
}
