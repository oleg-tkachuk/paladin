package middleware

import (
	"context"
	"errors"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
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
	return f.counts[id], fakeRetryAfterSeconds, nil
}

// fakeRetryAfterSeconds is what fakeRateStore says is left in the bucket.
const fakeRetryAfterSeconds = 30

// limited is a call through one limiter to a probe handler that counts the
// calls reaching it.
func limited(i *TenantRateLimitInterceptor, calls *int) func(ctx context.Context) (*connect.Header, error) {
	probe := &unarytest.Probe{OnCall: func(context.Context) error {
		*calls++
		return nil
	}}
	return func(ctx context.Context) (*connect.Header, error) {
		return unarytest.CallProbe(ctx, probe, []connect.ServerInterceptor{i.Intercept})
	}
}

func rlTenantCtx(t *testing.T, id uuid.UUID) context.Context {
	t.Helper()
	return auth.WithPrincipal(context.Background(), &auth.Principal{TenantID: id})
}

// RPS becomes a per-minute ceiling, so 1 rps admits 60 requests in a window
// and refuses the 61st.
func TestTenantRateLimit_ThrottlesPastCapacity(t *testing.T) {
	i := NewTenantRateLimitInterceptor(TenantRateLimitConfig{RPS: 1, Store: newFakeRateStore()})
	calls := 0
	h := limited(i, &calls)
	ctx := rlTenantCtx(t, uuid.New())

	for n := range 60 {
		if _, err := h(ctx); err != nil {
			t.Fatalf("call %d: %v", n, err)
		}
	}
	header, err := h(ctx)
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("call 61 err = %v, want CodeResourceExhausted", err)
	}
	if calls != 60 {
		t.Errorf("handler ran %d times, want 60 — the throttled call reached it", calls)
	}
	if got, want := header.Get(paladin.HeaderRetryAfter), retryAfterHeader(fakeRetryAfterSeconds); got != want {
		t.Errorf("Retry-After = %q, want %q: an integrating client cannot tell how long to back off", got, want)
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
	hA := limited(podA, &callsA)
	hB := limited(podB, &callsB)
	ctx := rlTenantCtx(t, uuid.New())

	// 30 through each replica exhausts the shared minute exactly.
	for n := range 30 {
		if _, err := hA(ctx); err != nil {
			t.Fatalf("A call %d: %v", n, err)
		}
		if _, err := hB(ctx); err != nil {
			t.Fatalf("B call %d: %v", n, err)
		}
	}
	if _, err := hA(ctx); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("replica A err = %v, want CodeResourceExhausted — the budget is shared", err)
	}
	if _, err := hB(ctx); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("replica B err = %v, want CodeResourceExhausted — the budget is shared", err)
	}
}

// One noisy tenant must not spend another's allowance.
func TestTenantRateLimit_BucketsArePerTenant(t *testing.T) {
	i := NewTenantRateLimitInterceptor(TenantRateLimitConfig{RPS: 1.0 / 60.0, Store: newFakeRateStore()})
	calls := 0
	h := limited(i, &calls)

	noisy := rlTenantCtx(t, uuid.New())
	quiet := rlTenantCtx(t, uuid.New())

	if _, err := h(noisy); err != nil {
		t.Fatalf("noisy first: %v", err)
	}
	if _, err := h(noisy); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("noisy second err = %v, want CodeResourceExhausted", err)
	}
	if _, err := h(quiet); err != nil {
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
	h := limited(i, &calls)

	for n := range 5 {
		if _, err := h(context.Background()); err != nil {
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
	h := limited(i, &calls)
	ctx := rlTenantCtx(t, uuid.New())

	for n := range 10 {
		if _, err := h(ctx); err != nil {
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
		h := limited(i, &calls)
		ctx := rlTenantCtx(t, uuid.New())
		for n := range 50 {
			if _, err := h(ctx); err != nil {
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
