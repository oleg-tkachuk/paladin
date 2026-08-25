package middleware

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/auth"
)

// nopNext counts calls so a test can tell "throttled" from "handled".
func nopNext(calls *int) connect.UnaryFunc {
	return func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		*calls++
		return nil, nil
	}
}

func rlTenantCtx(t *testing.T, id uuid.UUID) context.Context {
	t.Helper()
	return auth.WithPrincipal(context.Background(), &auth.Principal{TenantID: id})
}

func req() connect.AnyRequest {
	return connect.NewRequest(&struct{}{})
}

// The limit has to bite, or wiring it changes nothing. Burst 2 admits two and
// refuses the third within the same instant.
func TestTenantRateLimit_ThrottlesPastBurst(t *testing.T) {
	i := NewTenantRateLimitInterceptor(TenantRateLimitConfig{RPS: 1, Burst: 2})
	calls := 0
	h := i.WrapUnary(nopNext(&calls))
	ctx := rlTenantCtx(t, uuid.New())

	for n := range 2 {
		if _, err := h(ctx, req()); err != nil {
			t.Fatalf("call %d: %v", n, err)
		}
	}
	_, err := h(ctx, req())
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("third call err = %v, want CodeResourceExhausted", err)
	}
	if calls != 2 {
		t.Errorf("handler ran %d times, want 2 — the throttled call reached it", calls)
	}
	var cerr *connect.Error
	if errors.As(err, &cerr) && cerr.Meta().Get("Retry-After") == "" {
		t.Error("no Retry-After: an integrating client cannot tell how long to back off")
	}
}

// One noisy tenant must not spend another's allowance — that is the whole
// point of a per-tenant bucket rather than a global one.
func TestTenantRateLimit_BucketsArePerTenant(t *testing.T) {
	i := NewTenantRateLimitInterceptor(TenantRateLimitConfig{RPS: 1, Burst: 1})
	calls := 0
	h := i.WrapUnary(nopNext(&calls))

	noisy := rlTenantCtx(t, uuid.New())
	quiet := rlTenantCtx(t, uuid.New())

	if _, err := h(noisy, req()); err != nil {
		t.Fatalf("noisy first: %v", err)
	}
	if _, err := h(noisy, req()); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("noisy second err = %v, want CodeResourceExhausted", err)
	}
	if _, err := h(quiet, req()); err != nil {
		t.Fatalf("quiet tenant was charged for the noisy one: %v", err)
	}
}

// Pre-auth surfaces (health, login) carry no tenant. Charging them all to one
// shared bucket would let any unauthenticated caller deny service to the rest.
func TestTenantRateLimit_TenantlessRequestsPassThrough(t *testing.T) {
	i := NewTenantRateLimitInterceptor(TenantRateLimitConfig{RPS: 1, Burst: 1})
	calls := 0
	h := i.WrapUnary(nopNext(&calls))

	for n := range 5 {
		if _, err := h(context.Background(), req()); err != nil {
			t.Fatalf("call %d: %v", n, err)
		}
	}
	if calls != 5 {
		t.Errorf("handler ran %d times, want 5", calls)
	}
}

// Disabled must be a pass-through in the chain, not a nil interceptor Connect
// would dereference on the first request.
func TestTenantRateLimit_DisabledPassesEverything(t *testing.T) {
	i := NewTenantRateLimitInterceptor(TenantRateLimitConfig{RPS: 0})
	calls := 0
	h := i.WrapUnary(nopNext(&calls))
	ctx := rlTenantCtx(t, uuid.New())

	for n := range 50 {
		if _, err := h(ctx, req()); err != nil {
			t.Fatalf("call %d: %v", n, err)
		}
	}
	if calls != 50 {
		t.Errorf("handler ran %d times, want 50", calls)
	}
}

// The bucket refills, or a tenant that trips the limit once stays broken.
func TestTenantRateLimit_Refills(t *testing.T) {
	i := NewTenantRateLimitInterceptor(TenantRateLimitConfig{RPS: 100, Burst: 1})
	calls := 0
	h := i.WrapUnary(nopNext(&calls))
	ctx := rlTenantCtx(t, uuid.New())

	if _, err := h(ctx, req()); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := h(ctx, req()); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("second err = %v, want CodeResourceExhausted", err)
	}
	// At 100 rps a token is back in 10ms; 50ms is slack for a loaded CI box.
	time.Sleep(50 * time.Millisecond)
	if _, err := h(ctx, req()); err != nil {
		t.Fatalf("after refill: %v", err)
	}
}
