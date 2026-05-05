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
// per (subject, source IP) tuple to slow down credential-stuffing attempts.
//
// Sliding-window: each tuple gets `MaxAttempts` permits per `Window`. Once
// exhausted the request is rejected with `CodeResourceExhausted` until the
// window has rolled forward. Successful logins are NOT exempted — bots
// would otherwise game the limit by occasionally guessing right.
//
// Targets the Login procedure path only; all other RPCs pass through.
type LoginRateLimiter struct {
	MaxAttempts int
	Window      time.Duration
	Procedure   string // default "/paladin.iam.v1.AuthService/Login"

	mu      sync.Mutex
	buckets map[string][]time.Time
	now     func() time.Time
}

// NewLoginRateLimiter constructs a limiter with sensible defaults: 10
// attempts per minute per (subject, IP).
func NewLoginRateLimiter() *LoginRateLimiter {
	return &LoginRateLimiter{
		MaxAttempts: 10,
		Window:      1 * time.Minute,
		Procedure:   "/paladin.iam.v1.AuthService/Login",
		buckets:     map[string][]time.Time{},
		now:         time.Now,
	}
}

func (l *LoginRateLimiter) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if req.Spec().Procedure != l.Procedure {
			return next(ctx, req)
		}
		key := l.bucketKey(req)
		if !l.allow(key) {
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

// bucketKey builds the rate-limit key. Subject comes from the request body
// (LoginRequest has GetSubject); IP from the X-Forwarded-For first hop.
func (l *LoginRateLimiter) bucketKey(req connect.AnyRequest) string {
	type subjectGetter interface{ GetSubject() string }
	subject := ""
	if m, ok := req.Any().(subjectGetter); ok {
		subject = m.GetSubject()
	}
	ip := firstFwdedIP(req.Header().Get("X-Forwarded-For"))
	return subject + "|" + ip
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

// allow reports whether the bucket has capacity. Side-effect: appends the
// current timestamp to the bucket on success.
func (l *LoginRateLimiter) allow(key string) bool {
	now := l.now()
	cutoff := now.Add(-l.Window)

	l.mu.Lock()
	defer l.mu.Unlock()

	// Drop timestamps that have rolled out of the window.
	timestamps := l.buckets[key]
	keep := timestamps[:0]
	for _, t := range timestamps {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	if len(keep) >= l.MaxAttempts {
		// Persist the trimmed slice so future calls can re-check cleanly.
		l.buckets[key] = keep
		return false
	}
	l.buckets[key] = append(keep, now)
	return true
}
