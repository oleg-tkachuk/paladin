package paladin_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// capabilityLifetime is how long the test mint's capabilities live.
const capabilityLifetime = 15 * time.Minute

// testMint issues numbered tokens per key, counting calls; gate, when set,
// holds every mint until it is closed.
type testMint struct {
	calls atomic.Int32
	gate  chan struct{}
	fail  atomic.Bool
	now   func() time.Time
}

func (m *testMint) mint(_ context.Context, key string) (string, time.Time, error) {
	n := m.calls.Add(1)
	if m.gate != nil {
		<-m.gate
	}
	if m.fail.Load() {
		return "", time.Time{}, errors.New("issuer down")
	}
	return fmt.Sprintf("%s-%d", key, n), m.now().Add(capabilityLifetime), nil
}

func TestCapabilityCacheKeepsOnePerKeyUntilNearExpiry(t *testing.T) {
	clk := &clock{now: time.Unix(0, 0)}
	m := &testMint{now: clk.Now}
	c := paladin.NewCapabilityCache(m.mint, paladin.WithCapabilityCacheClock(clk.Now))
	ctx := context.Background()

	a, _ := c.Token(ctx, "tenant-a")
	if again, _ := c.Token(ctx, "tenant-a"); again != a {
		t.Errorf("a cached capability was replaced: %q then %q", a, again)
	}
	if b, _ := c.Token(ctx, "tenant-b"); b == a {
		t.Error("two keys shared one capability")
	}
	clk.advance(capabilityLifetime - paladin.DefaultCapabilityRefreshMargin + time.Second)
	if renewed, _ := c.Token(ctx, "tenant-a"); renewed == a {
		t.Error("a capability within the refresh margin of expiry was served")
	}
	if got := m.calls.Load(); got != 3 {
		t.Errorf("minted %d times, want 3", got)
	}
}

func TestCapabilityCacheMintsOnceForConcurrentCallers(t *testing.T) {
	m := &testMint{gate: make(chan struct{}), now: time.Now}
	c := paladin.NewCapabilityCache(m.mint)
	var wg sync.WaitGroup
	tokens := make([]string, parallelCallers)
	for i := range parallelCallers {
		wg.Go(func() { tokens[i], _ = c.Token(context.Background(), "tenant-a") })
	}
	time.Sleep(20 * time.Millisecond) // let every caller reach the cache
	close(m.gate)
	wg.Wait()
	if got := m.calls.Load(); got != 1 {
		t.Errorf("minted %d times for %d concurrent callers, want 1", got, parallelCallers)
	}
	for _, tok := range tokens {
		if tok != tokens[0] {
			t.Fatalf("callers got different capabilities: %v", tokens)
		}
	}
}

// A failed mint is not cached: the next call mints again rather than
// serving nothing or waiting forever.
func TestCapabilityCacheDoesNotKeepAFailedMint(t *testing.T) {
	m := &testMint{now: time.Now}
	m.fail.Store(true)
	c := paladin.NewCapabilityCache(m.mint)
	if _, err := c.Token(context.Background(), "tenant-a"); err == nil {
		t.Fatal("a failed mint returned no error")
	}
	m.fail.Store(false)
	if tok, err := c.Token(context.Background(), "tenant-a"); err != nil || tok == "" {
		t.Fatalf("after recovery: (%q, %v)", tok, err)
	}
}

func TestCapabilityCacheInvalidateMintsAfresh(t *testing.T) {
	m := &testMint{now: time.Now}
	c := paladin.NewCapabilityCache(m.mint)
	first, _ := c.Token(context.Background(), "tenant-a")
	c.Invalidate("tenant-a")
	if again, _ := c.Token(context.Background(), "tenant-a"); again == first {
		t.Error("an invalidated capability was served again")
	}
}

func TestCapabilityCacheRefusesAnEmptyKey(t *testing.T) {
	c := paladin.NewCapabilityCache((&testMint{now: time.Now}).mint)
	if _, err := c.Token(context.Background(), ""); !errors.Is(err, paladin.ErrNoCapabilityKey) {
		t.Errorf("err = %v, want ErrNoCapabilityKey", err)
	}
}
