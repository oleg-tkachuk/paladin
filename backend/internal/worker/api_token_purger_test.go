package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/auth/api_token"
	"github.com/oleg-tkachuk/paladin/internal/auth/api_token/ratelimit"
)

// The purger had no test, and it makes a promise in a comment that nothing
// checked: the rate-bucket sweep is "independent of the row purge so a failure
// on one doesn't skip the other". That is one `if` away from untrue, and the
// comment would still read correctly afterwards. Expired rows pile up or stale
// buckets do, neither raises an error, and the only symptom is a table that
// grows.

type purgeCall struct {
	expiredFor time.Duration
}

type fakeAPITokenStore struct {
	purged chan purgeCall
	err    error
}

func newFakeAPITokenStore(err error) *fakeAPITokenStore {
	return &fakeAPITokenStore{purged: make(chan purgeCall, 8), err: err}
}

func (f *fakeAPITokenStore) PurgeExpired(_ context.Context, expiredFor time.Duration) (int64, error) {
	record(f.purged, purgeCall{expiredFor: expiredFor})
	if f.err != nil {
		return 0, f.err
	}
	return 5, nil
}

// The rest of the seam, unused by the purger and present so the fake satisfies
// api_token.Store.
func (f *fakeAPITokenStore) Insert(context.Context, api_token.Token, []byte) error { return nil }
func (f *fakeAPITokenStore) FindByDigest(context.Context, []byte) (api_token.Token, error) {
	return api_token.Token{}, api_token.ErrTokenNotFound
}
func (f *fakeAPITokenStore) Get(context.Context, uuid.UUID) (api_token.Token, error) {
	return api_token.Token{}, api_token.ErrTokenNotFound
}
func (f *fakeAPITokenStore) Revoke(context.Context, uuid.UUID) error { return nil }
func (f *fakeAPITokenStore) TouchLastUsed(context.Context, uuid.UUID, uuid.UUID, time.Time) error {
	return nil
}
func (f *fakeAPITokenStore) ListByTenant(context.Context, api_token.ListByTenantArgs) ([]api_token.Token, string, error) {
	return nil, "", nil
}

type fakeSweepLimiter struct {
	swept chan time.Duration
}

func newFakeSweepLimiter() *fakeSweepLimiter {
	return &fakeSweepLimiter{swept: make(chan time.Duration, 8)}
}

func (f *fakeSweepLimiter) Sweep(_ context.Context, olderThan time.Duration) (int64, error) {
	record(f.swept, olderThan)
	return 2, nil
}
func (f *fakeSweepLimiter) Allow(context.Context, uuid.UUID, int) (ratelimit.Decision, error) {
	return ratelimit.Decision{Allowed: true}, nil
}
func (f *fakeSweepLimiter) Usage(context.Context, uuid.UUID) (ratelimit.Snapshot, error) {
	return ratelimit.Snapshot{}, nil
}

var (
	_ api_token.Store   = (*fakeAPITokenStore)(nil)
	_ ratelimit.Limiter = (*fakeSweepLimiter)(nil)
)

func TestAPITokenPurger_SweepsEvenWhenThePurgeFails(t *testing.T) {
	// The promise in the comment, as a test. A `return err` after the purge
	// would satisfy every other assertion in this file and silently stop the
	// bucket sweep from ever running on a cluster whose purge is failing —
	// exactly when the tables are growing.
	store := newFakeAPITokenStore(errors.New("relation is being vacuumed"))
	limiter := newFakeSweepLimiter()
	p := &APITokenPurger{Store: store, Limiter: limiter, Interval: time.Millisecond}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = p.Run(ctx) }()
	defer func() { cancel(); <-done }()

	select {
	case <-store.purged:
	case <-time.After(5 * time.Second):
		t.Fatal("the purge never ran")
	}
	select {
	case grace := <-limiter.swept:
		if grace != 5*time.Minute {
			t.Errorf("sweep grace = %s, want 5m — the sliding window reads back two buckets", grace)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the purge failed and took the rate-bucket sweep down with it")
	}
}

func TestAPITokenPurger_ExpiredForDefault(t *testing.T) {
	// Zero means "use the default", not "purge everything" — the same shape as
	// the page-size clamps elsewhere, and the direction that would hurt here is
	// a zero grace dropping rows an operator is still reading in admin tooling.
	for name, tc := range map[string]struct {
		set  time.Duration
		want time.Duration
	}{
		"unset falls back to seven days": {0, 7 * 24 * time.Hour},
		"negative falls back too":        {-time.Hour, 7 * 24 * time.Hour},
		"an explicit value is kept":      {36 * time.Hour, 36 * time.Hour},
	} {
		t.Run(name, func(t *testing.T) {
			store := newFakeAPITokenStore(nil)
			p := &APITokenPurger{Store: store, Interval: time.Millisecond, ExpiredFor: tc.set}

			got := runUntilCalled(t, p.Run, store.purged)

			if got.expiredFor != tc.want {
				t.Errorf("PurgeExpired grace = %s, want %s", got.expiredFor, tc.want)
			}
		})
	}
}

func TestAPITokenPurger_NoLimiterStillPurges(t *testing.T) {
	// Limiter is optional wiring; a nil one must skip the sweep rather than
	// panic on the first tick and crash-loop the dispatcher.
	store := newFakeAPITokenStore(nil)
	p := &APITokenPurger{Store: store, Interval: time.Millisecond}

	if got := runUntilCalled(t, p.Run, store.purged); got.expiredFor != 7*24*time.Hour {
		t.Errorf("PurgeExpired grace = %s, want the default", got.expiredFor)
	}
}

func TestAPITokenPurger_DisabledByInterval(t *testing.T) {
	t.Parallel()
	for name, interval := range map[string]time.Duration{"zero": 0, "negative": -time.Second} {
		t.Run(name, func(t *testing.T) {
			store := newFakeAPITokenStore(nil)
			p := &APITokenPurger{Store: store, Interval: interval}
			if err := p.Run(context.Background()); err != nil {
				t.Fatalf("a disabled purger must return nil, got %v", err)
			}
			if len(store.purged) != 0 {
				t.Error("a disabled purger reached its store")
			}
		})
	}
}
