package app

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/health"
	"github.com/oleg-tkachuk/paladin/backend/internal/ratecount"
)

const testValkeyAddr = "valkey.cache.svc:6379"

// valkeyStore is a shared rate-limit store whose counting and pinging fail
// with err, or succeed while err is nil.
type valkeyStore struct{ err error }

func (s *valkeyStore) Bump(context.Context, string) (ratecount.Counts, error) {
	return ratecount.Counts{}, s.err
}
func (s *valkeyStore) BumpIfFits(context.Context, string, int) (ratecount.Counts, error) {
	return ratecount.Counts{}, s.err
}
func (s *valkeyStore) Read(context.Context, string) (ratecount.Counts, error) {
	return ratecount.Counts{}, s.err
}
func (s *valkeyStore) Ping(context.Context) error { return s.err }

func detailValue(c health.Component, name string) (string, bool) {
	for _, d := range c.Details {
		if d.Name == name {
			return d.Value, true
		}
	}
	return "", false
}

// With the rate limits in postgres the page still lists Valkey, off by the
// key that would put them there.
func TestValkeyComponentOffWithThePostgresBackend(t *testing.T) {
	c := valkeyComponent("", nil)
	if c.Name != valkeyComponentName || c.Category != health.CategoryDatabase || c.Critical {
		t.Fatalf("check = %+v; want a non-critical database row named %s", c, valkeyComponentName)
	}
	got := componentOf(t, c)
	if got.Status != health.StatusDisabled || got.Message != "off by configuration: "+config.KeyRateLimitBackend {
		t.Fatalf("component = %+v, want off by %s", got, config.KeyRateLimitBackend)
	}
}

// On, the row follows the store: healthy while it answers a ping, unhealthy
// when it does not — never critical, as a limit decided per process is no
// outage. Its details say where limits are decided now and how many were
// decided in the process.
func TestValkeyComponentFollowsTheStore(t *testing.T) {
	store := &valkeyStore{}
	shared := ratecount.NewDegrading(store, config.RateLimitBackendValkey, zap.NewNop())
	ctx := context.Background()
	_, _ = shared.Bump(ctx, "tenant:t")

	got := componentOf(t, valkeyComponent(testValkeyAddr, shared))
	if got.Status != health.StatusHealthy || got.Control != health.ControlConfig {
		t.Fatalf("answering: component = %+v, want healthy, switched by config", got)
	}
	want := map[string]string{
		detailValkeyAddr:     testValkeyAddr,
		detailLimitsCounted:  limitsCountedShared,
		detailDecidedLocally: "0",
	}
	for name, v := range want {
		if got, ok := detailValue(got, name); !ok || got != v {
			t.Errorf("answering: detail %q = %q (present %v), want %q", name, got, ok, v)
		}
	}

	store.err = errors.New("i/o timeout")
	const outage = 2
	for i := 0; i < outage; i++ {
		_, _ = shared.Bump(ctx, "tenant:t")
	}
	got = componentOf(t, valkeyComponent(testValkeyAddr, shared))
	if got.Status != health.StatusUnhealthy || got.Critical {
		t.Fatalf("down: component = %+v, want unhealthy, not critical", got)
	}
	if v, _ := detailValue(got, detailLimitsCounted); v != limitsCountedPerProcess {
		t.Errorf("down: limits counted %q, want %q", v, limitsCountedPerProcess)
	}
	if v, _ := detailValue(got, detailDecidedLocally); v != "2" {
		t.Errorf("down: decided per process %q, want 2", v)
	}
}
