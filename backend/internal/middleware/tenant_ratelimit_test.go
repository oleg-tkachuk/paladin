package middleware

import (
	"context"
	"errors"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/auth"
)

// fakeRateStore is a shared sliding window in memory: one counter per tenant,
// visible to every interceptor holding this store. That is the property the
// Postgres table provides in production, and modelling it here is what lets
// the "two replicas share one budget" test mean anything.
type fakeRateStore struct {
	mu     sync.Mutex
	counts map[uuid.UUID]float64
	err    error
	calls  int
}

func newFakeRateStore() *fakeRateStore {
	return &fakeRateStore{counts: map[uuid.UUID]float64{}}
}

func (f *fakeRateStore) BumpTenantRate(_ context.Context, id uuid.UUID) (float64, float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return 0, 0, f.err
	}
	f.counts[id]++
	return f.counts[id], 30, nil
}

func nopNext(calls *int) connect.UnaryFunc {
	return func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
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

// RPS becomes a per-minute ceiling, so 1 rps admits 60 requests in a window
// and refuses the 61st.
func TestTenantRateLimit_ThrottlesPastCapacity(t *testing.T) {
	i := NewTenantRateLimitInterceptor(TenantRateLimitConfig{RPS: 1, Store: newFakeRateStore()})
	calls := 0
	h := i.WrapUnary(nopNext(&calls))
	ctx := rlTenantCtx(t, uuid.New())

	for n := range 60 {
		if _, err := h(ctx, req()); err != nil {
			t.Fatalf("call %d: %v", n, err)
		}
	}
	_, err := h(ctx, req())
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("call 61 err = %v, want CodeResourceExhausted", err)
	}
	if calls != 60 {
		t.Errorf("handler ran %d times, want 60 — the throttled call reached it", calls)
	}
	var cerr *connect.Error
	if errors.As(err, &cerr) && cerr.Meta().Get("Retry-After") == "" {
		t.Error("no Retry-After: an integrating client cannot tell how long to back off")
	}
}

// The point of moving the counters into Postgres: two processes must spend
// ONE budget. With a per-process bucket each interceptor below would admit
// the full capacity, and the tenant would get double what the operator set.
func TestTenantRateLimit_ReplicasShareOneBudget(t *testing.T) {
	store := newFakeRateStore()
	podA := NewTenantRateLimitInterceptor(TenantRateLimitConfig{RPS: 1, Store: store})
	podB := NewTenantRateLimitInterceptor(TenantRateLimitConfig{RPS: 1, Store: store})

	callsA, callsB := 0, 0
	hA := podA.WrapUnary(nopNext(&callsA))
	hB := podB.WrapUnary(nopNext(&callsB))
	ctx := rlTenantCtx(t, uuid.New())

	// 30 through each replica exhausts the shared minute exactly.
	for n := range 30 {
		if _, err := hA(ctx, req()); err != nil {
			t.Fatalf("A call %d: %v", n, err)
		}
		if _, err := hB(ctx, req()); err != nil {
			t.Fatalf("B call %d: %v", n, err)
		}
	}
	if _, err := hA(ctx, req()); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("replica A err = %v, want CodeResourceExhausted — the budget is shared", err)
	}
	if _, err := hB(ctx, req()); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("replica B err = %v, want CodeResourceExhausted — the budget is shared", err)
	}
}

// One noisy tenant must not spend another's allowance.
func TestTenantRateLimit_BucketsArePerTenant(t *testing.T) {
	i := NewTenantRateLimitInterceptor(TenantRateLimitConfig{RPS: 1.0 / 60.0, Store: newFakeRateStore()})
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

// Pre-auth surfaces carry no tenant. Charging them all to one shared bucket
// would let any unauthenticated caller deny service to the rest — and it
// would put a database round-trip on the health check.
func TestTenantRateLimit_TenantlessRequestsSkipTheStore(t *testing.T) {
	store := newFakeRateStore()
	i := NewTenantRateLimitInterceptor(TenantRateLimitConfig{RPS: 1.0 / 60.0, Store: store})
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
	if store.calls != 0 {
		t.Errorf("store consulted %d times for tenantless requests, want 0", store.calls)
	}
}

// A store error admits the request rather than refusing every tenant because
// Postgres hiccuped — but it must be counted, or the ceiling silently stops
// being in force.
func TestTenantRateLimit_FailsOpenOnStoreError(t *testing.T) {
	store := newFakeRateStore()
	store.err = errors.New("connection refused")
	i := NewTenantRateLimitInterceptor(TenantRateLimitConfig{RPS: 1.0 / 60.0, Store: store})
	calls := 0
	h := i.WrapUnary(nopNext(&calls))
	ctx := rlTenantCtx(t, uuid.New())

	for n := range 10 {
		if _, err := h(ctx, req()); err != nil {
			t.Fatalf("call %d was refused because the store errored: %v", n, err)
		}
	}
	if calls != 10 {
		t.Errorf("handler ran %d times, want 10", calls)
	}
}

// Disabled must be a pass-through in the chain, not a nil interceptor Connect
// would dereference on the first request — and it must not touch the store.
func TestTenantRateLimit_DisabledPassesEverything(t *testing.T) {
	store := newFakeRateStore()
	for _, cfg := range []TenantRateLimitConfig{
		{RPS: 0, Store: store},
		{RPS: 100, Store: nil},
	} {
		i := NewTenantRateLimitInterceptor(cfg)
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
	if store.calls != 0 {
		t.Errorf("disabled limiter still hit the store %d times", store.calls)
	}
}

// Retry-After is seconds, rounded up, never below 1: a client told to retry
// in 0 seconds retries immediately and is refused again.
func TestRetryAfterHeader(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want string
	}{
		{0, "1"}, {0.2, "1"}, {1, "1"}, {1.1, "2"}, {59.4, "60"},
	} {
		if got := retryAfterHeader(tc.in); got != tc.want {
			t.Errorf("retryAfterHeader(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
