package capability

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// cacheTTL is the lifetime of an entry in the tests below; they step a clock
// past it rather than sleeping.
const cacheTTL = time.Minute

// slowBiscuitLookup blocks until released, counting its calls.
type slowBiscuitLookup struct {
	calls   atomic.Int32
	release chan struct{}
}

func (s *slowBiscuitLookup) IsBiscuitRevoked(context.Context, [][]byte) (bool, error) {
	s.calls.Add(1)
	<-s.release
	return true, nil
}

// The Biscuit cache shares concurrent misses for one token as the
// capability cache does.
func TestBiscuitRevocationCacheCoalescesConcurrentMisses(t *testing.T) {
	up := &slowBiscuitLookup{release: make(chan struct{})}
	c := NewCachedBiscuitRevocationChecker(up, cacheTTL)
	ids := [][]byte{[]byte("a"), []byte("b")}

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if r, err := c.IsBiscuitRevoked(context.Background(), ids); err != nil || !r {
				t.Errorf("IsBiscuitRevoked = %v, %v", r, err)
			}
		})
	}
	time.Sleep(20 * time.Millisecond)
	close(up.release)
	wg.Wait()
	if n := up.calls.Load(); n != 1 {
		t.Errorf("upstream calls = %d, want 1", n)
	}
}

// Sweep drops what has expired and keeps what has not, in both caches.
func TestRevocationCachesSweep(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	clock := WithCacheClock(func() time.Time { return now })
	caps := NewCachedRevocationChecker(newMemStore(), cacheTTL, clock)
	copies := NewCachedBiscuitRevocationChecker(&countingBiscuitLookup{}, cacheTTL, clock)

	old := uuid.New()
	_, _ = caps.IsRevoked(ctx, old)
	_, _ = copies.IsBiscuitRevoked(ctx, [][]byte{[]byte("old")})
	now = now.Add(cacheTTL)
	_, _ = caps.IsRevoked(ctx, uuid.New())
	_, _ = copies.IsBiscuitRevoked(ctx, [][]byte{[]byte("new")})

	if n := caps.Sweep(); n != 1 || len(caps.cache.entries) != 1 {
		t.Errorf("capability cache Sweep = %d, %d left; want 1 dropped, 1 left", n, len(caps.cache.entries))
	}
	if n := copies.Sweep(); n != 1 || len(copies.cache.entries) != 1 {
		t.Errorf("Biscuit cache Sweep = %d, %d left; want 1 dropped, 1 left", n, len(copies.cache.entries))
	}
	if n := caps.Sweep(); n != 0 {
		t.Errorf("a second Sweep dropped %d", n)
	}
}

// Invalidate drops one entry, so the next check of it asks upstream.
func TestRevocationCacheInvalidate(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	c := NewCachedRevocationChecker(store, cacheTTL)
	id, other := uuid.New(), uuid.New()
	_, _ = c.IsRevoked(ctx, id)
	_, _ = c.IsRevoked(ctx, other)
	store.revoked[id] = true
	if r, _ := c.IsRevoked(ctx, id); r {
		t.Fatal("a cached answer should stand until invalidated")
	}
	c.Invalidate(id)
	if r, _ := c.IsRevoked(ctx, id); !r {
		t.Error("after Invalidate the revocation must be seen")
	}
	if len(c.cache.entries) != 2 {
		t.Errorf("Invalidate of one id left %d entries, want both ids cached again", len(c.cache.entries))
	}
}
