package middleware

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
)

// LoginRateLimiter is a Connect interceptor that throttles AuthService.Login
// with two layered sliding-windows:
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
// Targets the Login procedure path only; all other RPCs pass through.
type LoginRateLimiter struct {
	PerSubjectMax int
	PerIPMax      int
	Window        time.Duration
	Procedure     string // default "/paladin.iam.v1.AuthService/Login"

	mu      sync.Mutex
	buckets map[string][]time.Time
	now     func() time.Time
}

// NewLoginRateLimiter constructs a limiter with sensible defaults:
// 10 attempts/min per (subject, IP), 60 attempts/min per IP.
func NewLoginRateLimiter() *LoginRateLimiter {
	return &LoginRateLimiter{
		PerSubjectMax: 10,
		PerIPMax:      60,
		Window:        1 * time.Minute,
		Procedure:     "/paladin.iam.v1.AuthService/Login",
		buckets:       map[string][]time.Time{},
		now:           time.Now,
	}
}

func (l *LoginRateLimiter) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if req.Spec().Procedure != l.Procedure {
			return next(ctx, req)
		}
		subject, ip := l.coords(req)
		if !l.admit(subject, ip) {
			return nil, connect.NewError(connect.CodeResourceExhausted,
				errors.New("too many login attempts; try again later"))
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
// first X-Forwarded-For hop. Both empty when unset — the limiter still
// works (e.g. throttles "ip=” all unidentified clients").
func (l *LoginRateLimiter) coords(req connect.AnyRequest) (string, string) {
	type subjectGetter interface{ GetSubject() string }
	subject := ""
	if m, ok := req.Any().(subjectGetter); ok {
		subject = m.GetSubject()
	}
	ip := firstFwdedIP(req.Header().Get("X-Forwarded-For"))
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
