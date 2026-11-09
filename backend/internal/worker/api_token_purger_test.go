package worker

import (
	"context"
	"testing"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token"
)

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

var _ api_token.Store = (*fakeAPITokenStore)(nil)

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
