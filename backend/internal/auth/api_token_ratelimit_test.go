package auth

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token/ratelimit"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// recordingLimiter is the interceptor-side test stub. Tracks call count
// and returns a configurable Decision. Lives here (not in the ratelimit
// package) because the interceptor lives here.
type recordingLimiter struct {
	calls atomic.Int64
	resp  ratelimit.Decision
}

func (r *recordingLimiter) Allow(context.Context, uuid.UUID, int) (ratelimit.Decision, error) {
	r.calls.Add(1)
	return r.resp, nil
}

func (r *recordingLimiter) Usage(context.Context, uuid.UUID) (ratelimit.Snapshot, error) {
	return ratelimit.Snapshot{}, nil
}

func (r *recordingLimiter) Sweep(context.Context, time.Duration) (int64, error) {
	return 0, nil
}

// TestRateLimitGate_NilLimiter confirms the gate is skipped when the
// interceptor was wired without a Limiter — no DB call, no error.
func TestRateLimitGate_NilLimiter(t *testing.T) {
	t.Parallel()
	i := &apiTokenInterceptor{verifier: nil, limiter: nil, audience: "data"}
	tok := &api_token.Token{ID: uuid.New(), RateLimitRPM: 60}
	if err := i.rateLimitGate(context.Background(), tok); err != nil {
		t.Fatalf("expected nil for nil limiter, got %v", err)
	}
}

// TestRateLimitGate_TokenUnlimited confirms the gate is skipped when
// the token's RateLimitRPM is 0 (unlimited tokens never touch the
// limiter even when one is configured).
func TestRateLimitGate_TokenUnlimited(t *testing.T) {
	t.Parallel()
	rl := &recordingLimiter{}
	i := &apiTokenInterceptor{limiter: rl, audience: "data"}
	tok := &api_token.Token{ID: uuid.New(), RateLimitRPM: 0}
	if err := i.rateLimitGate(context.Background(), tok); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if rl.calls.Load() != 0 {
		t.Errorf("limiter called for unlimited token: %d times", rl.calls.Load())
	}
}

// TestRateLimitGate_Allowed exercises the happy path — limiter says
// yes, gate returns nil.
func TestRateLimitGate_Allowed(t *testing.T) {
	t.Parallel()
	rl := &recordingLimiter{resp: ratelimit.Decision{Allowed: true}}
	i := &apiTokenInterceptor{limiter: rl, audience: "data"}
	tok := &api_token.Token{ID: uuid.New(), RateLimitRPM: 60}
	if err := i.rateLimitGate(context.Background(), tok); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if rl.calls.Load() != 1 {
		t.Errorf("limiter call count: got %d, want 1", rl.calls.Load())
	}
}

// TestRateLimitGate_Denied confirms a denied decision surfaces as
// connect.CodeResourceExhausted with a Retry-After header on the call's
// response header, which the transport sends with the error. The gate
// writes that header to the server-side CallInfo, so it runs inside a
// call that has one.
func TestRateLimitGate_Denied(t *testing.T) {
	t.Parallel()
	const (
		rpm        = 60
		overLimit  = 65
		retryAfter = 30 * time.Second
	)
	rl := &recordingLimiter{resp: ratelimit.Decision{
		Allowed:       false,
		WeightedCount: overLimit,
		RetryAfter:    retryAfter,
	}}
	i := &apiTokenInterceptor{limiter: rl, audience: planeData}
	tok := &api_token.Token{ID: uuid.New(), RateLimitRPM: rpm}
	gate := func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			if err := i.rateLimitGate(ctx, tok); err != nil {
				return err
			}
			return next(ctx, spec, stream)
		}
	}

	c := callProbe(context.Background(), []connect.ServerInterceptor{gate})
	if c.err == nil {
		t.Fatal("expected denial error, got nil")
	}
	var ce *connect.Error
	if !errors.As(c.err, &ce) {
		t.Fatalf("expected *connect.Error, got %T", c.err)
	}
	if ce.Code() != connect.CodeResourceExhausted {
		t.Errorf("code: got %v, want ResourceExhausted", ce.Code())
	}
	want := strconv.Itoa(int(retryAfter.Seconds()))
	if got := c.responseHeader.Get(paladin.HeaderRetryAfter); got != want {
		t.Errorf("%s: got %q, want %q", paladin.HeaderRetryAfter, got, want)
	}
}
